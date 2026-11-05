package main

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	salesRole       = "baobei_sales"
	managerRole     = "baobei_manager"
	directorRole    = "baobei_director"
	adminRole       = "baobei_admin"
	staffPending    = "PENDING_PROVISION"
	staffActivation = "PENDING_ACTIVATION"
	staffActive     = "ACTIVE"
	staffSuspended  = "SUSPENDED"
	staffFailed     = "PROVISION_FAILED"
	staffArchived   = "ARCHIVED"
	allCapabilities = 7
)

var capabilityNames = []string{"project.create", "project.submit", "activity.create"}
var staffStates = []string{staffPending, staffActivation, staffActive, staffSuspended, staffFailed, staffArchived}

type staffProblem struct {
	status        int
	code, message string
}

func (p staffProblem) Error() string { return p.code }

var (
	staffDenied   = staffProblem{403, "STAFF_FORBIDDEN", "没有人员管理权限"}
	staffInvalid  = staffProblem{400, "STAFF_INVALID_INPUT", "请检查人员字段和操作原因"}
	staffMissing  = staffProblem{404, "STAFF_NOT_FOUND", "人员或团队不存在"}
	staffConflict = staffProblem{409, "STAFF_CONFLICT", "记录或账号状态已变化，请刷新后重试"}
	staffReauth   = staffProblem{403, "STAFF_REAUTH_REQUIRED", "请先通过企业账号重新认证"}
)

func staffError(w http.ResponseWriter, err error) bool {
	var p staffProblem
	if errors.As(err, &p) {
		fail(w, p.status, p.code, p.message)
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			fail(w, 409, "STAFF_DUPLICATE", "邮箱、身份或团队已存在")
			return true
		case "BB001":
			fail(w, 409, "STAFF_LAST_ADMIN", "必须保留至少一名有效管理员")
			return true
		}
	}
	return false
}

func capabilityMask(values []string) (int, error) {
	if values == nil {
		return 0, staffInvalid
	}
	mask := 0
	for _, value := range values {
		found := false
		for i, name := range capabilityNames {
			if name == value && mask&(1<<i) == 0 {
				mask |= 1 << i
				found = true
				break
			}
		}
		if !found {
			return 0, staffInvalid
		}
	}
	return mask, nil
}

func capabilityList(mask int) []string {
	result := make([]string, 0, len(capabilityNames))
	for i, name := range capabilityNames {
		if mask&(1<<i) != 0 {
			result = append(result, name)
		}
	}
	return result
}

func normalizedEmail(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(value)
	if err != nil || addr.Address != value || addr.Name != "" || len(value) > 254 ||
		strings.Count(value, "@") != 1 || !strings.Contains(strings.SplitN(value, "@", 2)[1], ".") {
		return "", staffInvalid
	}
	return value, nil
}

func staffRole(role string) bool {
	return role == managerRole || role == directorRole || role == adminRole
}
func staffVisible(actor User, role, team string) bool {
	return actor.Role == adminRole || actor.Role == directorRole ||
		actor.Role == managerRole && role == salesRole && actor.TeamID != "" && actor.TeamID == team
}
func staffWritable(actor User, subject, role, team string) bool {
	return actor.Subject != subject && staffVisible(actor, role, team) && (actor.Role == adminRole || role == salesRole)
}
func recentReauth(at *time.Time) bool {
	return at != nil && time.Since(*at) >= -30*time.Second && time.Since(*at) <= 5*time.Minute
}
func needsApproval(actor User, oldRole, newRole string, at *time.Time) error {
	if oldRole == salesRole && newRole == salesRole {
		return nil
	}
	if actor.Role != adminRole {
		return staffDenied
	}
	if !recentReauth(at) {
		return staffReauth
	}
	return nil
}

type staffMember struct {
	ID               string     `json:"id"`
	Subject          string     `json:"-"`
	Name             string     `json:"name"`
	Email            string     `json:"email"`
	TeamID           string     `json:"teamId"`
	Team             string     `json:"team"`
	Role             string     `json:"role"`
	Status           string     `json:"status"`
	Version          int        `json:"version"`
	Note             string     `json:"note"`
	Permissions      []string   `json:"permissions"`
	Projects         int        `json:"projectCount"`
	LastLogin        *time.Time `json:"lastLogin"`
	SyncStatus       string     `json:"syncStatus"`
	CanManage        bool       `json:"canManage"`
	IdentityVerified bool       `json:"identityVerified"`
	IdentityLinked   bool       `json:"identityLinked"`
	RoleChanged      bool       `json:"roleChanged"`
}
type staffProject struct {
	ID      string `json:"id"`
	Number  string `json:"number"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version int    `json:"version"`
	Queued  bool   `json:"queued"`
}
type staffTeam struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}
type staffEvent struct {
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Reason  string    `json:"reason"`
	Version int       `json:"version"`
	At      time.Time `json:"at"`
}

const staffColumns = `u.staff_id::text,u.subject,u.display_name,coalesce(u.work_email,''),coalesce(u.team_id::text,''),coalesce(t.name,''),
 u.role,u.staff_status,u.staff_version,u.staff_note,u.staff_permissions,
 (SELECT count(*) FROM intake_projects p WHERE p.owner_subject=u.subject
   AND ($1<>'baobei_manager' OR p.team_id::text=$2)),u.last_login_at,
 coalesce((SELECT status FROM staff_outbox o WHERE o.staff_id=u.staff_id ORDER BY o.staff_version DESC,o.created_at DESC LIMIT 1),'NONE'),
 (u.active AND u.staff_managed AND EXISTS(SELECT 1 FROM staff_audit a WHERE a.target_id=u.staff_id AND a.action='staff.activate')),
 EXISTS(SELECT 1 FROM staff_audit a WHERE a.target_id=u.staff_id AND a.action='staff.role-change')`
const staffFrom = ` FROM app_users u LEFT JOIN teams t ON t.id=u.team_id `
const staffScope = ` ($1 IN ('baobei_admin','baobei_director') OR ($1='baobei_manager' AND u.role='baobei_sales' AND u.team_id::text=$2)) `
const openProjects = `intake_status NOT IN ('REJECTED','CANCELLED','CONVERTED') AND sales_stage NOT IN ('WON','LOST')`

func scanStaff(row interface{ Scan(...any) error }) (staffMember, error) {
	var m staffMember
	var mask int
	err := row.Scan(&m.ID, &m.Subject, &m.Name, &m.Email, &m.TeamID, &m.Team, &m.Role, &m.Status, &m.Version, &m.Note, &mask, &m.Projects, &m.LastLogin, &m.SyncStatus, &m.IdentityVerified, &m.RoleChanged)
	m.Permissions = capabilityList(mask)
	email, emailErr := normalizedEmail(m.Email)
	m.IdentityLinked = validIdentityUUID(m.Subject)
	m.IdentityVerified = m.IdentityVerified && m.IdentityLinked && emailErr == nil && email == m.Email
	return m, err
}

func readStaff(ctx context.Context, tx pgx.Tx, actor User, id string) (staffMember, error) {
	if !uuidPattern.MatchString(id) {
		return staffMember{}, staffInvalid
	}
	m, err := scanStaff(tx.QueryRow(ctx, `SELECT `+staffColumns+staffFrom+` WHERE `+staffScope+` AND u.staff_id=$3`, actor.Role, actor.TeamID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, staffMissing
	}
	m.CanManage = staffWritable(actor, m.Subject, m.Role, m.TeamID) && m.Status != staffArchived
	return m, err
}

func listStaff(ctx context.Context, tx pgx.Tx, actor User, r *http.Request) (any, error) {
	q := r.URL.Query()
	search, status, order := strings.TrimSpace(q.Get("q")), q.Get("status"), q.Get("sort")
	if len(search) > 254 || status != "" && !contains(staffStates, status) {
		return nil, staffInvalid
	}
	ordering := map[string]string{"": "u.display_name", "name": "u.display_name", "email": "u.work_email NULLS LAST", "lastLogin": "u.last_login_at DESC NULLS LAST", "projects": "12 DESC"}[order]
	if ordering == "" {
		return nil, staffInvalid
	}
	page := 1
	if value := q.Get("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 10000 {
			return nil, staffInvalid
		}
		page = parsed
	}
	const pageSize = 20
	where := ` WHERE ` + staffScope + ` AND ($3='' OR position(lower($3) in lower(u.display_name))>0 OR position(lower($3) in coalesce(u.work_email,''))>0)
  AND ($4='' OR u.staff_status=$4)`
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*)`+staffFrom+where, actor.Role, actor.TeamID, search, status).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+staffColumns+staffFrom+where+` ORDER BY `+ordering+`,u.staff_id LIMIT $5 OFFSET $6`, actor.Role, actor.TeamID, search, status, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]staffMember, 0)
	for rows.Next() {
		m, err := scanStaff(rows)
		if err != nil {
			return nil, err
		}
		m.CanManage = staffWritable(actor, m.Subject, m.Role, m.TeamID) && m.Status != staffArchived
		m.Note = ""
		items = append(items, m)
	}
	return map[string]any{"items": items, "total": total, "page": page, "pageSize": pageSize}, rows.Err()
}

func staffProjects(ctx context.Context, tx pgx.Tx, actor User, m staffMember) ([]staffProject, error) {
	items := make([]staffProject, 0)
	if actor.Role == adminRole {
		return items, nil
	}
	rows, err := tx.Query(ctx, `SELECT p.id::text,p.registration_no,p.project_name,p.intake_status,p.version,
  EXISTS(SELECT 1 FROM staff_handoffs h WHERE h.staff_id=$1 AND h.project_id=p.id)
  FROM intake_projects p WHERE p.owner_subject=$2 AND ($3='baobei_director' OR p.team_id::text=$4)
  AND `+openProjects+` ORDER BY p.updated_at DESC,p.id LIMIT 100`, m.ID, m.Subject, actor.Role, actor.TeamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p staffProject
		if err = rows.Scan(&p.ID, &p.Number, &p.Name, &p.Status, &p.Version, &p.Queued); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func staffAudit(ctx context.Context, tx pgx.Tx, target, actor, action, reason string, version int) error {
	_, err := tx.Exec(ctx, `INSERT INTO staff_audit(target_id,actor,action,reason,version) VALUES($1,$2,$3,$4,$5)`, target, actor, action, reason, version)
	return err
}
func listStaffAudit(ctx context.Context, tx pgx.Tx, target string) (any, error) {
	rows, err := tx.Query(ctx, `SELECT coalesce(u.display_name,a.actor),a.action,a.reason,a.version,a.at
  FROM staff_audit a LEFT JOIN app_users u ON u.subject=a.actor WHERE a.target_id=$1 ORDER BY a.at DESC,a.id DESC LIMIT 50`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]staffEvent, 0)
	for rows.Next() {
		var event staffEvent
		if err = rows.Scan(&event.Actor, &event.Action, &event.Reason, &event.Version, &event.At); err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	return map[string]any{"items": items}, rows.Err()
}

func staffEnqueue(ctx context.Context, tx pgx.Tx, target string, version int, operation string) error {
	if _, err := tx.Exec(ctx, `UPDATE staff_outbox SET status='SUPERSEDED',processed_at=now() WHERE staff_id=$1 AND status='PENDING'`, target); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO staff_outbox(staff_id,staff_version,operation) VALUES($1,$2,$3)`, target, version, operation)
	return err
}

type staffProfileInput struct {
	Name           string   `json:"name"`
	Email          string   `json:"email"`
	TeamID         string   `json:"teamId"`
	Role           string   `json:"role"`
	Note           string   `json:"note"`
	Reason         string   `json:"reason"`
	Version        int      `json:"version"`
	Permissions    []string `json:"permissions,omitempty"`
	VerifyIdentity bool     `json:"verifyIdentity,omitempty"`
}
type staffMutation struct {
	Version     int      `json:"version"`
	Reason      string   `json:"reason"`
	Permissions []string `json:"permissions,omitempty"`
}

func reasonOK(reason string) bool { return strings.TrimSpace(reason) != "" && len(reason) <= 600 }

func validateProfile(ctx context.Context, tx pgx.Tx, actor User, in *staffProfileInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if !identityText(in.Name) || !reasonOK(in.Reason) || len(in.Note) > 1000 ||
		!contains([]string{salesRole, managerRole, directorRole, adminRole}, in.Role) {
		return staffInvalid
	}
	email, err := normalizedEmail(in.Email)
	if err != nil {
		return err
	}
	in.Email = email
	if actor.Role != adminRole && in.Role != salesRole {
		return staffDenied
	}
	if actor.Role == managerRole && in.TeamID != actor.TeamID {
		return staffDenied
	}
	if in.Role == salesRole || in.Role == managerRole {
		if !uuidPattern.MatchString(in.TeamID) {
			return staffInvalid
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1)`, in.TeamID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return staffMissing
		}
	} else if in.TeamID != "" {
		return staffInvalid
	}
	return nil
}

func inviteStaff(ctx context.Context, tx pgx.Tx, actor User, at *time.Time, in staffProfileInput) (any, error) {
	if in.VerifyIdentity {
		return nil, staffInvalid
	}
	if err := validateProfile(ctx, tx, actor, &in); err != nil {
		return nil, err
	}
	if err := needsApproval(actor, salesRole, in.Role, at); err != nil {
		return nil, err
	}
	mask := allCapabilities
	if in.Permissions != nil {
		var err error
		mask, err = capabilityMask(in.Permissions)
		if err != nil || in.Role != salesRole && mask != allCapabilities {
			return nil, staffInvalid
		}
	}
	var id string
	err := tx.QueryRow(ctx, `WITH id AS (SELECT gen_random_uuid() AS v)
 INSERT INTO app_users(staff_id,subject,display_name,work_email,role,team_id,active,staff_status,staff_note,staff_permissions,staff_managed)
 SELECT v,'pending:'||v::text,$1,$2,$3,NULLIF($4,'')::uuid,false,'PENDING_PROVISION',$5,$6,true FROM id RETURNING staff_id::text`,
		in.Name, in.Email, in.Role, in.TeamID, in.Note, mask).Scan(&id)
	if err != nil {
		return nil, err
	}
	if err = staffEnqueue(ctx, tx, id, 1, "PROVISION"); err != nil {
		return nil, err
	}
	if err = staffAudit(ctx, tx, id, actor.Subject, "staff.invite", in.Reason, 1); err != nil {
		return nil, err
	}
	return readStaff(ctx, tx, actor, id)
}

func editStaff(ctx context.Context, tx pgx.Tx, actor User, at *time.Time, m staffMember, in staffProfileInput) (any, error) {
	if in.Permissions != nil {
		return nil, staffInvalid
	}
	if in.Version != m.Version {
		return nil, staffConflict
	}
	if err := validateProfile(ctx, tx, actor, &in); err != nil {
		return nil, err
	}
	if err := needsApproval(actor, m.Role, in.Role, at); err != nil {
		return nil, err
	}
	roleChanged := m.Role != in.Role
	if roleChanged && (!m.IdentityVerified || in.Email != m.Email) {
		return nil, staffProblem{409, "STAFF_IDENTITY_NOT_VERIFIED", "请先完成企业身份核对和登录激活，再变更角色"}
	}
	roleOrTeam := roleChanged || m.TeamID != in.TeamID
	if in.VerifyIdentity && (actor.Role != adminRole || roleOrTeam || m.Status != staffActive || m.IdentityVerified || !m.IdentityLinked) {
		return nil, staffDenied
	}
	if roleOrTeam {
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM intake_projects WHERE owner_subject=$1 AND `+openProjects+`)`, m.Subject).Scan(&open); err != nil {
			return nil, err
		}
		if open {
			return nil, staffProblem{409, "STAFF_HANDOFF_REQUIRED", "请先移交未结束项目"}
		}
	}
	next := m.Status
	newProvision := roleOrTeam || in.Email != m.Email || in.VerifyIdentity
	if newProvision && next != staffSuspended {
		next = staffPending
	}
	version := m.Version + 1
	_, err := tx.Exec(ctx, `UPDATE app_users SET display_name=$2,work_email=$3,role=$4,team_id=NULLIF($5,'')::uuid,
 staff_note=$6,staff_version=$7,staff_status=$8,active=($8='ACTIVE'),staff_managed=true
 WHERE staff_id=$1`, m.ID, in.Name, in.Email, in.Role, in.TeamID, in.Note, version, next)
	if err != nil {
		return nil, err
	}
	if newProvision {
		if _, err = tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, m.Subject); err != nil {
			return nil, err
		}
		if next == staffPending {
			if err = staffEnqueue(ctx, tx, m.ID, version, "PROVISION"); err != nil {
				return nil, err
			}
		}
	}
	action, reason := "staff.edit", in.Reason
	if roleChanged {
		action, reason = "staff.role-change", m.Role+" → "+in.Role+" · "+in.Reason
	}
	if err = staffAudit(ctx, tx, m.ID, actor.Subject, action, reason, version); err != nil {
		return nil, err
	}
	return readStaff(ctx, tx, actor, m.ID)
}

func mutateStaff(ctx context.Context, tx pgx.Tx, actor User, at *time.Time, m staffMember, action string, in staffMutation) (any, error) {
	if !reasonOK(in.Reason) || in.Version < 1 {
		return nil, staffInvalid
	}
	if in.Version != m.Version {
		return nil, staffConflict
	}
	if err := needsApproval(actor, m.Role, m.Role, at); err != nil {
		return nil, err
	}
	next, operation := m.Status, ""
	mask, err := capabilityMask(m.Permissions)
	if err != nil {
		return nil, err
	}
	if action != "permissions" && in.Permissions != nil {
		return nil, staffInvalid
	}
	switch action {
	case "permissions":
		if m.Role != salesRole {
			return nil, staffDenied
		}
		mask, err = capabilityMask(in.Permissions)
		if err != nil {
			return nil, err
		}
	case "suspend":
		if m.Status == staffSuspended {
			return nil, staffConflict
		}
		next, operation = staffSuspended, "SUSPEND"
	case "reactivate":
		if m.Status != staffSuspended || m.Email == "" {
			return nil, staffConflict
		}
		next, operation = staffPending, "PROVISION"
	case "retry":
		switch {
		case m.Status == staffFailed:
			next, operation = staffPending, "PROVISION"
		case m.Status == staffSuspended && m.SyncStatus == "FAILED":
			operation = "SUSPEND"
		default:
			return nil, staffConflict
		}
	case "archive":
		if actor.Role != adminRole || m.Status != staffSuspended {
			return nil, staffDenied
		}
		var open bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM intake_projects WHERE owner_subject=$1 AND `+openProjects+`)`, m.Subject).Scan(&open); err != nil {
			return nil, err
		}
		if open {
			return nil, staffConflict
		}
		next = staffArchived
	default:
		return nil, staffMissing
	}
	version := m.Version + 1
	if _, err = tx.Exec(ctx, `UPDATE app_users SET staff_version=$2,staff_status=$3,active=($3='ACTIVE'),
  staff_permissions=$4,staff_managed=true WHERE staff_id=$1`, m.ID, version, next, mask); err != nil {
		return nil, err
	}
	if next != staffActive {
		if _, err = tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, m.Subject); err != nil {
			return nil, err
		}
	}
	if action == "suspend" {
		if _, err = tx.Exec(ctx, `INSERT INTO staff_handoffs(staff_id,project_id) SELECT $1,id
   FROM intake_projects WHERE owner_subject=$2 AND `+openProjects+` ON CONFLICT DO NOTHING`, m.ID, m.Subject); err != nil {
			return nil, err
		}
	}
	if operation != "" {
		if err = staffEnqueue(ctx, tx, m.ID, version, operation); err != nil {
			return nil, err
		}
	}
	if err = staffAudit(ctx, tx, m.ID, actor.Subject, "staff."+action, in.Reason, version); err != nil {
		return nil, err
	}
	return readStaff(ctx, tx, actor, m.ID)
}

type staffTransferInput struct {
	TargetID string `json:"targetId"`
	Version  int    `json:"version"`
	Reason   string `json:"reason"`
	Projects []struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	} `json:"projects"`
}
type transferResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int    `json:"version,omitempty"`
}

func transferStaffProjects(ctx context.Context, tx pgx.Tx, actor User, from staffMember, in staffTransferInput) (any, error) {
	if !manager(actor) || from.Role != salesRole {
		return nil, staffDenied
	}
	if from.Version != in.Version {
		return nil, staffConflict
	}
	if !reasonOK(in.Reason) || len(in.Projects) == 0 || len(in.Projects) > 100 || in.TargetID == from.ID {
		return nil, staffInvalid
	}
	target, err := readStaff(ctx, tx, actor, in.TargetID)
	if err != nil {
		return nil, err
	}
	if target.Status != staffActive || target.Role != salesRole || target.TeamID == "" {
		return nil, staffConflict
	}
	seen := map[string]bool{}
	results := make([]transferResult, 0, len(in.Projects))
	for _, item := range in.Projects {
		if !uuidPattern.MatchString(item.ID) || item.Version < 1 || seen[item.ID] {
			return nil, staffInvalid
		}
		seen[item.ID] = true
		result := transferResult{ID: item.ID, Status: "NOT_AVAILABLE"}
		var owner, team string
		var version int
		err = tx.QueryRow(ctx, `SELECT owner_subject,coalesce(team_id::text,''),version FROM intake_projects WHERE id=$1
   AND `+openProjects+` AND ($2='baobei_director' OR team_id::text=$3) FOR UPDATE`, item.ID, actor.Role, actor.TeamID).Scan(&owner, &team, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			results = append(results, result)
			continue
		}
		if err != nil {
			return nil, err
		}
		if owner == target.Subject {
			var prior bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ownership_history WHERE project_id=$1 AND from_subject=$2 AND to_subject=$3)`, item.ID, from.Subject, target.Subject).Scan(&prior); err != nil {
				return nil, err
			}
			if prior {
				result.Status = "ALREADY_TRANSFERRED"
				result.Version = version
			}
		} else if owner == from.Subject {
			if version != item.Version {
				result.Status = "VERSION_CONFLICT"
				result.Version = version
			} else {
				version++
				if _, err = tx.Exec(ctx, `UPDATE intake_projects SET owner_subject=$2,team_id=$3,version=$4,updated_at=now() WHERE id=$1`, item.ID, target.Subject, target.TeamID, version); err != nil {
					return nil, err
				}
				if _, err = tx.Exec(ctx, `INSERT INTO ownership_history(project_id,from_subject,to_subject,reason,actor_subject) VALUES($1,$2,$3,$4,$5)`, item.ID, from.Subject, target.Subject, in.Reason, actor.Subject); err != nil {
					return nil, err
				}
				if _, err = tx.Exec(ctx, `INSERT INTO audit_events(project_id,subject,action,revision,details) VALUES($1,$2,'ownership.transfer',$3,$4::jsonb)`, item.ID, actor.Subject, version, string(mustJSON(map[string]string{"fromOwner": from.Subject, "toOwner": target.Subject, "reason": in.Reason}))); err != nil {
					return nil, err
				}
				if err = staffAudit(ctx, tx, from.ID, actor.Subject, "staff.transfer", in.Reason, from.Version); err != nil {
					return nil, err
				}
				result.Status = "TRANSFERRED"
				result.Version = version
			}
		}
		results = append(results, result)
	}
	return map[string]any{"items": results}, nil
}

func staffTeams(ctx context.Context, tx pgx.Tx, actor User, w http.ResponseWriter, r *http.Request, id string) (any, error) {
	if r.Method == "GET" && id == "" {
		rows, err := tx.Query(ctx, `SELECT id::text,name,staff_version FROM teams WHERE $1<>'baobei_manager' OR id::text=$2 ORDER BY name,id`, actor.Role, actor.TeamID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := make([]staffTeam, 0)
		for rows.Next() {
			var t staffTeam
			if err = rows.Scan(&t.ID, &t.Name, &t.Version); err != nil {
				return nil, err
			}
			items = append(items, t)
		}
		return map[string]any{"items": items}, rows.Err()
	}
	if actor.Role != adminRole {
		return nil, staffDenied
	}
	if !(r.Method == "POST" && id == "" || r.Method == "PATCH" && uuidPattern.MatchString(id)) {
		return nil, staffInvalid
	}
	var in struct {
		Name    string `json:"name"`
		Reason  string `json:"reason"`
		Version int    `json:"version"`
	}
	if !decode(w, r, &in) {
		return nil, nil
	}
	in.Name = strings.TrimSpace(in.Name)
	if !identityText(in.Name) || !reasonOK(in.Reason) {
		return nil, staffInvalid
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE lower(name)=lower($1) AND id::text<>$2)`, in.Name, id).Scan(&duplicate); err != nil {
		return nil, err
	}
	if duplicate {
		return nil, staffConflict
	}
	var t staffTeam
	var err error
	action := "team.create"
	if id == "" {
		err = tx.QueryRow(ctx, `INSERT INTO teams(name) VALUES($1) RETURNING id::text,name,staff_version`, in.Name).Scan(&t.ID, &t.Name, &t.Version)
	} else {
		action = "team.edit"
		err = tx.QueryRow(ctx, `UPDATE teams SET name=$2,staff_version=staff_version+1 WHERE id=$1 AND staff_version=$3 RETURNING id::text,name,staff_version`, id, in.Name, in.Version).Scan(&t.ID, &t.Name, &t.Version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, staffConflict
	}
	if err != nil {
		return nil, err
	}
	if err = staffAudit(ctx, tx, t.ID, actor.Subject, action, in.Reason, t.Version); err != nil {
		return nil, err
	}
	return t, nil
}

func (a *App) staffPool() *pgxpool.Pool {
	if a.staffDB != nil {
		return a.staffDB
	}
	// Synthetic and legacy unit fixtures run against their own disposable database.
	if a.cfg.Env != "production" {
		return a.db
	}
	return nil
}

func (a *App) staff(w http.ResponseWriter, r *http.Request, actor User) error {
	if !staffRole(actor.Role) {
		return staffDenied
	}
	pool := a.staffPool()
	if pool == nil {
		return errors.New("staff database unavailable")
	}
	ctx := r.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	lock := "pg_advisory_xact_lock"
	if r.Method == "GET" {
		lock = "pg_advisory_xact_lock_shared"
	}
	if _, err = tx.Exec(ctx, `SELECT `+lock+`($1)`, identityLock); err != nil {
		return err
	}
	var role, team string
	var at *time.Time
	err = tx.QueryRow(ctx, `SELECT u.role,coalesce(u.team_id::text,''),g.reauthenticated_at FROM app_users u
  JOIN app_sessions s ON s.subject=u.subject JOIN staff_sessions g ON g.token_hash=s.token_hash AND g.subject=u.subject WHERE s.token_hash=$1 AND u.subject=$2 AND s.expires_at>now() AND u.active`, actor.sessionHash, actor.Subject).Scan(&role, &team, &at)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (role != actor.Role || team != actor.TeamID) {
		return staffDenied
	}
	if err != nil {
		return err
	}
	result, err := staffRoute(ctx, tx, w, r, actor, at)
	if err != nil || result == nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	respond(w, 200, result)
	return nil
}

func staffRoute(ctx context.Context, tx pgx.Tx, w http.ResponseWriter, r *http.Request, actor User, at *time.Time) (any, error) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
	if parts[0] == "teams" {
		if len(parts) == 1 {
			return staffTeams(ctx, tx, actor, w, r, "")
		}
		if len(parts) == 2 && parts[1] != "" {
			return staffTeams(ctx, tx, actor, w, r, parts[1])
		}
		return nil, staffMissing
	}
	if len(parts) == 1 && r.Method == "GET" {
		return listStaff(ctx, tx, actor, r)
	}
	if len(parts) == 2 && parts[1] == "invitations" && r.Method == "POST" {
		var in staffProfileInput
		if !decode(w, r, &in) {
			return nil, nil
		}
		return inviteStaff(ctx, tx, actor, at, in)
	}
	if len(parts) < 2 || len(parts) > 3 {
		return nil, staffMissing
	}
	m, err := readStaff(ctx, tx, actor, parts[1])
	if err != nil {
		return nil, err
	}
	if r.Method == "GET" {
		if len(parts) == 3 && parts[2] == "audit" {
			return listStaffAudit(ctx, tx, m.ID)
		}
		if len(parts) != 2 {
			return nil, staffMissing
		}
		projects, err := staffProjects(ctx, tx, actor, m)
		return map[string]any{"member": m, "projects": projects, "recentReauth": actor.Role == adminRole && recentReauth(at)}, err
	}
	if !staffWritable(actor, m.Subject, m.Role, m.TeamID) || m.Status == staffArchived {
		return nil, staffDenied
	}
	if len(parts) == 2 && r.Method == "PATCH" {
		var in staffProfileInput
		if !decode(w, r, &in) {
			return nil, nil
		}
		return editStaff(ctx, tx, actor, at, m, in)
	}
	if len(parts) != 3 {
		return nil, staffMissing
	}
	action := parts[2]
	if action == "ownership-transfer-batch" && r.Method == "POST" {
		var in staffTransferInput
		if !decode(w, r, &in) {
			return nil, nil
		}
		return transferStaffProjects(ctx, tx, actor, m, in)
	}
	if action == "permissions" && r.Method == "PATCH" || r.Method == "POST" && contains([]string{"suspend", "reactivate", "archive", "retry"}, action) {
		var in staffMutation
		if !decode(w, r, &in) {
			return nil, nil
		}
		return mutateStaff(ctx, tx, actor, at, m, action, in)
	}
	return nil, staffMissing
}

// Acquire the identity lock before any project lock. Suspension and capability
// changes can commit only before this recheck or after the entire business write.
func (a *App) beginBusiness(ctx context.Context, user User, required int) (pgx.Tx, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, identityLock); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
   SELECT 1 FROM app_sessions s JOIN app_users u ON u.subject=s.subject
   WHERE s.token_hash=$1 AND s.expires_at>now() AND u.active AND u.subject=$2 AND u.role=$3
   AND coalesce(u.team_id::text,'')=$4 AND (u.staff_permissions::integer & $5)=$5
 )`, user.sessionHash, user.Subject, user.Role, user.TeamID, required).Scan(&allowed)
	if err != nil || !allowed {
		_ = tx.Rollback(ctx)
		if err != nil {
			return nil, err
		}
		return nil, staffProblem{403, "BUSINESS_PERMISSION_DENIED", "账号状态或业务权限已变化"}
	}
	return tx, nil
}
