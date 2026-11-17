package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// V1.1 R5 (B2): customers and contacts on the existing master data
// (organizations, contacts, project_parties, project_contacts). Visibility is
// decided in SQL before any search, hint or detail:
//
//	administrator    every customer
//	director         customers with a team profile or an assigned project
//	sales/supervisor customers their team maintains (team profile) or that a
//	                 project they may read names
//
// Projects inside a customer are filtered by projectScope; contacts belong to
// a team and are visible to that team (and directors/administrators) only.
// The globally unique normalized name never reveals another team's data: an
// unreadable customer is 404 like a nonexistent one, and a similar name in
// another team is reported only by one fixed notice.

// orgVisibleSQL: params $1 subject, $2 role, $3 team.
func orgVisibleSQL(o string) string {
	return `($2='baobei_admin'
 OR ($2='baobei_director' AND (EXISTS(SELECT 1 FROM organization_team_profiles tp WHERE tp.organization_id=` + o + `.id)
   OR EXISTS(SELECT 1 FROM project_parties pp JOIN intake_projects p ON p.id=pp.project_id WHERE pp.organization_id=` + o + `.id AND p.owner_subject IS NOT NULL AND p.discarded_at IS NULL)))
 OR ($2 IN ('baobei_sales','baobei_manager') AND (($3<>'' AND EXISTS(SELECT 1 FROM organization_team_profiles tp WHERE tp.organization_id=` + o + `.id AND tp.team_id::text=$3))
   OR EXISTS(SELECT 1 FROM project_parties pp JOIN intake_projects p ON p.id=pp.project_id WHERE pp.organization_id=` + o + `.id AND p.discarded_at IS NULL AND ` + projectScope("p") + `))))`
}

// contactVisibleSQL: params $2 role, $3 team.
func contactVisibleSQL(c string) string {
	// $1 is typed so every statement can share the (subject, role, team) prefix.
	return `($1::text IS NOT NULL AND ($2 IN ('baobei_admin','baobei_director') OR ($3<>'' AND ` + c + `.team_id::text=$3)))`
}

type CustomerProfile struct {
	TeamID       string    `json:"teamId"`
	TeamName     string    `json:"teamName"`
	Industry     string    `json:"industry"`
	Region       string    `json:"region"`
	CustomerType string    `json:"customerType"`
	Notes        string    `json:"notes"`
	Version      int       `json:"version"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
type Contact struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Title      string     `json:"title"`
	Department string     `json:"department"`
	Phone      string     `json:"phone"`
	Notes      string     `json:"notes"`
	TeamID     string     `json:"teamId"`
	Version    int        `json:"version"`
	ArchivedAt *time.Time `json:"archivedAt"`
	CanEdit    bool       `json:"canEdit"`
}
type CustomerItem struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	CreditCode    string           `json:"creditCode,omitempty"`
	Profile       *CustomerProfile `json:"profile"`
	ProjectCount  int              `json:"projectCount"`
	ContactCount  int              `json:"contactCount"`
	LastProjectAt *time.Time       `json:"lastProjectAt"`
}
type CustomerProject struct {
	ID             string    `json:"id"`
	RegistrationNo string    `json:"registrationNo"`
	ProjectName    string    `json:"projectName"`
	Role           string    `json:"role"`
	SalesStage     string    `json:"salesStage"`
	IntakeStatus   string    `json:"intakeStatus"`
	OwnerName      string    `json:"ownerName"`
	UpdatedAt      time.Time `json:"updatedAt"`
}
type CustomerDetail struct {
	CustomerItem
	Profiles []CustomerProfile     `json:"profiles,omitempty"`
	Projects []CustomerProject     `json:"projects"`
	Contacts []Contact             `json:"contacts"`
	Actions  map[string]ActionHint `json:"actions"`
	History  []CustomerEvent       `json:"history"`
}
type CustomerEvent struct {
	Action    string         `json:"action"`
	ActorName string         `json:"actorName"`
	Details   map[string]any `json:"details"`
	At        time.Time      `json:"at"`
}

var (
	customerMissing   = staffProblem{404, "CUSTOMER_NOT_FOUND", "客户不存在或不在你的范围内"}
	contactMissing    = staffProblem{404, "CONTACT_NOT_FOUND", "联系人不存在或不在你的范围内"}
	customerForbidden = staffProblem{403, "CUSTOMER_FORBIDDEN", "当前账号不能维护该客户资料"}
	customerConflict  = staffProblem{409, "VERSION_CONFLICT", "资料已被其他人更新，请刷新后重试"}
	creditCodePattern = regexp.MustCompile(`^[0-9A-HJ-NPQRTUWXY]{2}[0-9]{6}[0-9A-HJ-NPQRTUWXY]{10}$`)
	customerTypes     = []string{"", "END_USER", "EQUIPMENT_MAKER", "DESIGN_INSTITUTE", "EPC", "INTEGRATOR", "AGENT", "OTHER"}
)

// teamMaintainer: sales members and supervisors maintain their team's
// customer profile and contacts (V1.1 B2). Directors read; administrators
// manage credit codes.
func teamMaintainer(u User) bool { return filerRole(u.Role) && u.TeamID != "" }

func maskContactPhone(p string) string {
	r := []rune(p)
	if len(r) < 7 {
		return strings.Repeat("*", len(r))
	}
	return string(r[:3]) + strings.Repeat("*", len(r)-7) + string(r[len(r)-4:])
}

func textOK(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsFunc(s, func(r rune) bool { return r < 32 && r != '\n' })
}

func customerAudit(ctx context.Context, tx pgx.Tx, org, team, contact, project string, u User, action string, details map[string]any) error {
	_, err := tx.Exec(ctx, `INSERT INTO customer_audit(organization_id,team_id,contact_id,project_id,actor_subject,action,details)
 VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7)`, org, team, contact, project, u.Subject, action, mustJSON(details))
	return err
}

func (a *App) customerVisible(ctx context.Context, q querier, u User, id string) (bool, error) {
	if !uuidPattern.MatchString(id) {
		return false, nil
	}
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations o WHERE o.id=$4 AND `+orgVisibleSQL("o")+`)`, u.Subject, u.Role, u.TeamID, id).Scan(&ok)
	return ok, err
}

// V1.1 R7-D team-safe name. The shared master spelling (organizations.name) is
// only shown to directors and administrators. A sales member or supervisor
// sees the team's own spelling (team profile); without one, the spelling of a
// project the viewer may read that names the customer (chosen in Go by the
// same normalization); never another team's spelling.
func teamSafeNameSQL(o string) string {
	return `CASE WHEN $2 IN ('baobei_admin','baobei_director') THEN ` + o + `.name ELSE NULLIF(tp.display_name,'') END,` + o + `.normalized_name,
 CASE WHEN $2 IN ('baobei_admin','baobei_director') OR coalesce(tp.display_name,'')<>'' THEN '[]'::jsonb ELSE coalesce((SELECT jsonb_agg(x.n) FROM (
  SELECT CASE WHEN pp.role='END_CUSTOMER' THEN p.document->>'customer' ELSE e->>'name' END AS n
  FROM project_parties pp JOIN intake_projects p ON p.id=pp.project_id
  LEFT JOIN LATERAL jsonb_array_elements(CASE WHEN pp.role<>'END_CUSTOMER' AND jsonb_typeof(p.document->'parties')='array' THEN p.document->'parties' ELSE '[]'::jsonb END) e ON e->>'role'=pp.role
  WHERE pp.organization_id=` + o + `.id AND p.discarded_at IS NULL AND ` + projectScope("p") + ` ORDER BY p.created_at,p.id LIMIT 20) x WHERE x.n IS NOT NULL),'[]'::jsonb) END`
}

// pickName returns the viewer-safe name from the projection above.
func pickName(shown *string, normalized string, candidates []byte) string {
	if shown != nil && *shown != "" {
		return *shown
	}
	var names []string
	_ = json.Unmarshal(candidates, &names)
	for _, n := range names {
		if normalize(n) == normalized {
			return n
		}
	}
	return normalized
}

const customerItemColumns = `o.id::text,NAMES,coalesce(o.credit_code,''),
 (SELECT count(DISTINCT p.id) FROM project_parties pp JOIN intake_projects p ON p.id=pp.project_id WHERE pp.organization_id=o.id AND p.discarded_at IS NULL AND ` + `PSCOPE` + `),
 (SELECT count(*) FROM contacts c WHERE c.organization_id=o.id AND c.archived_at IS NULL AND ` + `CVIS` + `),
 (SELECT max(p.updated_at) FROM project_parties pp JOIN intake_projects p ON p.id=pp.project_id WHERE pp.organization_id=o.id AND p.discarded_at IS NULL AND ` + `PSCOPE` + `),
 tp.team_id::text,coalesce(t.name,''),tp.industry,tp.region,tp.customer_type,tp.notes,tp.version,tp.updated_at`

func customerColumns() string {
	return strings.NewReplacer("NAMES", teamSafeNameSQL("o"), "PSCOPE", projectScope("p"), "CVIS", contactVisibleSQL("c")).Replace(customerItemColumns)
}

func scanCustomer(row interface{ Scan(...any) error }, u User) (CustomerItem, error) {
	var c CustomerItem
	var team, teamName, industry, region, ctype, notes, shown *string
	var normalized string
	var candidates []byte
	var version *int
	var updated *time.Time
	err := row.Scan(&c.ID, &shown, &normalized, &candidates, &c.CreditCode, &c.ProjectCount, &c.ContactCount, &c.LastProjectAt, &team, &teamName, &industry, &region, &ctype, &notes, &version, &updated)
	if err != nil {
		return c, err
	}
	c.Name = pickName(shown, normalized, candidates)
	if team != nil {
		c.Profile = &CustomerProfile{TeamID: *team, TeamName: *teamName, Industry: *industry, Region: *region, CustomerType: *ctype, Notes: *notes, Version: *version, UpdatedAt: *updated}
	}
	// Credit codes are master data managed by administrators; teams do not
	// see a code another team may have supplied.
	if u.Role != adminRole && u.Role != directorRole {
		c.CreditCode = ""
	}
	return c, nil
}

var customerListParams = map[string]bool{"q": true, "page": true, "pageSize": true}

func (a *App) listCustomers(ctx context.Context, u User, params map[string]string) (map[string]any, error) {
	for k := range params {
		if !customerListParams[k] {
			return nil, listError("客户列表不支持的筛选条件：" + k)
		}
	}
	if err := strictPaging(params); err != nil {
		return nil, err
	}
	page, _ := strconv.Atoi(params["page"])
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(params["pageSize"])
	if size < 1 {
		size = 20
	}
	q := strings.TrimSpace(params["q"])
	if utf8.RuneCountInString(q) > 100 {
		return nil, listError("搜索内容过长")
	}
	where := ` FROM organizations o LEFT JOIN organization_team_profiles tp ON tp.organization_id=o.id AND tp.team_id::text=$3 LEFT JOIN teams t ON t.id=tp.team_id
 WHERE ` + orgVisibleSQL("o") + ` AND ($4='' OR ($5<>'' AND strpos(o.normalized_name,$5)>0)
  OR tp.display_name ILIKE '%'||$4||'%' ESCAPE '\'
  OR ($2 IN ('baobei_admin','baobei_director') AND o.name ILIKE '%'||$4||'%' ESCAPE '\'))`
	// Search never probes another team's spelling: the normalized name (the
	// same for every spelling) and the team's own spelling only; LIKE
	// wildcards in the input are literal.
	args := []any{u.Subject, u.Role, u.TeamID, likeEscape(q), normalize(q)}
	var total int
	if err := a.db.QueryRow(ctx, `SELECT count(*)`+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := a.db.Query(ctx, `SELECT `+customerColumns()+where+` ORDER BY coalesce(NULLIF(tp.display_name,''),o.normalized_name),o.id LIMIT $6 OFFSET $7`, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CustomerItem{}
	for rows.Next() {
		c, err := scanCustomer(rows, u)
		if err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return map[string]any{"items": items, "total": total, "page": page, "pageSize": size}, rows.Err()
}

func (a *App) customerDetail(ctx context.Context, q querier, u User, id string) (CustomerDetail, error) {
	var d CustomerDetail
	ok, err := a.customerVisible(ctx, q, u, id)
	if err != nil {
		return d, err
	}
	if !ok {
		return d, customerMissing
	}
	d.CustomerItem, err = scanCustomer(q.QueryRow(ctx, `SELECT `+customerColumns()+` FROM organizations o
 LEFT JOIN organization_team_profiles tp ON tp.organization_id=o.id AND tp.team_id::text=$3 LEFT JOIN teams t ON t.id=tp.team_id WHERE o.id=$4`, u.Subject, u.Role, u.TeamID, id), u)
	if err != nil {
		return d, err
	}
	// Directors and administrators see every team's profile of the customer.
	d.Profiles = []CustomerProfile{}
	if u.Role == directorRole || u.Role == adminRole {
		rows, err := q.Query(ctx, `SELECT tp.team_id::text,t.name,tp.industry,tp.region,tp.customer_type,tp.notes,tp.version,tp.updated_at
 FROM organization_team_profiles tp JOIN teams t ON t.id=tp.team_id WHERE tp.organization_id=$1 ORDER BY t.name`, id)
		if err != nil {
			return d, err
		}
		for rows.Next() {
			var p CustomerProfile
			if err = rows.Scan(&p.TeamID, &p.TeamName, &p.Industry, &p.Region, &p.CustomerType, &p.Notes, &p.Version, &p.UpdatedAt); err != nil {
				rows.Close()
				return d, err
			}
			d.Profiles = append(d.Profiles, p)
		}
		rows.Close()
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT ON (p.id) p.id::text,p.registration_no,p.project_name,pp.role,p.sales_stage,p.intake_status,`+displayName("p.owner_subject")+`,p.updated_at
 FROM project_parties pp JOIN intake_projects p ON p.id=pp.project_id WHERE pp.organization_id=$4 AND p.discarded_at IS NULL AND `+projectScope("p")+`
 ORDER BY p.id, pp.role`, u.Subject, u.Role, u.TeamID, id)
	if err != nil {
		return d, err
	}
	d.Projects = []CustomerProject{}
	for rows.Next() {
		var p CustomerProject
		if err = rows.Scan(&p.ID, &p.RegistrationNo, &p.ProjectName, &p.Role, &p.SalesStage, &p.IntakeStatus, &p.OwnerName, &p.UpdatedAt); err != nil {
			rows.Close()
			return d, err
		}
		d.Projects = append(d.Projects, p)
	}
	rows.Close()
	if d.Contacts, err = a.contactsOf(ctx, q, u, id); err != nil {
		return d, err
	}
	d.Actions = map[string]ActionHint{"customer.edit": hint("CUSTOMER_FORBIDDEN"), "contact.create": hint("CUSTOMER_FORBIDDEN"), "customer.credit-code": hint("CUSTOMER_FORBIDDEN")}
	if teamMaintainer(u) {
		d.Actions["customer.edit"], d.Actions["contact.create"] = hint(""), hint("")
	}
	if u.Role == adminRole {
		d.Actions["customer.credit-code"] = hint("")
	}
	hrows, err := q.Query(ctx, `SELECT a.action,`+displayName("a.actor_subject")+`,a.details,a.at FROM customer_audit a WHERE a.organization_id=$1
 AND ($2 IN ('baobei_admin','baobei_director') OR ($3<>'' AND a.team_id::text=$3)) ORDER BY a.at DESC,a.id LIMIT 30`, id, u.Role, u.TeamID)
	if err != nil {
		return d, err
	}
	defer hrows.Close()
	d.History = []CustomerEvent{}
	for hrows.Next() {
		var e CustomerEvent
		var b []byte
		if err = hrows.Scan(&e.Action, &e.ActorName, &b, &e.At); err != nil {
			return d, err
		}
		_ = json.Unmarshal(b, &e.Details)
		d.History = append(d.History, e)
	}
	return d, hrows.Err()
}

func (a *App) contactsOf(ctx context.Context, q querier, u User, org string) ([]Contact, error) {
	rows, err := q.Query(ctx, `SELECT c.id::text,c.display_name,c.title,c.department,c.phone,c.notes,coalesce(c.team_id::text,''),c.version,c.archived_at
 FROM contacts c WHERE c.organization_id=$4 AND `+contactVisibleSQL("c")+` ORDER BY c.archived_at NULLS FIRST,c.display_name,c.id`, u.Subject, u.Role, u.TeamID, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		var c Contact
		if err = rows.Scan(&c.ID, &c.Name, &c.Title, &c.Department, &c.Phone, &c.Notes, &c.TeamID, &c.Version, &c.ArchivedAt); err != nil {
			return nil, err
		}
		c.CanEdit = teamMaintainer(u) && c.TeamID == u.TeamID
		if c.TeamID != u.TeamID {
			c.Phone = maskContactPhone(c.Phone)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type customerInput struct {
	Name         string `json:"name"`
	Industry     string `json:"industry"`
	Region       string `json:"region"`
	CustomerType string `json:"customerType"`
	Notes        string `json:"notes"`
	Version      int    `json:"version"`
}

func (in *customerInput) valid(create bool) error {
	in.Name, in.Industry, in.Region, in.Notes = strings.TrimSpace(in.Name), strings.TrimSpace(in.Industry), strings.TrimSpace(in.Region), strings.TrimSpace(in.Notes)
	if create && (in.Name == "" || !textOK(in.Name, 200)) {
		return fieldProblem{staffProblem{400, "CUSTOMER_NAME_INVALID", "请填写客户名称（不超过 200 字）"}, "name"}
	}
	if !textOK(in.Industry, 100) || !textOK(in.Region, 100) || !textOK(in.Notes, 2000) || !contains(customerTypes, in.CustomerType) {
		return staffProblem{400, "INVALID_INPUT", "请检查客户资料字段"}
	}
	return nil
}

// createCustomer: the team's profile of a customer. An existing master record
// with the same normalized name is reused, never merged with a similar one;
// if the team already maintains it the call is idempotent (same id).
func (a *App) createCustomer(ctx context.Context, tx pgx.Tx, u User, in customerInput) (string, bool, error) {
	if !teamMaintainer(u) {
		return "", false, customerForbidden
	}
	if err := in.valid(true); err != nil {
		return "", false, err
	}
	var org string
	if err := tx.QueryRow(ctx, `INSERT INTO organizations(name,normalized_name) VALUES($1,$2) ON CONFLICT(normalized_name) DO UPDATE SET normalized_name=excluded.normalized_name RETURNING id::text`, in.Name, normalize(in.Name)).Scan(&org); err != nil {
		return "", false, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO organization_team_profiles(organization_id,team_id,display_name,industry,region,customer_type,notes,created_by_subject)
 VALUES($1,$2::uuid,$8,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, org, u.TeamID, in.Industry, in.Region, in.CustomerType, in.Notes, u.Subject, in.Name)
	if err != nil {
		return "", false, err
	}
	if tag.RowsAffected() == 0 {
		return org, true, nil
	}
	return org, false, customerAudit(ctx, tx, org, u.TeamID, "", "", u, "customer.create", map[string]any{"name": in.Name})
}

type similarCustomers struct {
	Items      []CustomerItem     `json:"items"`
	Restricted *RestrictedRelated `json:"restrictedRelated"`
}

// similar: visible customers with a similar name, plus one fixed notice when
// a similar name exists only outside the caller's view (no name, id or count).
func (a *App) similarCustomers(ctx context.Context, u User, name string) (similarCustomers, error) {
	out := similarCustomers{Items: []CustomerItem{}}
	n := normalize(name)
	if utf8.RuneCountInString(n) < 2 || utf8.RuneCountInString(n) > 200 {
		return out, nil
	}
	rows, err := a.db.Query(ctx, `SELECT `+customerColumns()+` FROM organizations o
 LEFT JOIN organization_team_profiles tp ON tp.organization_id=o.id AND tp.team_id::text=$3 LEFT JOIN teams t ON t.id=tp.team_id
 WHERE (o.normalized_name=$4 OR similarity(o.normalized_name,$4)>=0.45) AND `+orgVisibleSQL("o")+` ORDER BY similarity(o.normalized_name,$4) DESC,o.id LIMIT 10`, u.Subject, u.Role, u.TeamID, n)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		c, err := scanCustomer(rows, u)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, c)
	}
	rows.Close()
	var hidden bool
	if err = a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations o WHERE (o.normalized_name=$4 OR similarity(o.normalized_name,$4)>=0.45)
 AND (EXISTS(SELECT 1 FROM organization_team_profiles tp WHERE tp.organization_id=o.id) OR EXISTS(SELECT 1 FROM project_parties pp WHERE pp.organization_id=o.id))
 AND NOT `+orgVisibleSQL("o")+`)`, u.Subject, u.Role, u.TeamID, n).Scan(&hidden); err != nil {
		return out, err
	}
	if hidden {
		out.Restricted = &RestrictedRelated{Code: "CUSTOMER_SIMILAR_RESTRICTED", Route: "ADMIN",
			Message: "可能已有其他团队维护名称相近的客户。为保护客户信息不显示详情，如需合并或核对请联系管理员。"}
	}
	return out, nil
}

type contactInput struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Department string `json:"department"`
	Phone      string `json:"phone"`
	Notes      string `json:"notes"`
	Version    int    `json:"version"`
	Reason     string `json:"reason"`
}

var contactPhone = regexp.MustCompile(`^\+?[0-9][0-9 -]{5,30}$`)

func (in *contactInput) valid() error {
	in.Name, in.Title, in.Department, in.Phone, in.Notes = strings.TrimSpace(in.Name), strings.TrimSpace(in.Title), strings.TrimSpace(in.Department), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Notes)
	if in.Name == "" || !textOK(in.Name, 64) {
		return fieldProblem{staffProblem{400, "CONTACT_NAME_INVALID", "请填写联系人姓名（不超过 64 字）"}, "name"}
	}
	if in.Phone != "" && !contactPhone.MatchString(in.Phone) {
		return fieldProblem{staffProblem{400, "CONTACT_PHONE_INVALID", "联系电话格式不正确"}, "phone"}
	}
	if !textOK(in.Title, 100) || !textOK(in.Department, 100) || !textOK(in.Notes, 1000) {
		return staffProblem{400, "INVALID_INPUT", "请检查联系人字段"}
	}
	return nil
}

func (a *App) customers(w http.ResponseWriter, r *http.Request, u User) error {
	ctx := r.Context()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	parts := strings.Split(path, "/")
	params := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			fail(w, 400, "INVALID_FILTER", "筛选条件重复："+k)
			return nil
		}
		params[k] = v[0]
	}
	write := func(fn func(tx pgx.Tx) (any, error)) error {
		tx, err := a.beginBusiness(ctx, u, 0)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		out, err := fn(tx)
		if err != nil || out == nil {
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		respond(w, 200, out)
		return nil
	}
	switch {
	case parts[0] == "customers" && len(parts) == 1 && r.Method == "GET":
		out, err := a.listCustomers(ctx, u, params)
		if le, ok := err.(listError); ok {
			fail(w, 400, "INVALID_FILTER", le.Error())
			return nil
		}
		if err != nil {
			return err
		}
		respond(w, 200, out)
		return nil
	case parts[0] == "customers" && len(parts) == 2 && parts[1] == "similar" && r.Method == "GET":
		out, err := a.similarCustomers(ctx, u, params["name"])
		if err != nil {
			return err
		}
		respond(w, 200, out)
		return nil
	case parts[0] == "customers" && len(parts) == 1 && r.Method == "POST":
		var in customerInput
		if !decode(w, r, &in) {
			return nil
		}
		return write(func(tx pgx.Tx) (any, error) {
			id, existed, err := a.createCustomer(ctx, tx, u, in)
			if err != nil {
				return nil, err
			}
			d, err := a.customerDetail(ctx, tx, u, id)
			if err != nil {
				return nil, err
			}
			return map[string]any{"customer": d, "existed": existed}, nil
		})
	case parts[0] == "customers" && len(parts) == 2 && r.Method == "GET":
		d, err := a.customerDetail(ctx, a.db, u, parts[1])
		if err != nil {
			return err
		}
		respond(w, 200, d)
		return nil
	case parts[0] == "customers" && len(parts) == 2 && r.Method == "PATCH":
		var in customerInput
		if !decode(w, r, &in) {
			return nil
		}
		return write(func(tx pgx.Tx) (any, error) {
			if ok, err := a.customerVisible(ctx, tx, u, parts[1]); err != nil || !ok {
				return nil, firstErr(err, customerMissing)
			}
			if !teamMaintainer(u) {
				return nil, customerForbidden
			}
			if in.Name != "" {
				return nil, staffProblem{400, "CUSTOMER_NAME_FIXED", "客户名称是共享主档，如需更正请联系管理员"}
			}
			if err := in.valid(false); err != nil {
				return nil, err
			}
			tag, err := tx.Exec(ctx, `UPDATE organization_team_profiles SET industry=$3,region=$4,customer_type=$5,notes=$6,version=version+1,updated_at=now()
 WHERE organization_id=$1 AND team_id=$2::uuid AND version=$7`, parts[1], u.TeamID, in.Industry, in.Region, in.CustomerType, in.Notes, in.Version)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 0 {
				var exists bool
				if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_team_profiles WHERE organization_id=$1 AND team_id=$2::uuid)`, parts[1], u.TeamID).Scan(&exists); err != nil {
					return nil, err
				}
				if exists || in.Version != 0 {
					return nil, customerConflict
				}
				// First profile of a customer the team reaches through a project;
				// two concurrent first edits: the second is a conflict, not a 503.
				tag, err = tx.Exec(ctx, `INSERT INTO organization_team_profiles(organization_id,team_id,industry,region,customer_type,notes,created_by_subject)
 VALUES($1,$2::uuid,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, parts[1], u.TeamID, in.Industry, in.Region, in.CustomerType, in.Notes, u.Subject)
				if err != nil {
					return nil, err
				}
				if tag.RowsAffected() == 0 {
					return nil, customerConflict
				}
			}
			if err = customerAudit(ctx, tx, parts[1], u.TeamID, "", "", u, "customer.edit", map[string]any{"industry": in.Industry, "region": in.Region, "customerType": in.CustomerType, "notes": in.Notes}); err != nil {
				return nil, err
			}
			return a.customerDetail(ctx, tx, u, parts[1])
		})
	case parts[0] == "customers" && len(parts) == 3 && parts[2] == "credit-code" && r.Method == "POST":
		var in struct {
			CreditCode string `json:"creditCode"`
			Reason     string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return nil
		}
		return write(func(tx pgx.Tx) (any, error) {
			if u.Role != adminRole {
				return nil, customerForbidden
			}
			in.CreditCode = strings.ToUpper(strings.TrimSpace(in.CreditCode))
			if in.CreditCode != "" && !creditCodePattern.MatchString(in.CreditCode) || !reasonOK(in.Reason) {
				return nil, fieldProblem{staffProblem{400, "CREDIT_CODE_INVALID", "统一社会信用代码为 18 位，且需填写原因"}, "creditCode"}
			}
			if ok, err := a.customerVisible(ctx, tx, u, parts[1]); err != nil || !ok {
				return nil, firstErr(err, customerMissing)
			}
			var taken bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE credit_code=$1 AND id::text<>$2)`, in.CreditCode, parts[1]).Scan(&taken); err != nil {
				return nil, err
			}
			if taken && in.CreditCode != "" {
				return nil, staffProblem{409, "CREDIT_CODE_TAKEN", "该统一社会信用代码已属于另一个客户主档，请先核对是否同一客户"}
			}
			if _, err := tx.Exec(ctx, `UPDATE organizations SET credit_code=NULLIF($2,'') WHERE id=$1`, parts[1], in.CreditCode); err != nil {
				return nil, err
			}
			if err := customerAudit(ctx, tx, parts[1], "", "", "", u, "customer.credit-code", map[string]any{"creditCode": in.CreditCode, "reason": in.Reason}); err != nil {
				return nil, err
			}
			return a.customerDetail(ctx, tx, u, parts[1])
		})
	case parts[0] == "customers" && len(parts) == 3 && parts[2] == "contacts" && r.Method == "POST":
		var in contactInput
		if !decode(w, r, &in) {
			return nil
		}
		return write(func(tx pgx.Tx) (any, error) {
			if ok, err := a.customerVisible(ctx, tx, u, parts[1]); err != nil || !ok {
				return nil, firstErr(err, customerMissing)
			}
			if !teamMaintainer(u) {
				return nil, customerForbidden
			}
			if err := in.valid(); err != nil {
				return nil, err
			}
			// AC-23: a retried or double-submitted create (same customer, team,
			// name and phone, still active) returns the existing contact and
			// writes nothing; creates of one customer and team are serialized.
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,11))`, parts[1]+"|"+u.TeamID); err != nil {
				return nil, err
			}
			var id string
			err := tx.QueryRow(ctx, `SELECT id::text FROM contacts WHERE organization_id=$1 AND team_id=$2::uuid AND archived_at IS NULL
 AND lower(btrim(display_name))=lower($3) AND phone=$4 LIMIT 1`, parts[1], u.TeamID, in.Name, in.Phone).Scan(&id)
			if err == nil {
				return a.customerDetail(ctx, tx, u, parts[1])
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err := tx.QueryRow(ctx, `INSERT INTO contacts(organization_id,display_name,team_id,title,department,phone,notes,created_by_subject)
 VALUES($1,$2,$3::uuid,$4,$5,$6,$7,$8) RETURNING id::text`, parts[1], in.Name, u.TeamID, in.Title, in.Department, in.Phone, in.Notes, u.Subject).Scan(&id); err != nil {
				return nil, err
			}
			if err := customerAudit(ctx, tx, parts[1], u.TeamID, id, "", u, "contact.create", map[string]any{"name": in.Name}); err != nil {
				return nil, err
			}
			return a.customerDetail(ctx, tx, u, parts[1])
		})
	case parts[0] == "contacts" && len(parts) >= 2 && uuidPattern.MatchString(parts[1]) && r.Method != "GET":
		var in contactInput
		if !decode(w, r, &in) {
			return nil
		}
		action := ""
		if len(parts) == 3 {
			action = parts[2]
		}
		if !(r.Method == "PATCH" && action == "" || r.Method == "POST" && (action == "archive" || action == "restore")) {
			fail(w, 405, "METHOD_NOT_ALLOWED", "操作不支持")
			return nil
		}
		return write(func(tx pgx.Tx) (any, error) {
			var org, team string
			var version int
			var archived *time.Time
			err := tx.QueryRow(ctx, `SELECT c.organization_id::text,coalesce(c.team_id::text,''),c.version,c.archived_at FROM contacts c WHERE c.id=$4 AND `+contactVisibleSQL("c")+` FOR UPDATE`,
				u.Subject, u.Role, u.TeamID, parts[1]).Scan(&org, &team, &version, &archived)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, contactMissing
			}
			if err != nil {
				return nil, err
			}
			if !teamMaintainer(u) || team != u.TeamID {
				return nil, customerForbidden
			}
			if in.Version != version {
				return nil, customerConflict
			}
			event := "contact.edit"
			var editChanges map[string]any
			switch action {
			case "":
				if archived != nil {
					return nil, staffProblem{409, "INVALID_STATE", "联系人已停用，请先恢复再修改"}
				}
				if err = in.valid(); err != nil {
					return nil, err
				}
				var old Contact
				if err = tx.QueryRow(ctx, `SELECT display_name,title,department,phone,notes FROM contacts WHERE id=$1`, parts[1]).Scan(&old.Name, &old.Title, &old.Department, &old.Phone, &old.Notes); err != nil {
					return nil, err
				}
				changes := map[string]any{}
				for k, v := range map[string][2]string{"name": {old.Name, in.Name}, "title": {old.Title, in.Title}, "department": {old.Department, in.Department}, "phone": {maskContactPhone(old.Phone), maskContactPhone(in.Phone)}, "notes": {old.Notes, in.Notes}} {
					if v[0] != v[1] {
						changes[k] = map[string]string{"from": v[0], "to": v[1]}
					}
				}
				in.Reason = ""
				editChanges = changes
				_, err = tx.Exec(ctx, `UPDATE contacts SET display_name=$2,title=$3,department=$4,phone=$5,notes=$6,version=version+1,updated_at=now() WHERE id=$1`, parts[1], in.Name, in.Title, in.Department, in.Phone, in.Notes)
			case "archive", "restore":
				if (action == "archive") == (archived != nil) || !reasonOK(in.Reason) {
					return nil, staffProblem{409, "INVALID_STATE", "联系人状态已变化或缺少原因"}
				}
				event = "contact." + action
				_, err = tx.Exec(ctx, `UPDATE contacts SET archived_at=CASE WHEN $2 THEN now() END,version=version+1,updated_at=now() WHERE id=$1`, parts[1], action == "archive")
			}
			if err != nil {
				return nil, err
			}
			details := map[string]any{"name": in.Name, "reason": in.Reason}
			if editChanges != nil {
				details["changes"] = editChanges
			}
			if err = customerAudit(ctx, tx, org, team, parts[1], "", u, event, details); err != nil {
				return nil, err
			}
			return a.customerDetail(ctx, tx, u, org)
		})
	}
	fail(w, 404, "NOT_FOUND", "接口不存在")
	return nil
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func firstErr(err error, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// Project contacts (V1.1 B2): contacts of the project's customers, same team.
// Reading follows the project's read scope; linking needs the project's write
// right (owner) and a contact of the owner's team.
func (a *App) projectContacts(w http.ResponseWriter, r *http.Request, u User, p Project) error {
	ctx := r.Context()
	if r.Method == "GET" {
		out, err := a.linkedContacts(ctx, a.db, u, p.ID)
		if err != nil {
			return err
		}
		respond(w, 200, map[string]any{"items": out, "customers": a.projectCustomers(ctx, u, p.ID)})
		return nil
	}
	var in struct {
		ContactID string `json:"contactId"`
		Version   int    `json:"version"`
		Unlink    bool   `json:"unlink"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	tx, err := a.beginBusiness(ctx, u, 0)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cur, err := get(ctx, tx, p.ID, true)
	if err != nil {
		return err
	}
	if !canWrite(u, cur) || u.Role == adminRole {
		return problem(403, "BUSINESS_PERMISSION_DENIED")
	}
	if in.Version != cur.Version {
		return problem(409, "VERSION_CONFLICT")
	}
	if !techOpen(cur) {
		return problem(409, "INVALID_STATE")
	}
	if !uuidPattern.MatchString(in.ContactID) {
		return contactMissing
	}
	var org string
	if in.Unlink {
		// Unlinking needs only the existing link (an archived contact or one
		// of a former team can still be removed from the project).
		err = tx.QueryRow(ctx, `SELECT c.organization_id::text FROM project_contacts pc JOIN contacts c ON c.id=pc.contact_id WHERE pc.project_id=$1 AND pc.contact_id=$2`, cur.ID, in.ContactID).Scan(&org)
	} else {
		// FOR SHARE: a concurrent archive of this contact waits (or this link
		// sees the archived state); never a link to a just-archived contact.
		err = tx.QueryRow(ctx, `SELECT c.organization_id::text FROM contacts c WHERE c.id=$1 AND c.archived_at IS NULL AND c.team_id::text=$2
 AND EXISTS(SELECT 1 FROM project_parties pp WHERE pp.project_id=$3 AND pp.organization_id=c.organization_id) FOR SHARE OF c`, in.ContactID, cur.TeamID, cur.ID).Scan(&org)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return contactMissing
	}
	if err != nil {
		return err
	}
	event := "project-contact.link"
	var tag pgconn.CommandTag
	if in.Unlink {
		event = "project-contact.unlink"
		tag, err = tx.Exec(ctx, `DELETE FROM project_contacts WHERE project_id=$1 AND contact_id=$2`, cur.ID, in.ContactID)
	} else {
		tag, err = tx.Exec(ctx, `INSERT INTO project_contacts(project_id,contact_id,linked_by_subject) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, cur.ID, in.ContactID, u.Subject)
	}
	if err != nil {
		return err
	}
	// Nothing changed (already linked): no audit row, a clear conflict.
	if tag.RowsAffected() == 0 {
		return problem(409, "INVALID_STATE")
	}
	if err = customerAudit(ctx, tx, org, cur.TeamID, in.ContactID, cur.ID, u, event, map[string]any{}); err != nil {
		return err
	}
	out, err := a.linkedContacts(ctx, tx, u, cur.ID)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	respond(w, 200, map[string]any{"items": out, "customers": a.projectCustomers(ctx, u, cur.ID)})
	return nil
}

func (a *App) linkedContacts(ctx context.Context, q querier, u User, project string) ([]Contact, error) {
	rows, err := q.Query(ctx, `SELECT c.id::text,c.display_name,c.title,c.department,c.phone,c.notes,coalesce(c.team_id::text,''),c.version,c.archived_at
 FROM project_contacts pc JOIN contacts c ON c.id=pc.contact_id WHERE pc.project_id=$4 AND `+contactVisibleSQL("c")+` ORDER BY c.display_name,c.id`, u.Subject, u.Role, u.TeamID, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		var c Contact
		if err = rows.Scan(&c.ID, &c.Name, &c.Title, &c.Department, &c.Phone, &c.Notes, &c.TeamID, &c.Version, &c.ArchivedAt); err != nil {
			return nil, err
		}
		if c.TeamID != u.TeamID {
			c.Phone = maskContactPhone(c.Phone)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// projectCustomers: the project's parties as customer links (ids are given
// only for customers the caller may open) plus the team's linkable contacts.
func (a *App) projectCustomers(ctx context.Context, u User, project string) []map[string]any {
	out := []map[string]any{}
	// The name is the spelling in this project's own document (the caller may
	// read the project); never the shared master spelling.
	rows, err := a.db.Query(ctx, `SELECT o.id::text,o.normalized_name,coalesce((SELECT jsonb_agg(n) FROM (SELECT CASE WHEN pp.role='END_CUSTOMER' THEN p.document->>'customer' END AS n
   FROM intake_projects p WHERE p.id=$4 UNION ALL SELECT e->>'name' FROM intake_projects p,
   jsonb_array_elements(CASE WHEN jsonb_typeof(p.document->'parties')='array' THEN p.document->'parties' ELSE '[]'::jsonb END) e WHERE p.id=$4 AND e->>'role'=pp.role) z WHERE n IS NOT NULL),'[]'::jsonb),pp.role,`+orgVisibleSQL("o")+`,
 coalesce((SELECT json_agg(json_build_object('id',c.id,'name',c.display_name,'title',c.title) ORDER BY c.display_name)
  FROM contacts c JOIN intake_projects p ON p.id=$4 WHERE c.organization_id=o.id AND c.archived_at IS NULL AND c.team_id=p.team_id AND `+contactVisibleSQL("c")+`),'[]')
 FROM project_parties pp JOIN organizations o ON o.id=pp.organization_id WHERE pp.project_id=$4 ORDER BY pp.role,o.normalized_name`, u.Subject, u.Role, u.TeamID, project)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, normalized, role string
		var visible bool
		var contacts, candidates []byte
		if rows.Scan(&id, &normalized, &candidates, &role, &visible, &contacts) != nil {
			return out
		}
		name := pickName(nil, normalized, candidates)
		item := map[string]any{"name": name, "role": role}
		if visible {
			item["id"] = id
			var cs []map[string]any
			_ = json.Unmarshal(contacts, &cs)
			item["contacts"] = cs
		}
		out = append(out, item)
	}
	return out
}
