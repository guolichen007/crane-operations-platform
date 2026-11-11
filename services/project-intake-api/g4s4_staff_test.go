package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// G4-S4 C1: phone normalization (unit, no database).
func TestG4S4PhoneNormalization(t *testing.T) {
	for raw, want := range map[string]string{
		"13800001111":       "+8613800001111",
		"138 0000 1111":     "+8613800001111",
		"138-0000-1111":     "+8613800001111",
		"8613800001111":     "+8613800001111",
		"008613800001111":   "+8613800001111",
		"+86 138 0000 1111": "+8613800001111",
		"+85291234567":      "+85291234567",
		"+1 (415) 555-0100": "+14155550100",
	} {
		got, err := normalizedPhone(raw)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "1380000111", "12800001111", "+8612800001111", "+86138000011112",
		"0138000011111", "abc", "+0123456789", "+1234567", "+1234567890123456", "13800001111 ext 2"} {
		if got, err := normalizedPhone(raw); err == nil {
			t.Errorf("%q accepted as %q", raw, got)
		}
	}
	if maskedPhone("+8613800001111") != "+86138****1111" {
		t.Fatal(maskedPhone("+8613800001111"))
	}
}

// G4-S4 A2/C: phone required for new members, email optional, field-specific
// errors, duplicate checks, masking, and reauth only for sensitive operations.
func TestDatabaseG4S4StaffProfile(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	app := &App{db: db, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	admin := login(t, server, "synthetic-admin")
	manager := login(t, server, "synthetic-manager")
	const base = "/api/v1/staff"
	code := func(want int, wantCode string, status int, body []byte) {
		t.Helper()
		expect(t, status, want, body)
		if wantCode != "" && !strings.Contains(string(body), `"code":"`+wantCode+`"`) {
			t.Fatalf("want %s: %s", wantCode, body)
		}
	}
	sales := staffProfileInput{Name: "合成无邮箱销售", Phone: "139 1000 0001", Role: salesRole, TeamID: syntheticTeam}

	t.Run("field_errors", func(t *testing.T) {
		for _, tc := range []struct {
			mutate func(*staffProfileInput)
			code   string
		}{
			{func(in *staffProfileInput) { in.Phone = "" }, "STAFF_PHONE_REQUIRED"},
			{func(in *staffProfileInput) { in.Phone = "12345" }, "STAFF_PHONE_INVALID"},
			{func(in *staffProfileInput) { in.Email = "not-an-email" }, "STAFF_EMAIL_INVALID"},
			{func(in *staffProfileInput) { in.Name = "12345" }, "STAFF_NAME_INVALID"},
			{func(in *staffProfileInput) { in.Name = " " }, "STAFF_NAME_INVALID"},
			{func(in *staffProfileInput) { in.TeamID = "" }, "STAFF_TEAM_REQUIRED"},
		} {
			in := sales
			tc.mutate(&in)
			s, b := admin.call("POST", base+"/invitations", in)
			code(400, tc.code, s, b)
		}
	})

	var noEmail staffMember
	t.Run("email_optional_default_reason", func(t *testing.T) {
		s, b := admin.call("POST", base+"/invitations", sales)
		noEmail = memberFrom(t, s, b)
		if noEmail.Email != "" || noEmail.Phone != "+8613910000001" || noEmail.Status != staffPending {
			t.Fatalf("%+v", noEmail)
		}
		var email any
		if err := db.QueryRow(ctx, `SELECT work_email FROM app_users WHERE staff_id=$1`, noEmail.ID).Scan(&email); err != nil || email != nil {
			t.Fatal("empty email must be stored as NULL", email, err)
		}
		var reason string
		if err := db.QueryRow(ctx, `SELECT reason FROM staff_audit WHERE target_id=$1 AND action='staff.invite'`, noEmail.ID).Scan(&reason); err != nil || reason != "新增成员" {
			t.Fatal("default audit reason", reason, err)
		}
	})

	t.Run("duplicates_and_db_constraint", func(t *testing.T) {
		dup := sales
		dup.Name, dup.Phone = "合成重复手机", "+86 139 1000 0001"
		s, b := admin.call("POST", base+"/invitations", dup)
		code(409, "STAFF_PHONE_DUPLICATE", s, b)
		withEmail := sales
		withEmail.Name, withEmail.Phone, withEmail.Email = "合成有邮箱", "13910000002", "g4s4-dup@example.test"
		s, b = admin.call("POST", base+"/invitations", withEmail)
		memberFrom(t, s, b)
		withEmail.Name, withEmail.Phone = "合成重复邮箱", "13910000003"
		s, b = admin.call("POST", base+"/invitations", withEmail)
		code(409, "STAFF_EMAIL_DUPLICATE", s, b)
		// The database itself refuses non-canonical phones (direct SQL bypass).
		if _, err := db.Exec(ctx, `UPDATE app_users SET work_phone='13910000009' WHERE staff_id=$1`, noEmail.ID); err == nil {
			t.Fatal("non-canonical phone accepted by the database")
		}
		if _, err := db.Exec(ctx, `UPDATE app_users SET work_phone='+8612910000009' WHERE staff_id=$1`, noEmail.ID); err == nil {
			t.Fatal("invalid mainland mobile accepted by the database")
		}
	})

	t.Run("list_masks_phone_detail_shows_it_search_by_phone", func(t *testing.T) {
		s, b := admin.call("GET", base+"?q=1391000", nil)
		expect(t, s, 200, b)
		if strings.Contains(string(b), "+8613910000001") || !strings.Contains(string(b), "+86139****0001") {
			t.Fatal("list must mask phones", string(b))
		}
		s, b = admin.call("GET", base+"/"+noEmail.ID, nil)
		expect(t, s, 200, b)
		if !strings.Contains(string(b), "+8613910000001") {
			t.Fatal("detail must show the full phone to an authorized manager", string(b))
		}
		// A manager outside the member's scope sees nothing at all.
		other := login(t, server, "synthetic-manager-other")
		s, b = other.call("GET", base+"/"+noEmail.ID, nil)
		expect(t, s, 404, b)
	})

	t.Run("reauth_only_for_sensitive_operations", func(t *testing.T) {
		if _, err := db.Exec(ctx, `UPDATE staff_sessions SET reauthenticated_at=now()-interval '10 minutes'`); err != nil {
			t.Fatal(err)
		}
		// Granting an advanced role at creation: requires reauth.
		lead := staffProfileInput{Name: "合成新主管", Phone: "13910000004", Role: managerRole, TeamID: syntheticTeam, Reason: "任命"}
		s, b := admin.call("POST", base+"/invitations", lead)
		code(403, "STAFF_REAUTH_REQUIRED", s, b)
		// Ordinary edit of an existing manager's name/phone/note: no reauth.
		var leadID string
		var version int
		if err := db.QueryRow(ctx, `SELECT staff_id::text,staff_version FROM app_users WHERE subject='synthetic-manager'`).Scan(&leadID, &version); err != nil {
			t.Fatal(err)
		}
		edit := staffProfileInput{Name: "示例主管", Phone: "13910000005", Role: managerRole, TeamID: syntheticTeam, Note: "日常资料", Reason: "补全电话", Version: version}
		s, b = admin.call("PATCH", base+"/"+leadID, edit)
		if m := memberFrom(t, s, b); m.Phone != "+8613910000005" || m.Status != staffActive {
			t.Fatalf("ordinary edit must not need reauth or re-provisioning: %+v", m)
		}
		// Changing the role of a manager: requires reauth (no front-end bypass).
		edit.Role, edit.Version = salesRole, version+1
		s, b = admin.call("PATCH", base+"/"+leadID, edit)
		code(403, "STAFF_REAUTH_REQUIRED", s, b)
		// Sales permission change by an administrator: ordinary, no reauth.
		var salesID string
		if err := db.QueryRow(ctx, `SELECT staff_id::text,staff_version FROM app_users WHERE subject='synthetic-b'`).Scan(&salesID, &version); err != nil {
			t.Fatal(err)
		}
		s, b = admin.call("PATCH", base+"/"+salesID+"/permissions", staffMutation{Version: version, Reason: "日常调整", Permissions: []string{"project.create", "project.submit"}})
		memberFrom(t, s, b)
		// A manager cannot even see another manager record (scope hides it), so no edit.
		s, b = manager.call("PATCH", base+"/"+leadID, edit)
		expect(t, s, 404, b)
		// Detail exposes reauth state only as a hint; null when stale.
		s, b = admin.call("GET", base+"/"+leadID, nil)
		expect(t, s, 200, b)
		var detail struct {
			RecentReauth    bool       `json:"recentReauth"`
			ReauthExpiresAt *time.Time `json:"reauthExpiresAt"`
		}
		if err := json.Unmarshal(b, &detail); err != nil || detail.RecentReauth || detail.ReauthExpiresAt != nil {
			t.Fatal("stale reauth must not be reported", string(b))
		}
		// Fresh proof: the same invitation is accepted and the window is reported.
		if _, err := db.Exec(ctx, `UPDATE staff_sessions SET reauthenticated_at=now()`); err != nil {
			t.Fatal(err)
		}
		s, b = admin.call("POST", base+"/invitations", lead)
		memberFrom(t, s, b)
		s, b = admin.call("GET", base+"/"+leadID, nil)
		if err := json.Unmarshal(b, &detail); err != nil || !detail.RecentReauth || detail.ReauthExpiresAt == nil ||
			time.Until(*detail.ReauthExpiresAt) > 5*time.Minute || time.Until(*detail.ReauthExpiresAt) < 4*time.Minute {
			t.Fatal("fresh reauth window", string(b))
		}
		// A future reauth time (forged clock) is never accepted as fresh.
		future := time.Now().Add(10 * time.Minute)
		if recentReauth(&future) {
			t.Fatal("future reauth accepted")
		}
	})

	t.Run("legacy_member_without_phone_can_be_edited_but_not_cleared", func(t *testing.T) {
		var id string
		var version int
		if err := db.QueryRow(ctx, `SELECT staff_id::text,staff_version FROM app_users WHERE subject='synthetic-c'`).Scan(&id, &version); err != nil {
			t.Fatal(err)
		}
		edit := staffProfileInput{Name: "示例销售 C", Email: "synthetic-c@example.test", Role: salesRole, TeamID: otherSyntheticTeam, Reason: "历史成员资料", Version: version}
		s, b := admin.call("PATCH", base+"/"+id, edit)
		m := memberFrom(t, s, b)
		edit.Phone, edit.Version = "13910000006", m.Version
		s, b = admin.call("PATCH", base+"/"+id, edit)
		m = memberFrom(t, s, b)
		edit.Phone, edit.Version = "", m.Version
		s, b = admin.call("PATCH", base+"/"+id, edit)
		code(400, "STAFF_PHONE_REQUIRED", s, b)
	})
}

// G4-S4 B2/C2: administrator review, no-email provisioning bound by subject and
// the exact client role, idempotent receipts, and refusals.
func TestDatabaseG4S4NoEmailProvisioning(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	app := &App{db: db, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	admin := login(t, server, "synthetic-admin")
	manager := login(t, server, "synthetic-manager")
	const subject = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	in := staffProfileInput{Name: "合成无邮箱开通", Phone: "13920000001", Role: salesRole, TeamID: syntheticTeam, Reason: "新人入职"}
	s, b := manager.call("POST", "/api/v1/staff/invitations", in)
	m := memberFrom(t, s, b)
	path := "/api/v1/staff/" + m.ID

	review := provisionReviewInput{Version: m.Version, Reason: "核对入职名单", Subject: subject, Username: "g4s4.sales"}
	s, b = manager.call("POST", path+"/provision-review", review)
	expect(t, s, 403, b) // only administrators review provisioning
	stale := review
	stale.Version++
	s, b = admin.call("POST", path+"/provision-review", stale)
	expect(t, s, 409, b)
	bad := review
	bad.Subject = "synthetic-a"
	s, b = admin.call("POST", path+"/provision-review", bad)
	expect(t, s, 400, b)
	taken := review
	if err := db.QueryRow(ctx, `SELECT subject FROM app_users WHERE subject LIKE '%-%-%-%-%' AND staff_id<>$1 LIMIT 1`, m.ID).Scan(&taken.Subject); err == nil {
		s, b = admin.call("POST", path+"/provision-review", taken)
		expect(t, s, 409, b)
	}
	s, b = admin.call("POST", path+"/provision-review", review)
	expect(t, s, 200, b)
	var reviewed struct {
		Member staffMember     `json:"member"`
		Draft  json.RawMessage `json:"draft"`
	}
	if err := json.Unmarshal(b, &reviewed); err != nil || !reviewed.Member.ProvisionReviewed || reviewed.Member.Status != staffPending ||
		reviewed.Member.IdentityLinked || reviewed.Member.IdentityVerified {
		t.Fatal("business review must not bind or verify identity", string(b))
	}
	var draft map[string]any
	if err := json.Unmarshal(reviewed.Draft, &draft); err != nil || draft["draftStatus"] != "PENDING_IDENTITY_VERIFICATION" ||
		draft["identityVerified"] != false || draft["subjectClaimed"] != subject || draft["subject"] != nil {
		t.Fatal("review result must be an unverified draft", string(reviewed.Draft))
	}
	// The draft as produced, or with only the identity flag flipped, never passes staff-provision.
	if _, err := applyProvisionReceipt(ctx, db, reviewed.Draft, "synthetic-operator", true); err == nil {
		t.Fatal("unverified review draft accepted by staff-provision")
	}
	forged := map[string]any{}
	for k, v := range draft {
		if k != "draftStatus" && k != "businessApprovalBy" && k != "subjectClaimed" && k != "identityChecklist" {
			forged[k] = v
		}
	}
	forged["subject"], forged["identityVerified"] = subject, true
	if _, err := applyProvisionReceipt(ctx, db, mustJSON(forged), "synthetic-operator", true); err == nil {
		t.Fatal("draft with self-asserted identityVerified and the review reference accepted")
	}
	// Identity operations verify the Keycloak user independently and write their own receipt.
	receipt := provisionReceipt{StaffID: m.ID, Version: m.Version, Operation: "approve", Subject: subject,
		Phone: "+8613920000001", IdentityVerified: true, ClientRoles: []string{salesRole},
		ApprovalReference: "IDOPS-SYNTHETIC-0001", Reason: "运维已在 Keycloak 核对 UUID、唯一角色与归属"}

	reject := func(r provisionReceipt, why string) {
		t.Helper()
		if _, err := applyProvisionReceipt(ctx, db, mustJSON(r), "synthetic-operator", false); err == nil {
			t.Fatal("accepted: " + why)
		}
	}
	noRole := receipt
	noRole.ClientRoles = nil
	reject(noRole, "no-email receipt without explicit exact client role")
	wrongRole := receipt
	wrongRole.ClientRoles = []string{managerRole}
	reject(wrongRole, "wrong client role")
	twoRoles := receipt
	twoRoles.ClientRoles = []string{salesRole, managerRole}
	reject(twoRoles, "multiple client roles")
	claimed := receipt
	claimed.UniqueEmail = true
	reject(claimed, "uniqueEmail claimed without an email")
	phone := receipt
	phone.Phone = "+8613920000002"
	reject(phone, "phone mismatch")
	unverified := receipt
	unverified.IdentityVerified = false
	reject(unverified, "identityVerified=false")
	oldVersion := receipt
	oldVersion.Version--
	reject(oldVersion, "stale version")
	reviewRef := receipt
	reviewRef.ApprovalReference = reviewReferencePrefix + "ANY-V1"
	reject(reviewRef, "business review reference instead of identity verification")
	duplicate := receipt
	duplicate.Subject = "dddddddd-0000-4000-8000-000000000001"
	if _, err := db.Exec(ctx, `INSERT INTO app_users(subject,display_name,role,team_id) VALUES($1,'合成已绑定成员','baobei_sales',$2)`, duplicate.Subject, syntheticTeam); err != nil {
		t.Fatal(err)
	}
	reject(duplicate, "subject already bound to another member")

	raw := mustJSON(receipt)
	if got, err := applyProvisionReceipt(ctx, db, raw, "synthetic-operator", true); err != nil || got != "DRY_RUN_"+staffActivation {
		t.Fatal(got, err)
	}
	if got, err := applyProvisionReceipt(ctx, db, raw, "synthetic-operator", false); err != nil || got != staffActivation {
		t.Fatal(got, err)
	}
	if got, err := applyProvisionReceipt(ctx, db, raw, "synthetic-operator", false); err != nil || got != "ALREADY_APPLIED" {
		t.Fatal("replay must be idempotent", got, err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM app_users WHERE subject=$1`, subject).Scan(&count); err != nil || count != 1 {
		t.Fatal("exactly one bound account", count, err)
	}

	// Activation without email: exact single client role is mandatory.
	activate := func(roles []string) error {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		_, err = authorizeOIDCSession(ctx, tx, subject, loginEmail{}, roles, true)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return err
	}
	if err := activate([]string{salesRole, managerRole}); err != loginRoleMissing {
		t.Fatal("multiple roles must not activate a no-email member", err)
	}
	if err := activate([]string{managerRole}); err != loginRoleMissing {
		t.Fatal("wrong role", err)
	}
	if err := activate([]string{salesRole}); err != nil {
		t.Fatal("exact subject + exact role must activate", err)
	}
	s, b = admin.call("GET", path, nil)
	var detail struct{ Member staffMember }
	if err := json.Unmarshal(b, &detail); err != nil || detail.Member.Status != staffActive || !detail.Member.IdentityVerified {
		t.Fatal("no-email member must become active and verified", string(b))
	}
	// A member with an email still requires the verified matching email.
	withEmail := staffProfileInput{Name: "合成有邮箱开通", Phone: "13920000003", Email: "g4s4-mail@example.test", Role: salesRole, TeamID: syntheticTeam}
	s, b = admin.call("POST", "/api/v1/staff/invitations", withEmail)
	e := memberFrom(t, s, b)
	const mailSubject = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	mailReceipt := provisionReceipt{StaffID: e.ID, Version: e.Version, Operation: "approve", Subject: mailSubject, Email: e.Email,
		IdentityVerified: true, UniqueEmail: true, ApprovalReference: "SYNTHETIC-G4S4", Reason: "合成"}
	if got, err := applyProvisionReceipt(ctx, db, mustJSON(mailReceipt), "synthetic-operator", false); err != nil || got != staffActivation {
		t.Fatal(got, err)
	}
	tx, _ := db.Begin(ctx)
	_, err := authorizeOIDCSession(ctx, tx, mailSubject, loginEmail{address: "other@example.test", verified: true}, []string{salesRole}, true)
	tx.Rollback(ctx)
	if err != loginEmailMismatch {
		t.Fatal("email members keep the email check", err)
	}
}
