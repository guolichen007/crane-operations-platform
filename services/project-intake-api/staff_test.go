package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const syntheticTeam = "11111111-1111-4111-8111-111111111111"
const otherSyntheticTeam = "22222222-2222-4222-8222-222222222222"

func memberFrom(t *testing.T, status int, body []byte) staffMember {
	t.Helper()
	expect(t, status, 200, body)
	var member staffMember
	if err := json.Unmarshal(body, &member); err != nil || member.ID == "" {
		t.Fatalf("member response: %s", body)
	}
	return member
}

func TestDatabaseStaffGovernance(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	app := &App{db: db, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	manager := login(t, server, "synthetic-manager")
	other := login(t, server, "synthetic-manager-other")
	director := login(t, server, "synthetic-director")
	admin := login(t, server, "synthetic-admin")
	sales := login(t, server, "synthetic-a")
	staffID := func(subject string) string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `SELECT staff_id::text FROM app_users WHERE subject=$1`, subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	const base = "/api/v1/staff"
	status, body := sales.call("GET", base, nil)
	expect(t, status, 403, body)
	status, body = manager.call("GET", base+"?sort=projects", nil)
	expect(t, status, 200, body)
	if strings.Contains(string(body), "示例销售 C") || strings.Contains(string(body), "示例主管") {
		t.Fatal("scope leak", string(body))
	}
	status, body = manager.call("GET", base+"?page=2", nil)
	expect(t, status, 200, body)
	if !strings.Contains(string(body), `"items":[]`) {
		t.Fatal("pagination", string(body))
	}
	invite := staffProfileInput{Name: "合成邀请", Email: "Invitation@Example.Test", Role: salesRole, TeamID: syntheticTeam, Reason: "合成审批"}
	status, body = manager.call("POST", base+"/invitations", invite)
	invited := memberFrom(t, status, body)
	if invited.Status != staffPending || invited.Email != "invitation@example.test" || len(invited.Permissions) != 3 || invited.SyncStatus != "PENDING" {
		t.Fatal(invited)
	}
	status, body = manager.call("POST", base+"/invitations", invite)
	expect(t, status, 409, body)
	invite.Email = "another@example.test"
	invite.TeamID = otherSyntheticTeam
	status, body = manager.call("POST", base+"/invitations", invite)
	expect(t, status, 403, body)
	invite.TeamID = syntheticTeam
	invite.Role = managerRole
	status, body = manager.call("POST", base+"/invitations", invite)
	expect(t, status, 403, body)
	for _, subject := range []string{"synthetic-manager", "synthetic-c", "synthetic-director", "synthetic-admin"} {
		status, body = manager.call("POST", base+"/"+staffID(subject)+"/suspend", staffMutation{Version: 1, Reason: "禁止越权"})
		expect(t, status, 404, body)
	}
	status, body = other.call("GET", base+"/"+invited.ID+"/audit", nil)
	expect(t, status, 404, body)
	status, body = director.call("GET", base+"/"+invited.ID, nil)
	expect(t, status, 200, body)
	status, body = manager.call("PATCH", base+"/"+invited.ID, invite)
	expect(t, status, 409, body)
	invite.Version = invited.Version
	status, body = manager.call("PATCH", base+"/"+invited.ID, invite)
	expect(t, status, 403, body)
	status, body = manager.call("PATCH", base+"/"+invited.ID+"/permissions", map[string]any{"version": 1, "reason": "合成", "permissions": []string{"project.read_all"}})
	expect(t, status, 400, body)
	status, body = manager.call("PATCH", base+"/"+invited.ID+"/permissions", map[string]any{"version": 1, "reason": "合成", "permissions": []string{}, "actor": "synthetic-admin"})
	expect(t, status, 400, body)
	status, body = manager.call("PATCH", base+"/"+invited.ID+"/permissions", map[string]any{"version": 1, "reason": "合成", "permissions": []string{}})
	invited = memberFrom(t, status, body)
	if invited.Version != 2 || len(invited.Permissions) != 0 {
		t.Fatal(invited)
	}
	status, body = manager.call("PATCH", base+"/"+invited.ID+"/permissions", map[string]any{"version": 1, "reason": "合成", "permissions": []string{}})
	expect(t, status, 409, body)

	// A normal sales session is subject to all three current capabilities.
	status, body = sales.call("POST", "/api/v1/projects", sample("Synthetic staff handoff"))
	p := projectFrom(t, status, body)
	status, body = sales.call("POST", "/api/v1/projects", sample("Synthetic staff conflict"))
	q := projectFrom(t, status, body)
	id := staffID("synthetic-a")
	status, body = manager.call("PATCH", base+"/"+id+"/permissions", map[string]any{"version": 1, "reason": "收回权限", "permissions": []string{}})
	changed := memberFrom(t, status, body)
	for _, path := range []string{"/api/v1/projects", "/api/v1/projects/" + p.ID + "/submit", "/api/v1/projects/" + p.ID + "/activities"} {
		status, body = sales.call("POST", path, sample("Synthetic denied"))
		expect(t, status, 403, body)
	}
	status, body = manager.call("PATCH", base+"/"+id+"/permissions", staffMutation{Version: changed.Version, Reason: "恢复能力", Permissions: capabilityNames})
	changed = memberFrom(t, status, body)
	status, body = sales.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
	p = projectFrom(t, status, body)
	status, body = manager.call("POST", "/api/v1/projects/"+p.ID+"/approve", map[string]any{"version": p.Version, "reason": "合成审批"})
	p = projectFrom(t, status, body)

	req := httptest.NewRequest("POST", server.URL+"/api/v1/projects", nil)
	uri, _ := url.Parse(server.URL)
	for _, cookie := range sales.client.Jar.Cookies(uri) {
		req.AddCookie(cookie)
	}
	stale, err := app.user(req)
	if err != nil {
		t.Fatal(err)
	}
	write, err := app.beginBusiness(ctx, stale, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer write.Rollback(ctx)
	done := make(chan struct {
		status int
		body   []byte
	}, 1)
	go func() {
		status, body := manager.call("POST", base+"/"+id+"/suspend", staffMutation{Version: changed.Version, Reason: "离职交接"})
		done <- struct {
			status int
			body   []byte
		}{status, body}
	}()
	select {
	case <-done:
		t.Fatal("suspension bypassed an in-flight business transaction")
	case <-time.After(120 * time.Millisecond):
	}
	if err = write.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		changed = memberFrom(t, result.status, result.body)
	case <-time.After(8 * time.Second):
		t.Fatal("suspension did not complete")
	}
	if changed.Status != staffSuspended || changed.SyncStatus != "PENDING" {
		t.Fatal(changed)
	}
	status, body = sales.call("GET", "/api/v1/me", nil)
	expect(t, status, 401, body)
	if tx, err := app.beginBusiness(ctx, stale, 1); err == nil {
		tx.Rollback(ctx)
		t.Fatal("old session wrote after suspension")
	}
	status, body = manager.call("GET", base+"/"+id, nil)
	expect(t, status, 200, body)
	if !strings.Contains(string(body), `"queued":true`) {
		t.Fatal("missing durable handoff")
	}
	status, body = admin.call("GET", base+"/"+id, nil)
	expect(t, status, 200, body)
	if strings.Contains(string(body), p.ProjectName) {
		t.Fatal("admin received project content")
	}

	transfer := map[string]any{"version": changed.Version, "targetId": staffID("synthetic-c"), "reason": "合成移交", "projects": []map[string]any{
		{"id": p.ID, "version": p.Version}, {"id": q.ID, "version": q.Version + 1},
	}}
	status, body = manager.call("POST", base+"/"+id+"/ownership-transfer-batch", transfer)
	expect(t, status, 404, body)
	transfer["targetId"] = staffID("synthetic-b")
	status, body = manager.call("POST", base+"/"+id+"/ownership-transfer-batch", transfer)
	expect(t, status, 200, body)
	if !strings.Contains(string(body), "TRANSFERRED") || !strings.Contains(string(body), "VERSION_CONFLICT") {
		t.Fatal(string(body))
	}
	status, body = manager.call("POST", base+"/"+id+"/ownership-transfer-batch", transfer)
	expect(t, status, 200, body)
	if !strings.Contains(string(body), "ALREADY_TRANSFERRED") {
		t.Fatal(string(body))
	}
	transfer["targetId"] = staffID("synthetic-c")
	transfer["projects"] = []map[string]any{{"id": q.ID, "version": q.Version}}
	status, body = director.call("POST", base+"/"+id+"/ownership-transfer-batch", transfer)
	expect(t, status, 200, body)
	var newTeam string
	if err = db.QueryRow(ctx, `SELECT team_id::text FROM intake_projects WHERE id=$1 AND owner_subject='synthetic-c' AND created_by_subject='synthetic-a'`, q.ID).Scan(&newTeam); err != nil ||
		newTeam != otherSyntheticTeam || !strings.Contains(string(body), "TRANSFERRED") {
		t.Fatal(newTeam, string(body), err)
	}
	var creator, owner, state string
	var history int
	if err = db.QueryRow(ctx, `SELECT created_by_subject,owner_subject,intake_status,
  (SELECT count(*) FROM ownership_history WHERE project_id=$1 AND from_subject='synthetic-a')
  FROM intake_projects WHERE id=$1`, p.ID).Scan(&creator, &owner, &state, &history); err != nil ||
		creator != "synthetic-a" || owner != "synthetic-b" || state != "APPROVED" || history != 1 {
		t.Fatal(creator, owner, state, history, err)
	}
	status, body = manager.call("POST", base+"/"+id+"/reactivate", staffMutation{Version: changed.Version, Reason: "重新入职"})
	changed = memberFrom(t, status, body)
	if changed.Status != staffPending {
		t.Fatal(changed)
	}
	status, body = sales.call("POST", "/auth/dev", map[string]string{"subject": "synthetic-a"})
	expect(t, status, 403, body)
	status, body = manager.call("GET", base+"/"+id+"/audit", nil)
	expect(t, status, 200, body)
	if !strings.Contains(string(body), "离职交接") || !strings.Contains(string(body), "合成移交") {
		t.Fatal("missing personnel audit")
	}

	status, body = manager.call("POST", "/api/v1/teams", map[string]any{"name": "禁止", "reason": "合成"})
	expect(t, status, 403, body)
	status, body = admin.call("POST", "/api/v1/teams", map[string]any{"name": "合成新团队", "reason": "创建团队"})
	expect(t, status, 200, body)
	var team staffTeam
	if err = json.Unmarshal(body, &team); err != nil {
		t.Fatal(err)
	}
	status, body = admin.call("PATCH", "/api/v1/teams/"+team.ID, map[string]any{"name": "合成改名", "reason": "名称调整", "version": team.Version})
	expect(t, status, 200, body)
	status, body = admin.call("PATCH", "/api/v1/teams/"+team.ID, map[string]any{"name": "过期更新", "reason": "合成", "version": team.Version})
	expect(t, status, 409, body)
	status, body = admin.call("POST", base+"/"+staffID("synthetic-admin")+"/suspend", staffMutation{Version: 1, Reason: "最后管理员"})
	expect(t, status, 403, body)
	_, err = db.Exec(ctx, `UPDATE app_users SET active=false WHERE subject='synthetic-admin'`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "BB001" {
		t.Fatal("last admin database guard", err)
	}
	// A normal admin session without the separate recent proof cannot grant a role.
	if _, err = db.Exec(ctx, `UPDATE staff_sessions SET reauthenticated_at=NULL WHERE subject='synthetic-admin'`); err != nil {
		t.Fatal(err)
	}
	status, body = admin.call("POST", base+"/invitations", staffProfileInput{Name: "合成主管", Email: "new-manager@example.test", Role: managerRole, TeamID: syntheticTeam, Reason: "高级角色审批"})
	expect(t, status, 403, body)
	if !strings.Contains(string(body), "STAFF_REAUTH_REQUIRED") {
		t.Fatal(string(body))
	}
}

func TestStaffReauthenticationTime(t *testing.T) {
	now := time.Now()
	until := now.Add(5 * time.Minute)
	if reauthenticationTime("reauth-synthetic", now.Unix(), until) == nil ||
		reauthenticationTime("normal", now.Unix(), until) != nil ||
		reauthenticationTime("reauth-synthetic", now.Add(-time.Minute).Unix(), until) != nil {
		t.Fatal("explicit fresh OIDC authentication required")
	}
}
