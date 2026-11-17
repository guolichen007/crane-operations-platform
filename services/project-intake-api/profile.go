package main

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// V1.1 R3: own profile. A member reads and confirms (or later corrects) only
// their own display name. Role, team, subject, staff id and phone are not part
// of the request and cannot change here; every change has an audit row.
type ownProfile struct {
	Name      string        `json:"name"`
	Role      string        `json:"role"`
	Team      string        `json:"team"`
	Completed bool          `json:"completed"`
	Source    string        `json:"source"`
	Version   int           `json:"version"`
	History   []profileItem `json:"history"`
}
type profileItem struct {
	Action  string    `json:"action"`
	OldName string    `json:"oldName"`
	NewName string    `json:"newName"`
	Reason  string    `json:"reason"`
	At      time.Time `json:"at"`
}

func (a *App) readProfile(r *http.Request, u User) (ownProfile, error) {
	var p ownProfile
	ctx := r.Context()
	err := a.db.QueryRow(ctx, `SELECT u.display_name,u.role,coalesce((SELECT t.name FROM teams t WHERE t.id=u.team_id),''),
 u.profile_completed_at IS NOT NULL,u.profile_name_source,u.profile_version FROM app_users u WHERE u.subject=$1 AND u.active`, u.Subject).
		Scan(&p.Name, &p.Role, &p.Team, &p.Completed, &p.Source, &p.Version)
	if err != nil {
		return p, err
	}
	rows, err := a.db.Query(ctx, `SELECT a.action,a.old_name,a.new_name,a.reason,a.at FROM profile_audit a JOIN app_users u ON u.staff_id=a.staff_id
 WHERE u.subject=$1 ORDER BY a.at DESC,a.id DESC LIMIT 20`, u.Subject)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.History = []profileItem{}
	for rows.Next() {
		var it profileItem
		if err = rows.Scan(&it.Action, &it.OldName, &it.NewName, &it.Reason, &it.At); err != nil {
			return p, err
		}
		p.History = append(p.History, it)
	}
	return p, rows.Err()
}

func (a *App) profileHandler(w http.ResponseWriter, r *http.Request, u User) error {
	switch r.Method {
	case "GET":
		p, err := a.readProfile(r, u)
		if err != nil {
			return err
		}
		respond(w, 200, p)
		return nil
	case "PATCH":
	default:
		fail(w, 405, "METHOD_NOT_ALLOWED", "操作不支持")
		return nil
	}
	// Unknown fields (role, teamId, subject, …) are rejected by decode.
	var in struct {
		Name    string `json:"name"`
		Reason  string `json:"reason"`
		Version int    `json:"version"`
	}
	if !decode(w, r, &in) {
		return nil
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 || digitsOnly.MatchString(in.Name) || !identityText(in.Name) {
		failField(w, 400, "PROFILE_NAME_INVALID", "请填写你的真实姓名（1–64 字，不能只包含数字）", "name")
		return nil
	}
	if utf8.RuneCountInString(in.Reason) > 200 {
		failField(w, 400, "REASON_TOO_LONG", "说明不能超过 200 字", "reason")
		return nil
	}
	if in.Version < 1 {
		failField(w, 400, "INVALID_INPUT", "请刷新后重试", "version")
		return nil
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Same lock order as business writes: identity changes cannot interleave.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, identityLock); err != nil {
		return err
	}
	var version int
	err = tx.QueryRow(ctx, `SELECT confirm_own_profile($1,$2,$3,$4)`, u.Subject, in.Name, in.Reason, in.Version).Scan(&version)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "BB012":
			fail(w, 409, "VERSION_CONFLICT", "资料已在其他页面更新，请刷新后重试")
			return nil
		case "BB010":
			failField(w, 400, "PROFILE_NAME_INVALID", "请填写你的真实姓名（1–64 字，不能只包含数字）", "name")
			return nil
		case "BB011":
			fail(w, 403, "BUSINESS_PERMISSION_DENIED", "账号状态已变化，请重新登录")
			return nil
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return problem(403, "BUSINESS_PERMISSION_DENIED")
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	p, err := a.readProfile(r, u)
	if err != nil {
		return err
	}
	respond(w, 200, p)
	return nil
}
