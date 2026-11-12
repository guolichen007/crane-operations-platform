package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This receipt records a human-reviewed result. The worker has no Keycloak Admin API client.
type provisionReceipt struct {
	StaffID   string `json:"staffId"`
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	Subject   string `json:"subject"`
	Email     string `json:"email"`
	// Phone is checked against the record (exact canonical value) when present.
	Phone             string   `json:"phone,omitempty"`
	IdentityVerified  bool     `json:"identityVerified"`
	UniqueEmail       bool     `json:"uniqueEmail"`
	ClientRoles       []string `json:"clientRoles,omitempty"`
	ApprovalReference string   `json:"approvalReference"`
	Reason            string   `json:"reason"`
}

func applyProvisionReceipt(ctx context.Context, db *pgxpool.Pool, raw []byte, operator string, dry bool) (string, error) {
	if len(raw) == 0 || len(raw) > identityLimit || !evidenceID.MatchString(operator) {
		return "", staffInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := uniqueJSON(decoder); err != nil {
		return "", staffInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", staffInvalid
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var in provisionReceipt
	if err := decoder.Decode(&in); err != nil || !uuidPattern.MatchString(in.StaffID) || in.Version < 1 ||
		!reasonOK(in.Reason) || !evidenceID.MatchString(in.ApprovalReference) ||
		!contains([]string{"approve", "ack-suspend", "fail"}, in.Operation) ||
		strings.HasPrefix(in.ApprovalReference, reviewReferencePrefix) ||
		in.Operation != "approve" && in.ClientRoles != nil {
		return "", staffInvalid
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
		return "", err
	}
	receipt := hash(string(raw))
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_outbox WHERE staff_id=$1 AND receipt_hash=$2)`, in.StaffID, receipt).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return "ALREADY_APPLIED", nil
	}
	var subject, email, phone, state, job, operation, role string
	var version int
	var roleChanged bool
	err = tx.QueryRow(ctx, `SELECT u.subject,coalesce(u.work_email,''),coalesce(u.work_phone,''),u.staff_status,u.staff_version,o.id::text,o.operation,u.role,
 EXISTS(SELECT 1 FROM staff_audit a WHERE a.target_id=u.staff_id AND a.action='staff.role-change')
 FROM app_users u JOIN staff_outbox o ON o.staff_id=u.staff_id
 WHERE u.staff_id=$1 AND u.staff_managed AND o.status='PENDING'
 ORDER BY o.staff_version DESC LIMIT 1`, in.StaffID).Scan(&subject, &email, &phone, &state, &version, &job, &operation, &role, &roleChanged)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (version != in.Version || email != in.Email || in.Phone != "" && in.Phone != phone) {
		return "", staffConflict
	}
	if err != nil {
		return "", err
	}
	next, result, jobState := state, "ACKNOWLEDGED", "ACKNOWLEDGED"
	switch in.Operation {
	case "approve":
		// With an email the operator attests it is unique and verified in Keycloak.
		// Without one, the exact subject plus the exact Baobei client role is the
		// binding: the receipt must then name that single client role explicitly.
		if state != staffPending || operation != "PROVISION" || !in.IdentityVerified ||
			email != "" && !in.UniqueEmail || email == "" && in.UniqueEmail ||
			!validIdentityUUID(in.Subject) || subject != in.Subject && !strings.HasPrefix(subject, "pending:") {
			return "", staffDenied
		}
		if (roleChanged || in.ClientRoles != nil || email == "") && !exactClientRole(in.ClientRoles, role) {
			return "", staffDenied
		}
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_users WHERE subject=$1 AND staff_id<>$2)`, in.Subject, in.StaffID).Scan(&exists); err != nil {
			return "", err
		}
		if exists {
			return "", staffConflict
		}
		subject, next, result = in.Subject, staffActivation, staffActivation
	case "ack-suspend":
		if operation != "SUSPEND" || state != staffSuspended && state != staffArchived {
			return "", staffConflict
		}
		if strings.HasPrefix(subject, "pending:") {
			if in.Subject != "" {
				return "", staffDenied
			}
		} else if subject != in.Subject || !validIdentityUUID(in.Subject) || !in.IdentityVerified {
			return "", staffDenied
		}
	case "fail":
		if in.Subject != "" {
			return "", staffInvalid
		}
		jobState, result = "FAILED", staffFailed
		if operation == "PROVISION" && state == staffPending {
			next = staffFailed
		}
	}
	version++
	if _, err = tx.Exec(ctx, `UPDATE app_users SET subject=$2,staff_status=$3,active=false,staff_version=$4 WHERE staff_id=$1`, in.StaffID, subject, next, version); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE staff_outbox SET status=$2,receipt_hash=$3,processed_at=now() WHERE id=$1`, job, jobState, receipt); err != nil {
		return "", err
	}
	if err = staffAudit(ctx, tx, in.StaffID, operator, "staff.provision."+in.Operation, in.ApprovalReference+" · "+in.Reason, version); err != nil {
		return "", err
	}
	if dry {
		return "DRY_RUN_" + result, nil
	}
	return result, tx.Commit(ctx)
}

func provisionCommand(ctx context.Context, db *pgxpool.Pool, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("staff-provision", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("file", "", "受控回执")
	operator := flags.String("operator", "", "审批执行人")
	dry := flags.Bool("dry-run", false, "仅校验")
	once := flags.Bool("once", false, "只处理一份回执")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !*once || *file == "" {
		return errors.New("staff-provision 参数无效")
	}
	body, err := readIdentityFile(*file)
	if err != nil {
		return errors.New("staff-provision 要求 root-only 回执文件")
	}
	status, err := applyProvisionReceipt(ctx, db, body, *operator, *dry)
	if err != nil {
		return errors.New("staff-provision 回执被拒绝，未变更人员")
	}
	return json.NewEncoder(out).Encode(map[string]string{"status": status})
}

// The caller has verified the signed ID token and holds the identity lock.
func authorizeOIDCSession(ctx context.Context, tx pgx.Tx, subject string, email loginEmail, roles []string, activate bool) (string, error) {
	var target, role, status, expected string
	var version int
	var active, managed, roleChanged bool
	var accountState string
	err := tx.QueryRow(ctx, `SELECT staff_id::text,role,staff_status,coalesce(work_email,''),staff_version,active,staff_managed,
 EXISTS(SELECT 1 FROM staff_audit a WHERE a.target_id=u.staff_id AND a.action='staff.role-change'),account_state
 FROM app_users u WHERE subject=$1`, subject).Scan(&target, &role, &status, &expected, &version, &active, &managed, &roleChanged, &accountState)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", loginNotProvisioned
	}
	if err != nil {
		return "", err
	}
	// An account being created, or waiting for review after an inconsistent
	// result, cannot sign in until the account service has settled it.
	if accountState == accountCreating || accountState == accountReview {
		return "", loginNotProvisioned
	}
	if status == staffSuspended || status == staffArchived || status == staffRevoked {
		return "", loginDisabled
	}
	if !active && !(activate && managed && status == staffActivation) {
		return "", loginNotProvisioned
	}
	if !contains(roles, role) {
		return "", loginRoleMissing
	}
	if roleChanged && !exactClientRole(roles, role) {
		return "", loginRoleMissing
	}
	if managed && expected != "" {
		canonical, err := normalizedEmail(email.address)
		if err != nil || !email.verified || canonical != expected {
			return "", loginEmailMismatch
		}
	} else if managed && !active && !exactClientRole(roles, role) {
		// No email on record: activation relies on the subject bound by the reviewed
		// receipt and on exactly one Baobei client role matching the record.
		return "", loginRoleMissing
	}
	// The identity provider has enforced any temporary-password change before
	// issuing this token; record that the member now has their own password.
	if _, err = tx.Exec(ctx, `UPDATE app_users SET account_must_change_password=false WHERE staff_id=$1 AND account_must_change_password`, target); err != nil {
		return "", err
	}
	if active {
		return role, nil
	}
	version++
	if _, err = tx.Exec(ctx, `UPDATE app_users SET staff_status='ACTIVE',active=true,staff_version=$2 WHERE staff_id=$1`, target, version); err != nil {
		return "", err
	}
	return role, staffAudit(ctx, tx, target, subject, "staff.activate", "企业认证完成精确身份与角色校验", version)
}

func exactClientRole(roles []string, expected string) bool {
	return len(roles) == 1 && roles[0] == expected
}

func reauthenticationTime(nonce string, seconds int64, expires time.Time) *time.Time {
	if !strings.HasPrefix(nonce, "reauth-") {
		return nil
	}
	at := time.Unix(seconds, 0)
	if !recentReauth(&at) || at.Before(expires.Add(-5*time.Minute-30*time.Second)) {
		return nil
	}
	return &at
}
