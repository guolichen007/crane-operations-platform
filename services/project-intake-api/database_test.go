package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5/pgxpool"
)

type testClient struct {
	client       *http.Client
	origin, csrf string
	t            *testing.T
}

func (c testClient) call(method, path string, body any) (int, []byte) {
	c.t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, e := http.NewRequest(method, c.origin+path, bytes.NewReader(b))
	if e != nil {
		c.t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin)
	req.Header.Set("X-CSRF-Token", c.csrf)
	resp, e := c.client.Do(req)
	if e != nil {
		c.t.Fatal(e)
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(resp.Body)
	if e != nil {
		c.t.Fatal(e)
	}
	return resp.StatusCode, raw
}
func expect(t *testing.T, got, want int, b []byte) {
	t.Helper()
	if got != want {
		t.Fatalf("HTTP %d want %d: %s", got, want, b)
	}
}
func projectFrom(t *testing.T, status int, b []byte) Project {
	t.Helper()
	expect(t, status, 200, b)
	var p Project
	if e := json.Unmarshal(b, &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func login(t *testing.T, server *httptest.Server, subject string) testClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := testClient{&http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, server.URL, "", t}
	s, b := c.call("POST", "/auth/dev", map[string]string{"subject": subject})
	expect(t, s, 200, b)
	s, b = c.call("GET", "/api/v1/me", nil)
	expect(t, s, 200, b)
	var u User
	_ = json.Unmarshal(b, &u)
	c.csrf = u.CSRF
	return c
}
func TestDatabase(t *testing.T) {
	dsn := os.Getenv("BAOBEI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOT_RUN: BAOBEI_TEST_DATABASE_URL required")
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" {
		t.Fatal("test DB must be local loopback")
	}
	ctx := context.Background()
	admin, e := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := "baobei_test_" + hash(randomToken())[:16]
	if !regexp.MustCompile(`^baobei_test_[a-f0-9]{16}$`).MatchString(schema) {
		t.Fatal("unsafe schema")
	}
	if _, e = admin.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_, err := admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		if err != nil {
			t.Error(err)
		}
	}()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = migrate(ctx, db); e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db); e != nil {
		t.Fatal("idempotent migration", e)
	}
	if e = seed(ctx, db); e != nil {
		t.Fatal(e)
	}
	var trgm float64
	if e = db.QueryRow(ctx, `SELECT similarity('synthetic','synthetic')`).Scan(&trgm); e != nil || trgm != 1 {
		t.Fatal("pg_trgm", e)
	}
	a, e := newApp(ctx, db, Config{Env: "development", Dev: true})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	a.cfg.Origin = server.URL
	ca, cb := login(t, server, "synthetic-a"), login(t, server, "synthetic-b")
	t.Run("authentication-csrf", func(t *testing.T) {
		req, _ := http.NewRequest("POST", server.URL+"/api/v1/projects", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		r, err := ca.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 403 {
			t.Fatal(r.StatusCode)
		}
		req, _ = http.NewRequest("GET", server.URL+"/api/v1/me", nil)
		r, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal(r.StatusCode)
		}
		s, b := ca.call("POST", "/api/v1/projects", map[string]any{"ownerSubject": "synthetic-b"})
		expect(t, s, 400, b)
	})
	var p Project
	t.Run("draft-edit-submit-ownership-version", func(t *testing.T) {
		s, b := ca.call("POST", "/api/v1/projects", Input{})
		p = projectFrom(t, s, b)
		if p.OwnerSubject != "synthetic-a" || p.CreatedBySubject != "synthetic-a" || !regexp.MustCompile(`^BB-\d{4}-\d{6}$`).MatchString(p.RegistrationNo) {
			t.Fatal("ownership or number", p)
		}
		s, b = ca.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
		expect(t, s, 400, b)
		in := sample("合成闭环项目")
		in.Version = p.Version
		s, b = ca.call("PATCH", "/api/v1/projects/"+p.ID, in)
		p = projectFrom(t, s, b)
		if p.Version != 2 {
			t.Fatal(p.Version)
		}
		s, b = ca.call("PATCH", "/api/v1/projects/"+p.ID, in)
		expect(t, s, 409, b)
		s, b = ca.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
		p = projectFrom(t, s, b)
		if p.IntakeStatus != "SUBMITTED" {
			t.Fatal(p.IntakeStatus)
		}
		s, b = ca.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		expect(t, s, 409, b)
	})
	t.Run("sales-isolation-guess-uuid", func(t *testing.T) {
		for _, path := range []string{"", "/duplicates"} {
			s, b := cb.call("GET", "/api/v1/projects/"+p.ID+path, nil)
			expect(t, s, 404, b)
		}
		s, b := cb.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		expect(t, s, 404, b)
		s, b = cb.call("POST", "/api/v1/projects/"+p.ID+"/activities", Activity{Version: p.Version})
		expect(t, s, 404, b)
		s, b = cb.call("GET", "/api/v1/projects?q=合成闭环项目", nil)
		expect(t, s, 200, b)
		var out struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(b, &out)
		if out.Total != 0 {
			t.Fatal("leaked project")
		}
		s, b = cb.call("POST", "/api/v1/projects/"+p.ID+"/approve", map[string]int{"version": p.Version})
		expect(t, s, 403, b)
	})
	t.Run("followup-transaction-and-occ", func(t *testing.T) {
		act := Activity{Version: p.Version, Type: "PHONE", OccurredAt: time.Now().UTC(), Summary: "Synthetic 电话沟通", Result: "需求确认", NextAction: "准备 synthetic 方案", NextFollowupAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}
		s, b := ca.call("POST", "/api/v1/projects/"+p.ID+"/activities", act)
		p = projectFrom(t, s, b)
		s, b = ca.call("POST", "/api/v1/projects/"+p.ID+"/activities", act)
		expect(t, s, 409, b)
		s, b = ca.call("GET", "/api/v1/projects/"+p.ID, nil)
		p = projectFrom(t, s, b)
		if len(p.Activities) != 1 || p.NextAction != act.NextAction || p.LastActivityAt == nil || len(p.Audit) != 6 {
			t.Fatal("followup/audit", p)
		}
		var n int
		if e = db.QueryRow(ctx, `SELECT count(*) FROM project_activities WHERE project_id=$1`, p.ID).Scan(&n); e != nil || n != 1 {
			t.Fatal("duplicate activity", n, e)
		}
	})
	t.Run("duplicate-redaction-and-alias", func(t *testing.T) {
		in := sample("合成闭环项目")
		s, b := cb.call("POST", "/api/v1/duplicate-checks", in)
		expect(t, s, 200, b)
		var out struct {
			Items []Duplicate `json:"items"`
		}
		_ = json.Unmarshal(b, &out)
		if len(out.Items) != 1 || out.Items[0].CandidateID != "" || out.Items[0].ProjectName != "" || bytes.Contains(b, []byte(p.ID)) {
			t.Fatal("privacy", string(b))
		}
		_, e = db.Exec(ctx, `INSERT INTO organization_aliases(normalized_alias,organization_id) SELECT $1,id FROM organizations WHERE normalized_name=$2`, normalize("Synthetic Alias"), normalize(in.Customer))
		if e != nil {
			t.Fatal(e)
		}
		in.Customer = "Synthetic Alias"
		s, b = cb.call("POST", "/api/v1/duplicate-checks", in)
		expect(t, s, 200, b)
		_ = json.Unmarshal(b, &out)
		if len(out.Items) != 1 {
			t.Fatal("alias not scored")
		}
	})
	t.Run("concurrent-submissions-no-double-ownership", func(t *testing.T) {
		in := sample("另一个合成并发项目")
		in.Customer = "Synthetic Concurrency"
		in.TenderNo = "SYNTHETIC-CONCURRENT-ONLY"
		s, b := ca.call("POST", "/api/v1/projects", in)
		pa := projectFrom(t, s, b)
		s, b = cb.call("POST", "/api/v1/projects", in)
		pb := projectFrom(t, s, b)
		var wg sync.WaitGroup
		results := make(chan string, 2)
		for _, pair := range []struct {
			c testClient
			p Project
		}{{ca, pa}, {cb, pb}} {
			wg.Add(1)
			go func(c testClient, p Project) {
				defer wg.Done()
				s, b := c.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": p.Version})
				var out Project
				_ = json.Unmarshal(b, &out)
				results <- fmt.Sprintf("%d:%s", s, out.IntakeStatus)
			}(pair.c, pair.p)
		}
		wg.Wait()
		close(results)
		counts := map[string]int{}
		for r := range results {
			counts[r]++
		}
		if counts["200:SUBMITTED"] != 1 || counts["200:UNDER_REVIEW"] != 1 {
			t.Fatal(counts)
		}
		if pa.RegistrationNo == pb.RegistrationNo {
			t.Fatal("number reused")
		}
	})
	t.Run("manager-team-scope", func(t *testing.T) {
		m := login(t, server, "synthetic-manager")
		s, b := m.call("GET", "/api/v1/projects/"+p.ID, nil)
		expect(t, s, 200, b)
		s, b = m.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		expect(t, s, 403, b)
		_, e = db.Exec(ctx, `UPDATE app_users SET team_id=NULL WHERE subject='synthetic-manager'`)
		if e != nil {
			t.Fatal(e)
		}
		s, b = m.call("GET", "/api/v1/projects/"+p.ID, nil)
		expect(t, s, 404, b)
	})
	t.Run("logout", func(t *testing.T) {
		s, b := ca.call("POST", "/auth/logout", map[string]bool{})
		expect(t, s, 200, b)
		s, b = ca.call("GET", "/api/v1/me", nil)
		expect(t, s, 401, b)
	})
	t.Run("oidc-real-signature-state-pkce-nonce", func(t *testing.T) { testOIDC(t, db) })
}
func testOIDC(t *testing.T, db *pgxpool.Pool) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "synthetic"))
	if e != nil {
		t.Fatal(e)
	}
	fixtureBytes, e := os.ReadFile("testdata/keycloak-id-token.synthetic.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture map[string]any
	if e = json.Unmarshal(fixtureBytes, &fixture); e != nil {
		t.Fatal(e)
	}
	tokenSubject := "synthetic-a"
	var issuer, nonce, challenge string
	badNonce := false
	roleMode := "client"
	authAge := time.Duration(0)
	signedEmail := ""
	signedVerified := false
	pkceChecked := false
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			respond(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			respond(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "synthetic", Use: "sig", Algorithm: "RS256"}}})
		case "/token":
			_ = r.ParseForm()
			h := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(h[:]) != challenge {
				respond(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			pkceChecked = true
			n := nonce
			if badNonce {
				n = "wrong"
			}
			claims := map[string]any{}
			for k, v := range fixture {
				claims[k] = v
			}
			claims["iss"] = issuer
			claims["sub"] = tokenSubject
			claims["exp"] = time.Now().Add(time.Hour).Unix()
			claims["iat"] = time.Now().Unix()
			claims["nonce"] = n
			claims["email"] = signedEmail
			claims["email_verified"] = signedVerified
			claims["auth_time"] = time.Now().Add(-authAge).Unix()
			if roleMode == "realm" {
				delete(claims, "resource_access")
				claims["realm_access"] = map[string]any{"roles": []string{"baobei_sales"}}
			}
			if roleMode == "other" {
				claims["resource_access"] = map[string]any{"other-client": map[string]any{"roles": []string{"baobei_sales"}}}
			}
			if roleMode == "admin" {
				claims["resource_access"] = map[string]any{"baobei-web": map[string]any{"roles": []string{adminRole}}}
			}
			b, _ := json.Marshal(claims)
			obj, _ := signer.Sign(b)
			token, _ := obj.CompactSerialize()
			respond(w, 200, map[string]any{"access_token": "synthetic-only", "token_type": "Bearer", "id_token": token})
		default:
			w.WriteHeader(404)
		}
	}))
	defer idp.Close()
	issuer = idp.URL
	app, e := newApp(context.Background(), db, Config{Env: "development", Issuer: issuer, ClientID: "baobei-web"})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	app.oauth.RedirectURL = server.URL + "/auth/callback"
	jar, _ := cookiejar.New(nil)
	c := testClient{&http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, server.URL, "", t}
	startLogin := func(reauth bool) string {
		path := "/auth/login"
		if reauth {
			path += "?reauth=1"
		}
		r, e := c.client.Get(server.URL + path)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		if r.StatusCode != 302 {
			t.Fatal(r.StatusCode)
		}
		u, e := url.Parse(r.Header.Get("Location"))
		if e != nil {
			t.Fatal(e)
		}
		nonce = u.Query().Get("nonce")
		challenge = u.Query().Get("code_challenge")
		if nonce == "" || challenge == "" || u.Query().Get("code_challenge_method") != "S256" {
			t.Fatal("missing nonce/PKCE")
		}
		if reauth && (u.Query().Get("prompt") != "login" || u.Query().Get("max_age") != "0") {
			t.Fatal("missing explicit Keycloak reauthentication parameters")
		}
		return u.Query().Get("state")
	}
	start := func() string { return startLogin(false) }
	state := start()
	s, b := c.call("GET", "/auth/callback?code=synthetic&state=wrong", nil)
	expect(t, s, 400, b)
	// The invalid state attempt clears the binding cookie; start a fresh flow.
	state = start()
	s, b = c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(state), nil)
	expect(t, s, 303, b)
	if !pkceChecked {
		t.Fatal("PKCE not verified")
	}
	s, b = c.call("GET", "/api/v1/me", nil)
	expect(t, s, 200, b)
	s, b = c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(state), nil)
	expect(t, s, 400, b)
	badNonce = true
	state = start()
	s, b = c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(state), nil)
	expect(t, s, 401, b)
	badNonce = false
	for _, mode := range []string{"realm", "other", "client"} {
		roleMode = mode
		state = start()
		s, b = c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(state), nil)
		want := 403
		if mode == "client" {
			want = 303
		}
		expect(t, s, want, b)
	}
	roleMode = "client"
	for _, test := range []struct {
		name, sql string
		want      int
	}{
		{"absent", "", 403},
		{"mismatch", `INSERT INTO app_users(subject,display_name,role) VALUES($1,'Synthetic','baobei_admin')`, 403},
		{"inactive", `UPDATE app_users SET role='baobei_sales',team_id='11111111-1111-4111-8111-111111111111',active=false WHERE subject=$1`, 403},
		{"matching", `UPDATE app_users SET active=true WHERE subject=$1`, 303},
	} {
		t.Run("identity_"+test.name, func(t *testing.T) {
			tokenSubject = fixture["sub"].(string)
			if test.sql != "" {
				if _, e = db.Exec(context.Background(), test.sql, tokenSubject); e != nil {
					t.Fatal(e)
				}
			}
			state = start()
			s, b = c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(state), nil)
			expect(t, s, test.want, b)
			var count int
			if e = db.QueryRow(context.Background(), `SELECT count(*) FROM app_users WHERE subject=$1`, tokenSubject).Scan(&count); e != nil {
				t.Fatal(e)
			}
			if test.name == "absent" && count != 0 {
				t.Fatal("login auto-provisioned user")
			}
		})
	}
	tokenSubject = "synthetic-a"
	state = start()
	if _, e = db.Exec(context.Background(), `UPDATE oidc_pending_logins SET expires_at=now()-interval '1 second' WHERE state_hash=$1`, hash(state)); e != nil {
		t.Fatal(e)
	}
	s, b = c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(state), nil)
	expect(t, s, 400, b)
	second, e := newApp(context.Background(), db, Config{Env: "development", Issuer: issuer, ClientID: "baobei-web"})
	if e != nil {
		t.Fatal(e)
	}
	secondServer := httptest.NewServer(second.handler())
	defer secondServer.Close()
	second.oauth.RedirectURL = app.oauth.RedirectURL
	second.cfg.Origin = server.URL
	state = start()
	c.origin = secondServer.URL
	s, b = c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(state), nil)
	expect(t, s, 303, b)
	s, b = c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(state), nil)
	expect(t, s, 400, b)
	c.origin = server.URL
	var raw int
	if e = db.QueryRow(context.Background(), `SELECT count(*) FROM oidc_pending_logins WHERE state_hash=$1 OR binding_hash=$1`, state).Scan(&raw); e != nil || raw != 0 {
		t.Fatal("raw state persisted", e)
	}
	t.Run("staff_provision_receipt_and_signed_activation", func(t *testing.T) {
		ctx := context.Background()
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		actor := User{Subject: "synthetic-manager", Role: managerRole, TeamID: syntheticTeam}
		result, err := inviteStaff(ctx, tx, actor, nil, staffProfileInput{Name: "合成已审批成员", Email: "signed-staff@example.test", Role: salesRole, TeamID: syntheticTeam, Reason: "合成开通"})
		if err != nil {
			t.Fatal(err)
		}
		m := result.(staffMember)
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		tokenSubject = "88888888-8888-4888-8888-888888888888"
		signedEmail = m.Email
		signedVerified = true
		callback := func(want int) {
			s, b := c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(start()), nil)
			expect(t, s, want, b)
		}
		callback(403)
		failed := provisionReceipt{StaffID: m.ID, Version: m.Version, Operation: "fail", Email: m.Email, ApprovalReference: "SYNTHETIC-FAILED", Reason: "未能核对"}
		if outcome, err := applyProvisionReceipt(ctx, db, mustJSON(failed), "synthetic-operator", false); err != nil || outcome != staffFailed {
			t.Fatal(outcome, err)
		}
		tx, err = db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		m, err = readStaff(ctx, tx, actor, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		result, err = mutateStaff(ctx, tx, actor, nil, m, "retry", staffMutation{Version: m.Version, Reason: "重新核对"})
		if err != nil {
			t.Fatal(err)
		}
		m = result.(staffMember)
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		receipt := provisionReceipt{StaffID: m.ID, Version: m.Version, Operation: "approve", Subject: fixture["sub"].(string), Email: m.Email,
			IdentityVerified: true, UniqueEmail: true, ApprovalReference: "SYNTHETIC-APPROVED", Reason: "已确认企业账号"}
		if _, err = applyProvisionReceipt(ctx, db, mustJSON(receipt), "synthetic-operator", false); err == nil {
			t.Fatal("existing identity rebound")
		}
		receipt.Subject = tokenSubject
		receipt.UniqueEmail = false
		if _, err = applyProvisionReceipt(ctx, db, mustJSON(receipt), "synthetic-operator", false); err == nil {
			t.Fatal("ambiguous email accepted")
		}
		receipt.UniqueEmail = true
		if outcome, err := applyProvisionReceipt(ctx, db, mustJSON(receipt), "synthetic-operator", true); err != nil || outcome != "DRY_RUN_PENDING_ACTIVATION" {
			t.Fatal(outcome, err)
		}
		callback(403)
		if outcome, err := applyProvisionReceipt(ctx, db, mustJSON(receipt), "synthetic-operator", false); err != nil || outcome != staffActivation {
			t.Fatal(outcome, err)
		}
		if outcome, err := applyProvisionReceipt(ctx, db, mustJSON(receipt), "synthetic-operator", false); err != nil || outcome != "ALREADY_APPLIED" {
			t.Fatal(outcome, err)
		}
		roleMode = "other"
		callback(403)
		roleMode = "client"
		signedVerified = false
		callback(403)
		signedVerified = true
		signedEmail = "other@example.test"
		callback(403)
		signedEmail = m.Email
		callback(303)
		var state string
		if err = db.QueryRow(ctx, `SELECT staff_status FROM app_users WHERE staff_id=$1`, m.ID).Scan(&state); err != nil || state != staffActive {
			t.Fatal(state, err)
		}
		// Existing production emergency imports cannot bypass this managed lifecycle.
		active := true
		raw := mustJSON(IdentityManifest{ApprovalReference: "SYNTHETIC-LEGACY", Teams: []IdentityTeam{}, Users: []IdentityUser{
			{Subject: tokenSubject, DisplayName: "合成", Role: salesRole, Team: syntheticTeam, Active: &active},
		}})
		if _, err = applyIdentity(ctx, db, raw, "synthetic-operator", false); err == nil {
			t.Fatal("managed identity overwritten by legacy import")
		}
		signedEmail = "other@example.test"
		callback(403)

		changeState := func(action string) {
			t.Helper()
			tx, err := db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
				t.Fatal(err)
			}
			m, err = readStaff(ctx, tx, actor, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			value, err := mutateStaff(ctx, tx, actor, nil, m, action, staffMutation{Version: m.Version, Reason: "合成恢复流程"})
			if err != nil {
				t.Fatal(err)
			}
			m = value.(staffMember)
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
		signedEmail = m.Email
		changeState("suspend")
		callback(403)
		ack := receipt
		ack.Version, ack.Operation = m.Version, "ack-suspend"
		if outcome, err := applyProvisionReceipt(ctx, db, mustJSON(ack), "synthetic-operator", false); err != nil || outcome != "ACKNOWLEDGED" {
			t.Fatal(outcome, err)
		}
		changeState("reactivate")
		callback(403)
		receipt.Version = m.Version
		if outcome, err := applyProvisionReceipt(ctx, db, mustJSON(receipt), "synthetic-operator", false); err != nil || outcome != staffActivation {
			t.Fatal(outcome, err)
		}
		roleMode = "other"
		callback(403)
		roleMode = "client"
		callback(303)
	})
	t.Run("staff_admin_requires_explicit_fresh_signed_reauthentication", func(t *testing.T) {
		tokenSubject, roleMode = "synthetic-admin", "admin"
		invite := staffProfileInput{Name: "合成新主管", Email: "fresh-reauth@example.test", Role: managerRole, TeamID: syntheticTeam, Reason: "核对主管任命"}
		for _, tc := range []struct {
			reauth bool
			age    time.Duration
			want   int
		}{{false, 0, 403}, {true, time.Minute, 403}, {true, 0, 200}} {
			authAge = tc.age
			status, body := c.call("GET", "/auth/callback?code=synthetic&state="+url.QueryEscape(startLogin(tc.reauth)), nil)
			expect(t, status, 303, body)
			status, body = c.call("GET", "/api/v1/me", nil)
			expect(t, status, 200, body)
			var admin User
			if err := json.Unmarshal(body, &admin); err != nil {
				t.Fatal(err)
			}
			c.csrf = admin.CSRF
			status, body = c.call("POST", "/api/v1/staff/invitations", invite)
			expect(t, status, tc.want, body)
		}
	})
	production := &App{cfg: Config{Env: "production"}}
	rec := httptest.NewRecorder()
	production.cookie(rec, production.cookieName(), "synthetic-only", 28800)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-baobei_session" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" || cookies[0].Domain != "" {
		t.Fatal("production cookie attributes")
	}
}
