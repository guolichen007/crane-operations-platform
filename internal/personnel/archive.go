package personnel

// R9 A1 — controlled personnel deletion.
//
// "删除人员" in the UI is an ARCHIVE, never a row deletion:
//
//   - system_admin only (CapPersonnelArchive); account managers can disable but never delete;
//   - refused for oneself, for protected identities (users.protected_reason: OWNER / SERVICE /
//     SYSTEM), for the last enabled system administrator and for an unfinished account opening;
//   - in ONE transaction under the person locks and the administrator lock: every system, portfolio
//     and project grant is revoked (recorded in the audit change summary), the account is disabled,
//     archived_at / archived_by / reason are set, the session fence moves and every web session is
//     deleted; after commit the live-view leases end and Keycloak is disabled + logged out through
//     the same idempotent identity synchronization as a disable (a Keycloak outage leaves
//     identitySync=PENDING; repeating the archive re-runs only that synchronization);
//   - the users row, its display name and every audit / revision / intake / schedule / work-order
//     reference stay untouched, so historical responsibility never breaks. Archived people are
//     hidden from the default list, refused by every lifecycle operation and can never be enabled
//     again (DB CHECK users_archived_disabled + service guard).
//
// A separate PERMANENT purge exists only for archived TEST accounts that no business history
// references (purgeReferences). It is never cascading: if any reference exists the purge is
// refused and the person stays archived. The username stays reserved (purged_usernames), because
// the local id equals the username and the retained security audit of the purged id must never be
// attributed to a new person. Test-account cleanup works only from an explicit list of exact
// (id, username) pairs (PlanCleanup / ApplyCleanup) — never a name pattern.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
)

// ProtectedReasons are the allowed users.protected_reason values.
var ProtectedReasons = []string{"OWNER", "SERVICE", "SYSTEM"}

// ArchiveResult reports an archive.
type ArchiveResult struct {
	AccountResult
	Revoked RevokedGrants `json:"revoked"`
}

// RevokedGrants lists what an archive removed (also stored in the audit change summary).
type RevokedGrants struct {
	SystemRoles []string `json:"systemRoles"`
	Portfolio   []string `json:"portfolio"`
	Projects    []string `json:"projects"`
}

func listStrings(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func normalizeReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n == 0 || n > 200 {
		return "", InvalidError{"reason", "请填写删除原因（不超过 200 个字符）"}
	}
	return reason, nil
}

// confirmMatches requires the operator to type the exact login username (or local id for an
// unmanaged person) — a second, explicit identification of the target.
func confirmMatches(t target, confirm string) error {
	want := t.username
	if want == "" {
		want = t.id
	}
	if strings.TrimSpace(confirm) != want {
		return InvalidError{"confirmUsername", "请输入该人员的登录用户名以确认"}
	}
	return nil
}

// Archive deletes (archives) a person. version is the person version the caller last saw.
func (s *Service) Archive(ctx context.Context, a Actor, id string, version int64, reason, confirm string) (ArchiveResult, error) {
	var out ArchiveResult
	if _, err := s.Authorize(ctx, a, CapPersonnelArchive); err != nil {
		return out, err
	}
	reason, err := normalizeReason(reason)
	if err != nil {
		return out, err
	}
	if !a.Host && a.ID == id {
		return out, ErrSelf
	}
	err = s.withPeople(ctx, []string{a.ID, id}, func() error {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, adminLock); err != nil {
			return err
		}
		if _, err = authorize(ctx, tx, a, CapPersonnelArchive); err != nil {
			return err
		}
		t, err := lockTarget(ctx, tx, id)
		if err != nil {
			return err
		}
		if t.protected != "" {
			return ErrProtectedIdentity
		}
		if err = confirmMatches(t, confirm); err != nil {
			return err
		}
		if t.archived {
			// Idempotent retry: only the Keycloak half of an earlier archive may still be pending.
			var idpState string
			if err = tx.QueryRowContext(ctx, `SELECT idp_state FROM users WHERE id=$1`, id).Scan(&idpState); err != nil {
				return err
			}
			if !t.managed() || idpState != "PENDING" {
				return ErrArchived
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			if _, err = s.revokeLive(ctx, id); err != nil {
				return err
			}
			out.IdentitySync, err = s.syncIdentity(ctx, s.DB, id, false)
			if err != nil {
				return err
			}
			if out.IdentitySync == "SYNCED" {
				if err = s.refence(ctx, id); err != nil {
					return err
				}
			}
			return auditlog.Write(ctx, s.DB, a.event("identity_sync", map[bool]string{true: "success", false: "pending"}[out.IdentitySync == "SYNCED"], id, "account",
				map[string]any{"archived": true, "retry": true}))
		}
		if version != 0 && t.version != version {
			return fmt.Errorf("%w: the person was changed by someone else", ErrConflict)
		}
		if t.hasRole(RoleSystemAdmin) {
			n, err := otherEnabledAdmins(ctx, tx, id)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrLastAdmin
			}
		}
		if pending, err := unfinishedProvision(ctx, tx, t.username); err != nil {
			return err
		} else if pending {
			return ErrProvisionIncomplete
		}
		if out.Revoked.SystemRoles, err = listStrings(ctx, tx, `DELETE FROM system_memberships WHERE user_id=$1 RETURNING role`, id); err != nil {
			return err
		}
		if out.Revoked.Portfolio, err = listStrings(ctx, tx, `DELETE FROM portfolio_memberships WHERE user_id=$1 RETURNING portfolio_scope_id||':'||role`, id); err != nil {
			return err
		}
		if out.Revoked.Projects, err = listStrings(ctx, tx, `DELETE FROM project_memberships WHERE user_id=$1 RETURNING project_id||':'||role`, id); err != nil {
			return err
		}
		for _, l := range [][]string{out.Revoked.SystemRoles, out.Revoked.Portfolio, out.Revoked.Projects} {
			sort.Strings(l)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET enabled=false, archived_at=now(), archived_by=$2, archive_reason=$3,
idp_state=CASE WHEN $4 THEN 'PENDING' ELSE idp_state END, version=version+1, updated_at=now() WHERE id=$1`,
			id, a.event("", "", "", "", nil).Actor, reason, t.managed()); err != nil {
			return err
		}
		n, err := fence(ctx, tx, id)
		if err != nil {
			return err
		}
		out.SessionsRevoked = n
		// Project grant revocations are also written in the Q2 project_role_changed shape so every
		// project's own membership history shows the removal.
		for _, g := range out.Revoked.Projects {
			project := g[:strings.Index(g, ":")]
			ev := a.event("project_role_changed", "revoked:"+id, "", "", nil)
			ev.Project = project
			if err = auditlog.Write(ctx, tx, ev); err != nil {
				return err
			}
		}
		if err = auditlog.Write(ctx, tx, a.event("personnel_archived", "success", id, "account", map[string]any{
			"reason": reason, "sessionsRevoked": n, "revoked": out.Revoked, "historyPreserved": true,
		})); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if out.LiveLeasesRevoked, err = s.revokeLive(ctx, id); err != nil {
			return err
		}
		out.IdentitySync, err = s.syncIdentity(ctx, s.DB, id, false)
		if err == nil && out.IdentitySync == "SYNCED" {
			err = s.refence(ctx, id)
		}
		return err
	})
	if err != nil {
		return out, err
	}
	out.Person, err = loadPerson(ctx, s.DB, id)
	return out, err
}

// referenceQueries are the business-history references that forbid a permanent purge. Every
// table that records a person as actor / responsible / approver must be listed here; the
// governance test (TestPurgeReferenceCoverage) fails when a migration adds such a column that is
// not covered. Security events ABOUT the person (target_user_id) and the person's own login /
// logout / denial events are not business references; they stay in the audit log after a purge.
type referenceQuery struct{ table, query string }

var referenceQueries = map[string]referenceQuery{
	"audit_actions":           {"audit_logs", `SELECT count(*) FROM audit_logs WHERE user_id=$1 AND action NOT IN ('login_success','logout','authorization_denied')`},
	"entity_revisions":        {"entity_revisions", `SELECT count(*) FROM entity_revisions WHERE changed_by=$1`},
	"project_intakes":         {"project_intakes", `SELECT count(*) FROM project_intakes WHERE $1 IN (registered_by, COALESCE(project_manager_user_id,''), COALESCE(technical_lead_user_id,''), COALESCE(site_lead_user_id,''))`},
	"intake_transitions":      {"project_intake_transitions", `SELECT count(*) FROM project_intake_transitions WHERE actor=$1`},
	"project_activities":      {"project_activities", `SELECT count(*) FROM project_activities WHERE owner_user_id=$1`},
	"person_resources":        {"resources", `SELECT count(*) FROM resources WHERE resource_type='PERSON' AND reference_id=$1`},
	"resource_reservations":   {"resource_reservations", `SELECT count(*) FROM resource_reservations WHERE created_by=$1`},
	"conflict_overrides":      {"schedule_conflict_overrides", `SELECT count(*) FROM schedule_conflict_overrides WHERE overridden_by=$1`},
	"schedule_baselines":      {"schedule_baselines", `SELECT count(*) FROM schedule_baselines WHERE approved_by=$1`},
	"accounts_opened":         {"account_provisions", `SELECT count(*) FROM account_provisions WHERE actor=$1`},
	"people_created":          {"users", `SELECT count(*) FROM users WHERE created_by=$1 AND id<>$1`},
	"roles_granted":           {"system_memberships", `SELECT count(*) FROM system_memberships WHERE granted_by=$1`},
	"maintenance_work_orders": {"maintenance_work_orders", `SELECT count(*) FROM maintenance_work_orders WHERE $1 IN (created_by, COALESCE(assigned_to,''), COALESCE(completed_by,''))`},
	"maintenance_rules":       {"maintenance_rules", `SELECT count(*) FROM maintenance_rules WHERE $1 IN (created_by, COALESCE(approved_by,''))`},
	"wire_rope_inspections":   {"wire_rope_inspections", `SELECT count(*) FROM wire_rope_inspections WHERE inspector=$1`},
	"onboarding_imports":      {"onboarding_imports", `SELECT count(*) FROM onboarding_imports WHERE applied_by=$1`},
	"point_mapping_reviews":   {"telemetry_points", `SELECT count(*) FROM telemetry_points WHERE reviewed_by=$1`},
	"equipment_replacements":  {"equipment_replacements", `SELECT count(*) FROM equipment_replacements WHERE replaced_by=$1`},
	"meter_baselines":         {"equipment_meter_baselines", `SELECT count(*) FROM equipment_meter_baselines WHERE recorded_by=$1`},
}

// purgeReferences counts business references to a person. A table that does not exist on this
// schema yet (Stage A deployed before the Stage B maintenance tables) cannot hold a reference.
func purgeReferences(ctx context.Context, q queryer, id string) (map[string]int, error) {
	out := map[string]int{}
	keys := make([]string, 0, len(referenceQueries))
	for k := range referenceQueries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var exists bool
		if err := q.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+referenceQueries[k].table).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		var n int
		if err := q.QueryRowContext(ctx, referenceQueries[k].query, id).Scan(&n); err != nil {
			return nil, fmt.Errorf("reference check %s: %w", k, err)
		}
		if n > 0 {
			out[k] = n
		}
	}
	return out, nil
}

// PurgeCheck is what the UI needs to offer (or not) a permanent purge.
type PurgeCheck struct {
	Archived   bool           `json:"archived"`
	Protected  bool           `json:"protected"`
	References map[string]int `json:"references"`
	Purgeable  bool           `json:"purgeable"`
}

// CheckPurge reports whether a person could be purged now (read only).
func (s *Service) CheckPurge(ctx context.Context, a Actor, id string) (PurgeCheck, error) {
	var out PurgeCheck
	if _, err := s.Authorize(ctx, a, CapPersonnelArchive); err != nil {
		return out, err
	}
	var protected sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT archived_at IS NOT NULL, protected_reason FROM users WHERE id=$1 AND (issuer IS NOT NULL OR username IS NOT NULL)`, id).Scan(&out.Archived, &protected)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Protected = protected.Valid
	if out.References, err = purgeReferences(ctx, s.DB, id); err != nil {
		return out, err
	}
	out.Purgeable = out.Archived && !out.Protected && len(out.References) == 0
	return out, nil
}

// Purge permanently removes an ARCHIVED, unprotected, unreferenced (test) person: Keycloak user
// first (disabled + managed only), then — in one transaction that re-checks every condition — the
// remaining session / grant rows and the users row; the username is reserved in purged_usernames
// and the opening record becomes a PURGED tombstone without personal data. Never cascading.
func (s *Service) Purge(ctx context.Context, a Actor, id, confirm string) error {
	if _, err := s.Authorize(ctx, a, CapPersonnelArchive); err != nil {
		return err
	}
	if !a.Host && a.ID == id {
		return ErrSelf
	}
	return s.withPeople(ctx, []string{a.ID, id}, func() error {
		check := func(tx *sql.Tx) (target, error) {
			if _, err := authorize(ctx, tx, a, CapPersonnelArchive); err != nil {
				return target{}, err
			}
			t, err := lockTarget(ctx, tx, id)
			if err != nil {
				return t, err
			}
			if t.protected != "" {
				return t, ErrProtectedIdentity
			}
			if err = confirmMatches(t, confirm); err != nil {
				return t, err
			}
			if !t.archived {
				return t, InvalidError{"state", "只能永久删除已删除（归档）的测试账号；请先删除该人员"}
			}
			refs, err := purgeReferences(ctx, tx, id)
			if err != nil {
				return t, err
			}
			if len(refs) > 0 {
				return t, fmt.Errorf("%w: %v", ErrReferenced, refs)
			}
			return t, nil
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		t, err := check(tx)
		_ = tx.Rollback()
		if err != nil {
			return err
		}
		if t.managed() && t.issuer == s.Issuer {
			if s.IdP == nil {
				return ErrNotConfigured
			}
			if err = s.IdP.DeleteUnbound(ctx, t.subject); err != nil && !errors.Is(err, kcadmin.ErrNotFound) {
				_ = auditlog.Write(ctx, s.DB, a.event("personnel_purged", "identity_provider_failed", id, "account", nil))
				return ErrIdentityProvider
			}
		}
		tx, err = s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if t, err = check(tx); err != nil {
			return err
		}
		reserved := t.username
		if reserved == "" {
			reserved = t.id
		}
		for _, q := range []string{
			`DELETE FROM web_sessions WHERE user_id=$1`,
			`DELETE FROM live_sessions WHERE user_id=$1`,
			`DELETE FROM remote_sessions WHERE user_id=$1`,
			`DELETE FROM user_roles WHERE user_id=$1`,
			`DELETE FROM system_memberships WHERE user_id=$1`,
			`DELETE FROM portfolio_memberships WHERE user_id=$1`,
			`DELETE FROM project_memberships WHERE user_id=$1`,
		} {
			if _, err = tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO purged_usernames(username,user_id,purged_by) VALUES($1,$2,$3) ON CONFLICT (username) DO NOTHING`,
			reserved, id, a.event("", "", "", "", nil).Actor); err != nil {
			return err
		}
		if t.username != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE account_provisions SET state='PURGED', request=jsonb_build_object('username',username,'purged',true), failure='', updated_at=now()
WHERE username=$1 AND state='COMPLETED'`, t.username); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=$1 AND archived_at IS NOT NULL AND protected_reason IS NULL`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrConflict
		}
		if err = auditlog.Write(ctx, tx, a.event("personnel_purged", "success", id, "account", map[string]any{
			"username": t.username, "usernameReserved": true, "businessReferences": 0,
		})); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// usernameReserved reports whether a username was used by a purged person.
func usernameReserved(ctx context.Context, q queryer, username string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM purged_usernames WHERE username=$1`, username).Scan(&n)
	return n > 0, err
}

// ---------------------------------------------------------------------------------------------
// Explicit test-account cleanup list (host tool only)
// ---------------------------------------------------------------------------------------------

// CleanupEntry is one exact account named by the operator.
type CleanupEntry struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// CleanupList is the operator's explicit list. Reason is recorded in every audit row.
type CleanupList struct {
	Reason   string         `json:"reason"`
	Accounts []CleanupEntry `json:"accounts"`
}

// Cleanup actions.
const (
	CleanupArchiveAndPurge = "ARCHIVE_AND_PURGE"
	CleanupArchiveOnly     = "ARCHIVE_ONLY" // business history references the person
	CleanupPurge           = "PURGE"        // already archived, unreferenced
	CleanupKeepArchived    = "KEEP_ARCHIVED"
	CleanupRefused         = "REFUSED"
)

type CleanupItem struct {
	CleanupEntry
	Action     string         `json:"action"`
	Reason     string         `json:"reason,omitempty"`
	References map[string]int `json:"references,omitempty"`
}

type CleanupPlan struct {
	Items  []CleanupItem `json:"items"`
	Digest string        `json:"digest"`
}

// PlanCleanup classifies every listed account (read only). Each entry must name an existing
// person by BOTH exact local id and exact username; anything else is REFUSED.
func (s *Service) PlanCleanup(ctx context.Context, list CleanupList) (CleanupPlan, error) {
	var out CleanupPlan
	if _, err := normalizeReason(list.Reason); err != nil {
		return out, err
	}
	if len(list.Accounts) == 0 || len(list.Accounts) > 200 {
		return out, InvalidError{"accounts", "名单需包含 1–200 个账号"}
	}
	seen := map[string]bool{}
	for _, e := range list.Accounts {
		it := CleanupItem{CleanupEntry: e}
		var username, protected sql.NullString
		var archived, isAdmin bool
		err := s.DB.QueryRowContext(ctx, `SELECT username, protected_reason, archived_at IS NOT NULL,
EXISTS(SELECT 1 FROM system_memberships m WHERE m.user_id=u.id AND m.role='system_admin') FROM users u WHERE id=$1`, e.ID).Scan(&username, &protected, &archived, &isAdmin)
		switch {
		case seen[e.ID]:
			it.Action, it.Reason = CleanupRefused, "duplicate entry"
		case errors.Is(err, sql.ErrNoRows):
			it.Action, it.Reason = CleanupRefused, "no such person"
		case err != nil:
			return out, err
		case !idPattern.MatchString(e.ID) || username.String != e.Username || e.Username == "":
			it.Action, it.Reason = CleanupRefused, "id and username do not identify the same managed person"
		case protected.Valid:
			it.Action, it.Reason = CleanupRefused, "protected identity ("+protected.String+")"
		case isAdmin && !archived:
			it.Action, it.Reason = CleanupRefused, "holds system_admin; remove the role through the reviewed path first"
		default:
			refs, err := purgeReferences(ctx, s.DB, e.ID)
			if err != nil {
				return out, err
			}
			it.References = refs
			switch {
			case archived && len(refs) == 0:
				it.Action = CleanupPurge
			case archived:
				it.Action = CleanupKeepArchived
			case len(refs) == 0:
				it.Action = CleanupArchiveAndPurge
			default:
				it.Action = CleanupArchiveOnly
			}
		}
		seen[e.ID] = true
		out.Items = append(out.Items, it)
	}
	b, _ := json.Marshal(struct {
		Reason string        `json:"reason"`
		Items  []CleanupItem `json:"items"`
	}{strings.TrimSpace(list.Reason), out.Items})
	sum := sha256.Sum256(b)
	out.Digest = hex.EncodeToString(sum[:])
	return out, nil
}

// ApplyCleanup re-plans, requires the approved digest and REFUSED-free plan, then archives and
// (where unreferenced) purges each account through the normal service paths (all guards, locks,
// Keycloak synchronization and audit apply).
func (s *Service) ApplyCleanup(ctx context.Context, a Actor, list CleanupList, approved string) (CleanupPlan, error) {
	if !a.Host {
		return CleanupPlan{}, ErrForbidden
	}
	plan, err := s.PlanCleanup(ctx, list)
	if err != nil {
		return plan, err
	}
	if plan.Digest != approved {
		return plan, fmt.Errorf("%w: cleanup state changed since the approved plan; re-plan and re-approve", ErrConflict)
	}
	for _, it := range plan.Items {
		if it.Action == CleanupRefused {
			return plan, fmt.Errorf("%w: plan contains refused entries; nothing applied", ErrConflict)
		}
	}
	for _, it := range plan.Items {
		if it.Action == CleanupArchiveAndPurge || it.Action == CleanupArchiveOnly {
			if _, err = s.Archive(ctx, a, it.ID, 0, list.Reason, it.Username); err != nil {
				return plan, fmt.Errorf("archive %s: %w", it.ID, err)
			}
		}
		if it.Action == CleanupArchiveAndPurge || it.Action == CleanupPurge {
			if err = s.Purge(ctx, a, it.ID, it.Username); err != nil {
				return plan, fmt.Errorf("purge %s: %w", it.ID, err)
			}
		}
	}
	return plan, nil
}

// SetProtected marks / clears the protection of an identity (host recovery tool only, audited).
func (s *Service) SetProtected(ctx context.Context, a Actor, id, reason string) error {
	if !a.Host {
		return ErrForbidden
	}
	if reason != "" {
		ok := false
		for _, r := range ProtectedReasons {
			ok = ok || r == reason
		}
		if !ok {
			return InvalidError{"reason", "OWNER | SERVICE | SYSTEM"}
		}
	}
	return s.withPeople(ctx, []string{id}, func() error {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		t, err := lockTarget(ctx, tx, id)
		if err != nil {
			return err
		}
		if t.archived {
			return ErrArchived
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET protected_reason=NULLIF($2,''), version=version+1, updated_at=now() WHERE id=$1`, id, reason); err != nil {
			return err
		}
		if err = auditlog.Write(ctx, tx, a.event("identity_protection_changed", "success", id, "system", map[string]any{"from": t.protected, "to": reason})); err != nil {
			return err
		}
		return tx.Commit()
	})
}
