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
	StaffID           string `json:"staffId"`
	Version           int    `json:"version"`
	Operation         string `json:"operation"`
	Subject           string `json:"subject"`
	Email             string `json:"email"`
	IdentityVerified  bool   `json:"identityVerified"`
	UniqueEmail       bool   `json:"uniqueEmail"`
	ApprovalReference string `json:"approvalReference"`
	Reason            string `json:"reason"`
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
		!contains([]string{"approve", "ack-suspend", "fail"}, in.Operation) {
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
	var subject, email, state, job, operation string
	var version int
	err = tx.QueryRow(ctx, `SELECT u.subject,coalesce(u.work_email,''),u.staff_status,u.staff_version,o.id::text,o.operation
 FROM app_users u JOIN staff_outbox o ON o.staff_id=u.staff_id
 WHERE u.staff_id=$1 AND u.staff_managed AND o.status='PENDING'
 ORDER BY o.staff_version DESC LIMIT 1`, in.StaffID).Scan(&subject, &email, &state, &version, &job, &operation)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (version != in.Version || email != in.Email) {
		return "", staffConflict
	}
	if err != nil {
		return "", err
	}
	next, result, jobState := state, "ACKNOWLEDGED", "ACKNOWLEDGED"
	switch in.Operation {
	case "approve":
		if state != staffPending || operation != "PROVISION" || !in.IdentityVerified || !in.UniqueEmail ||
			!validIdentityUUID(in.Subject) || email == "" || subject != in.Subject && !strings.HasPrefix(subject, "pending:") {
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

// The caller has verified the signed ID token, its issuer, audience and nonce.
func (a *App) confirmStaffLogin(ctx context.Context, subject string, roles []string, email string, verified bool) error {
	pool := a.staffPool()
	if pool == nil {
		return errIdentityDenied
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
		return err
	}
	var target, role, expected string
	var version int
	err = tx.QueryRow(ctx, `SELECT staff_id::text,role,coalesce(work_email,''),staff_version FROM app_users
 WHERE subject=$1 AND staff_status='PENDING_ACTIVATION' AND staff_managed`, subject).Scan(&target, &role, &expected, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return errIdentityDenied
	}
	if err != nil {
		return err
	}
	canonical, err := normalizedEmail(email)
	if err != nil || !verified || canonical != expected || !contains(roles, role) {
		return errIdentityDenied
	}
	version++
	if _, err = tx.Exec(ctx, `UPDATE app_users SET staff_status='ACTIVE',active=true,staff_version=$2 WHERE staff_id=$1`, target, version); err != nil {
		return err
	}
	if err = staffAudit(ctx, tx, target, subject, "staff.activate", "企业认证完成精确身份与角色校验", version); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

func verifyManagedEmail(ctx context.Context, tx pgx.Tx, subject string, email loginEmail) error {
	var expected string
	if err := tx.QueryRow(ctx, `SELECT CASE WHEN staff_managed THEN coalesce(work_email,'') ELSE '' END
      FROM app_users WHERE subject=$1 AND active`, subject).Scan(&expected); err != nil {
		return err
	}
	if expected == "" {
		return nil
	}
	actual, err := normalizedEmail(email.address)
	if err != nil || !email.verified || actual != expected {
		return errIdentityDenied
	}
	return nil
}
