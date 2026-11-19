package personnel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
)

// IdentityProvider is the Keycloak surface the service needs (kcadmin.Client in production; tests
// inject a faithful fake to prove the non-atomic recovery paths).
type IdentityProvider interface {
	FindByUsername(ctx context.Context, username string) (*kcadmin.User, error)
	Get(ctx context.Context, id string) (*kcadmin.User, error)
	CreateDisabled(ctx context.Context, p kcadmin.Profile) (string, error)
	UpdateProfile(ctx context.Context, id string, p kcadmin.Profile) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	SetTemporaryPassword(ctx context.Context, id string, password []byte) error
	Logout(ctx context.Context, id string) error
	DeleteUnbound(ctx context.Context, id string) error
	Adopt(ctx context.Context, id, localUserID string) (*kcadmin.User, error)
}

// Service is the personnel / account service. IdP may be nil: personnel data stays readable and
// account operations answer ErrNotConfigured.
type Service struct {
	DB     *sql.DB
	IdP    IdentityProvider
	Issuer string
	// RevokeLive ends the user's live-view leases (session manager); nil in tests without runtime.
	RevokeLive func(ctx context.Context, user string) error
	// Fault (tests only) injects a failure at a named step of a non-atomic operation.
	Fault func(step string) error
}

func (s *Service) fault(step string) error {
	if s.Fault != nil {
		return s.Fault(step)
	}
	return nil
}

// Actor is who performs an operation. Host actors (the controlled host recovery tool) bypass the
// system-capability check but are still audited and still subject to the last-admin protection.
type Actor struct {
	ID    string
	Host  bool
	Event auditlog.Event // source IP / request id template
}

func (a Actor) event(action, result, target, scope string, change map[string]any) auditlog.Event {
	e := a.Event
	e.Actor, e.Action, e.Result, e.TargetUser, e.Scope, e.Change = a.ID, action, result, target, scope, change
	if a.Host && !strings.HasPrefix(e.Actor, "host:") {
		e.Actor = "host:" + e.Actor
	}
	return e
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// RolesOf returns the system roles of an ENABLED user (disabled users have none in effect).
func RolesOf(ctx context.Context, q queryer, user string) ([]SystemRole, error) {
	rows, err := q.QueryContext(ctx, `SELECT m.role FROM system_memberships m JOIN users u ON u.id=m.user_id
WHERE m.user_id=$1 AND u.enabled ORDER BY m.role`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SystemRole{}
	for rows.Next() {
		var r string
		if err = rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, SystemRole(r))
	}
	return out, rows.Err()
}

// authorize re-reads the actor's roles inside tx (FOR SHARE on the actor row serializes against a
// concurrent disable/role removal of the actor) and checks one capability.
func authorize(ctx context.Context, tx *sql.Tx, a Actor, cap SystemCapability) ([]SystemRole, error) {
	if a.Host {
		return []SystemRole{RoleSystemAdmin}, nil
	}
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT enabled FROM users WHERE id=$1 FOR SHARE`, a.ID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !enabled) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	roles, err := RolesOf(ctx, tx, a.ID)
	if err != nil {
		return nil, err
	}
	if !Has(roles, cap) {
		return nil, ErrForbidden
	}
	return roles, nil
}

// Authorize checks a capability outside a transaction (read paths).
func (s *Service) Authorize(ctx context.Context, a Actor, cap SystemCapability) ([]SystemRole, error) {
	if a.Host {
		return []SystemRole{RoleSystemAdmin}, nil
	}
	roles, err := RolesOf(ctx, s.DB, a.ID)
	if err != nil {
		return nil, err
	}
	if !Has(roles, cap) {
		return roles, ErrForbidden
	}
	return roles, nil
}

// ---------------------------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------------------------

type GrantRef struct {
	ScopeID   string `json:"scopeId"`
	ScopeName string `json:"scopeName"`
	Role      string `json:"role"`
}

type Person struct {
	ID              string     `json:"id"`
	Username        string     `json:"username"`
	DisplayName     string     `json:"displayName"`
	DepartmentID    string     `json:"departmentId"`
	DepartmentName  string     `json:"departmentName"`
	JobTitle        string     `json:"jobTitle"`
	Phone           string     `json:"phone"`
	Email           string     `json:"email"`
	Enabled         bool       `json:"enabled"`
	ArchivedAt      *time.Time `json:"archivedAt"`
	ArchivedBy      string     `json:"archivedBy"`
	ArchiveReason   string     `json:"archiveReason"`
	Protected       string     `json:"protected"` // "" | OWNER | SERVICE | SYSTEM
	Managed         bool       `json:"managed"`   // has a Crane-managed login account (username set)
	IdentitySync    string     `json:"identitySync"`
	CreatedAt       time.Time  `json:"createdAt"`
	CreatedBy       string     `json:"createdBy"`
	LastLoginAt     *time.Time `json:"lastLoginAt"`
	Version         int64      `json:"version"`
	SystemRoles     []string   `json:"systemRoles"`
	ProjectCount    int        `json:"projectCount"`
	PortfolioCount  int        `json:"portfolioCount"`
	PortfolioGrants []GrantRef `json:"portfolioGrants,omitempty"`
	ProjectGrants   []GrantRef `json:"projectGrants,omitempty"`
}

type HistoryEntry struct {
	At        time.Time       `json:"at"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Result    string          `json:"result"`
	Scope     string          `json:"scope"`
	Change    json.RawMessage `json:"change"`
	RequestID string          `json:"requestId"`
	SourceIP  string          `json:"sourceIp"`
}

type IdentityStatus struct {
	Available              bool `json:"available"`
	Exists                 bool `json:"exists"`
	Enabled                bool `json:"enabled"`
	PasswordChangeRequired bool `json:"passwordChangeRequired"`
}

type PersonDetail struct {
	Person
	Identity IdentityStatus `json:"identity"`
	History  []HistoryEntry `json:"history"`
}

type Department struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Version     int64  `json:"version"`
	MemberCount int    `json:"memberCount"`
}

// ---------------------------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------------------------

var (
	usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)
	phonePattern    = regexp.MustCompile(`^[0-9+() -]{3,32}$`)
	emailPattern    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	idPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
)

// Reserved usernames are never provisioned (service identities, administrators of Keycloak).
var reservedUsernames = map[string]bool{"admin": true, "root": true, "bootstrap-crane": true, "system": true, "anonymous": true}

// Profile is the editable person information.
type Profile struct {
	DisplayName  string `json:"displayName"`
	DepartmentID string `json:"departmentId"`
	JobTitle     string `json:"jobTitle"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
}

func (p *Profile) normalize() error {
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	p.DepartmentID = strings.TrimSpace(p.DepartmentID)
	p.JobTitle = strings.TrimSpace(p.JobTitle)
	p.Phone = strings.TrimSpace(p.Phone)
	p.Email = strings.TrimSpace(p.Email)
	switch n := utf8.RuneCountInString(p.DisplayName); {
	case n == 0:
		return InvalidError{"displayName", "请填写姓名"}
	case n > 80:
		return InvalidError{"displayName", "姓名不能超过 80 个字符"}
	}
	if p.DepartmentID != "" && !idPattern.MatchString(p.DepartmentID) {
		return InvalidError{"departmentId", "部门不存在"}
	}
	if utf8.RuneCountInString(p.JobTitle) > 80 {
		return InvalidError{"jobTitle", "岗位不能超过 80 个字符"}
	}
	if p.Phone != "" && !phonePattern.MatchString(p.Phone) {
		return InvalidError{"phone", "电话格式不正确"}
	}
	if p.Email != "" && (len(p.Email) > 254 || !emailPattern.MatchString(p.Email)) {
		return InvalidError{"email", "邮箱格式不正确"}
	}
	return nil
}

func validUsername(u string) error {
	if !usernamePattern.MatchString(u) {
		return InvalidError{"username", "用户名需为 2–63 位小写字母、数字、- 或 _，并以字母或数字开头"}
	}
	if reservedUsernames[u] || strings.HasPrefix(u, "service-account-") {
		return InvalidError{"username", "该用户名为系统保留名称"}
	}
	return nil
}

func checkDepartment(ctx context.Context, q queryer, id string) error {
	if id == "" {
		return nil
	}
	var status string
	err := q.QueryRowContext(ctx, `SELECT status FROM departments WHERE id=$1`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status != "ACTIVE") {
		return InvalidError{"departmentId", "部门不存在或已停用"}
	}
	return err
}

// ---------------------------------------------------------------------------------------------
// Temporary passwords
// ---------------------------------------------------------------------------------------------

const passwordAlphabetLower = "abcdefghijkmnpqrstuvwxyz"
const passwordAlphabetUpper = "ABCDEFGHJKLMNPQRSTUVWXYZ"
const passwordAlphabetDigit = "23456789"

// GeneratePassword returns a 16-character one-time password (upper, lower and digit guaranteed,
// no ambiguous characters). It is returned to the administrator exactly once and never stored.
func GeneratePassword() ([]byte, error) {
	all := passwordAlphabetLower + passwordAlphabetUpper + passwordAlphabetDigit
	pick := func(set string) (byte, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			return 0, err
		}
		return set[n.Int64()], nil
	}
	out := make([]byte, 16)
	for i := range out {
		set := all
		switch i {
		case 0:
			set = passwordAlphabetUpper
		case 1:
			set = passwordAlphabetLower
		case 2:
			set = passwordAlphabetDigit
		}
		c, err := pick(set)
		if err != nil {
			return nil, err
		}
		out[i] = c
	}
	// Shuffle so the guaranteed classes are not at fixed positions.
	for i := len(out) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, err
		}
		j := n.Int64()
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------------------------

type ListFilter struct {
	Query, DepartmentID, Status, SystemRole string
	Limit, Offset                           int
}

type ListResult struct {
	Items []Person `json:"items"`
	Total int      `json:"total"`
}

const personColumns = `u.id, COALESCE(u.username,''), u.display_name, COALESCE(u.department_id,''), COALESCE(d.name,''), u.job_title,
COALESCE(u.phone,''), COALESCE(u.email,''), u.enabled, u.username IS NOT NULL, u.idp_state, u.created_at, u.created_by, u.last_login_at, u.version,
COALESCE((SELECT array_to_json(array_agg(s.role ORDER BY s.role)) FROM system_memberships s WHERE s.user_id=u.id),'[]'::json),
(SELECT count(*) FROM project_memberships m WHERE m.user_id=u.id),
(SELECT count(*) FROM portfolio_memberships m WHERE m.user_id=u.id),
u.archived_at, COALESCE(u.archived_by,''), COALESCE(u.archive_reason,''), COALESCE(u.protected_reason,'')`

func scanPerson(row interface{ Scan(...any) error }) (Person, error) {
	var p Person
	var roles []byte
	var last, archived sql.NullTime
	err := row.Scan(&p.ID, &p.Username, &p.DisplayName, &p.DepartmentID, &p.DepartmentName, &p.JobTitle, &p.Phone, &p.Email,
		&p.Enabled, &p.Managed, &p.IdentitySync, &p.CreatedAt, &p.CreatedBy, &last, &p.Version, &roles, &p.ProjectCount, &p.PortfolioCount,
		&archived, &p.ArchivedBy, &p.ArchiveReason, &p.Protected)
	if err != nil {
		return p, err
	}
	if archived.Valid {
		t := archived.Time
		p.ArchivedAt = &t
	}
	if last.Valid {
		t := last.Time
		p.LastLoginAt = &t
	}
	p.SystemRoles = []string{}
	if err = json.Unmarshal(roles, &p.SystemRoles); err != nil {
		return p, err
	}
	return p, nil
}

// List returns people with an OIDC identity or a managed account (legacy development identities
// without either are not personnel). Requires personnel_read.
func (s *Service) List(ctx context.Context, a Actor, f ListFilter) (ListResult, error) {
	if _, err := s.Authorize(ctx, a, CapPersonnelRead); err != nil {
		return ListResult{}, err
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where := []string{`(u.issuer IS NOT NULL OR u.username IS NOT NULL)`}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		if utf8.RuneCountInString(q) > 80 {
			return ListResult{}, InvalidError{"q", "搜索词过长"}
		}
		add(`(u.display_name ILIKE ? OR u.username ILIKE ? OR u.id ILIKE ? OR u.job_title ILIKE ?)`, "%"+escapeLike(q)+"%")
	}
	if f.DepartmentID != "" {
		add(`u.department_id = ?`, f.DepartmentID)
	}
	// R9: archived (deleted) people are hidden unless explicitly requested.
	switch f.Status {
	case "":
		where = append(where, `u.archived_at IS NULL`)
	case "ALL":
	case "ENABLED":
		where = append(where, `u.enabled`)
	case "DISABLED":
		where = append(where, `NOT u.enabled AND u.archived_at IS NULL`)
	case "UNMANAGED":
		where = append(where, `u.username IS NULL AND u.archived_at IS NULL`)
	case "ARCHIVED":
		where = append(where, `u.archived_at IS NOT NULL`)
	default:
		return ListResult{}, InvalidError{"status", "未知状态筛选"}
	}
	if f.SystemRole != "" {
		if !ValidSystemRole(f.SystemRole) {
			return ListResult{}, InvalidError{"systemRole", "未知系统角色"}
		}
		add(`EXISTS (SELECT 1 FROM system_memberships s WHERE s.user_id=u.id AND s.role=?)`, f.SystemRole)
	}
	cond := strings.Join(where, " AND ")
	var out ListResult
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users u WHERE `+cond, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+personColumns+` FROM users u LEFT JOIN departments d ON d.id=u.department_id WHERE `+cond+
		` ORDER BY u.enabled DESC, u.display_name, u.id LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Items = []Person{}
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, p)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func itoa(n int) string { return big.NewInt(int64(n)).String() }

func loadPerson(ctx context.Context, q queryer, id string) (Person, error) {
	p, err := scanPerson(q.QueryRowContext(ctx, `SELECT `+personColumns+` FROM users u LEFT JOIN departments d ON d.id=u.department_id
WHERE u.id=$1 AND (u.issuer IS NOT NULL OR u.username IS NOT NULL)`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func grantsOf(ctx context.Context, q queryer, id string) (portfolio, project []GrantRef, err error) {
	portfolio, project = []GrantRef{}, []GrantRef{}
	rows, err := q.QueryContext(ctx, `SELECT m.portfolio_scope_id, s.name, m.role FROM portfolio_memberships m JOIN portfolio_scopes s ON s.id=m.portfolio_scope_id WHERE m.user_id=$1 ORDER BY s.name`, id)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var g GrantRef
		if err = rows.Scan(&g.ScopeID, &g.ScopeName, &g.Role); err != nil {
			rows.Close()
			return nil, nil, err
		}
		portfolio = append(portfolio, g)
	}
	rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT m.project_id, p.name, m.role FROM project_memberships m JOIN projects p ON p.id=m.project_id WHERE m.user_id=$1 ORDER BY p.name`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g GrantRef
		if err = rows.Scan(&g.ScopeID, &g.ScopeName, &g.Role); err != nil {
			return nil, nil, err
		}
		project = append(project, g)
	}
	return portfolio, project, rows.Err()
}

// personnelActions are the audit actions shown in a person's security history.
var personnelActions = []string{"personnel_created", "account_enabled", "account_disabled", "password_reset", "sessions_revoked",
	"profile_updated", "system_role_changed", "portfolio_role_changed", "project_role_changed", "account_adopted",
	"account_provision_failed", "account_provision_cancelled", "identity_sync", "login_success", "logout", "identity_bound",
	"personnel_archived", "personnel_purged", "identity_protection_changed", "identity_test_account_changed"}

// Get returns one person with grants, live identity-provider status and security history.
func (s *Service) Get(ctx context.Context, a Actor, id string) (PersonDetail, error) {
	var d PersonDetail
	if _, err := s.Authorize(ctx, a, CapPersonnelRead); err != nil {
		return d, err
	}
	p, err := loadPerson(ctx, s.DB, id)
	if err != nil {
		return d, err
	}
	if p.PortfolioGrants, p.ProjectGrants, err = grantsOf(ctx, s.DB, id); err != nil {
		return d, err
	}
	d.Person = p
	d.History = []HistoryEntry{}
	rows, err := s.DB.QueryContext(ctx, `SELECT observed_at, user_id, action, result, COALESCE(scope, COALESCE('project:'||project_id,'')), COALESCE(change::text,'null'), request_id, COALESCE(host(source_ip),'')
FROM audit_logs WHERE (target_user_id=$1 OR (user_id=$1 AND action IN ('login_success','logout')) OR (action='project_role_changed' AND (result LIKE 'granted:'||$1||':%' OR result='revoked:'||$1)))
AND action = ANY($2) ORDER BY observed_at DESC, id DESC LIMIT 100`, id, personnelActions)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var h HistoryEntry
		var change string
		if err = rows.Scan(&h.At, &h.Actor, &h.Action, &h.Result, &h.Scope, &change, &h.RequestID, &h.SourceIP); err != nil {
			return d, err
		}
		h.Change = json.RawMessage(change)
		d.History = append(d.History, h)
	}
	if err = rows.Err(); err != nil {
		return d, err
	}
	d.Identity = s.identityStatus(ctx, id)
	return d, nil
}

func (s *Service) subjectOf(ctx context.Context, q queryer, id string) (string, error) {
	var issuer, subject sql.NullString
	err := q.QueryRowContext(ctx, `SELECT issuer, subject FROM users WHERE id=$1`, id).Scan(&issuer, &subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !issuer.Valid || issuer.String != s.Issuer || !kcadmin.ValidSubject(subject.String) {
		return "", nil
	}
	return subject.String, nil
}

func (s *Service) identityStatus(ctx context.Context, id string) IdentityStatus {
	st := IdentityStatus{}
	if s.IdP == nil {
		return st
	}
	subject, err := s.subjectOf(ctx, s.DB, id)
	if err != nil || subject == "" {
		st.Available = err == nil
		return st
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u, err := s.IdP.Get(call, subject)
	if err != nil {
		return st
	}
	st.Available = true
	if u == nil {
		return st
	}
	st.Exists = true
	st.Enabled = u.Enabled != nil && *u.Enabled
	for _, ra := range u.RequiredActions {
		if ra == "UPDATE_PASSWORD" {
			st.PasswordChangeRequired = true
		}
	}
	return st
}

// ---------------------------------------------------------------------------------------------
// Departments
// ---------------------------------------------------------------------------------------------

func (s *Service) Departments(ctx context.Context, a Actor) ([]Department, error) {
	if _, err := s.Authorize(ctx, a, CapPersonnelRead); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.id, d.name, d.status, d.version, (SELECT count(*) FROM users u WHERE u.department_id=d.id AND u.archived_at IS NULL) FROM departments d ORDER BY d.status, d.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Department{}
	for rows.Next() {
		var d Department
		if err = rows.Scan(&d.ID, &d.Name, &d.Status, &d.Version, &d.MemberCount); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveDepartment creates (version 0) or renames / enables / disables (version = current) a department.
func (s *Service) SaveDepartment(ctx context.Context, a Actor, d Department) (Department, error) {
	if _, err := s.Authorize(ctx, a, CapDepartmentManage); err != nil {
		return d, err
	}
	d.ID, d.Name = strings.TrimSpace(d.ID), strings.TrimSpace(d.Name)
	if !idPattern.MatchString(d.ID) {
		return d, InvalidError{"id", "部门编号只能包含字母、数字、- 和 _（1–80 位）"}
	}
	if n := utf8.RuneCountInString(d.Name); n == 0 || n > 80 {
		return d, InvalidError{"name", "请填写部门名称（不超过 80 个字符）"}
	}
	if d.Status == "" {
		d.Status = "ACTIVE"
	}
	if d.Status != "ACTIVE" && d.Status != "DISABLED" {
		return d, InvalidError{"status", "未知部门状态"}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	if _, err = authorize(ctx, tx, a, CapDepartmentManage); err != nil {
		return d, err
	}
	var result sql.Result
	action := "department_created"
	if d.Version == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO departments(id,name,status) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, d.ID, d.Name, d.Status)
	} else {
		action = "department_updated"
		result, err = tx.ExecContext(ctx, `UPDATE departments SET name=$2,status=$3,version=version+1,updated_at=now() WHERE id=$1 AND version=$4`, d.ID, d.Name, d.Status, d.Version)
	}
	if isUniqueViolation(err) {
		return d, ErrConflict
	}
	if err != nil {
		return d, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return d, ErrConflict
	}
	if err = auditlog.Write(ctx, tx, a.event(action, "success", "", "department:"+d.ID, map[string]any{"name": d.Name, "status": d.Status})); err != nil {
		return d, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT version FROM departments WHERE id=$1`, d.ID).Scan(&d.Version); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}
