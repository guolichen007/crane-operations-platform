package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/inventory"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
	"github.com/guolichen007/crane-operations-platform/internal/webauth"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/alarm"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/device"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/session"
)

type securityFixture struct {
	db       *sql.DB
	provider *testsupport.OIDCProvider
	handler  http.Handler
	server   *httptest.Server
}

func newSecurityFixture(t *testing.T) securityFixture {
	t.Helper()
	ctx := context.Background()
	db := testsupport.Database(t)
	provider := testsupport.NewOIDC(t)
	path := os.Getenv("A1_INVENTORY_EXAMPLE")
	if path == "" {
		path = "../../../../deploy/inventory/inventory.example.yaml"
	}
	document, err := inventory.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = inventory.Import(ctx, db, document); err != nil {
		t.Fatal(err)
	}
	store, err := device.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range document.RuntimeDevices {
		envelope := contract.WithPayload(contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: document.TenantId, ProjectId: runtime.ProjectId, DeviceId: runtime.RuntimeDeviceId, BootId: "security-fixture", BootSeq: 1, Sequence: 1, Kind: "BIRTH", SentAt: time.Now().UTC()}, contract.BirthPayload{EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY", Reported: contract.ReportedState{OperatingState: "SILENT", Mode: "AUTO", AlarmState: "UNKNOWN"}})
		if accepted, err := store.Apply(ctx, envelope); err != nil || !accepted {
			t.Fatal("registered fixture shadow failed", err)
		}
		envelope.MessageId = platform.ID()
		envelope.Kind = "ALARM"
		envelope = contract.WithPayload(envelope, contract.AlarmPayload{Code: "SIMULATED_OVERLOAD", Text: "Anonymous scoped alarm"})
		if err = alarm.Record(ctx, db, envelope); err != nil {
			t.Fatal(err)
		}
	}
	for user, role := range map[string]string{"a": "viewer", "b": "operator", "operator": "operator", "maintainer": "maintainer", "admin": "platform_admin"} {
		if err = identity.Bind(ctx, db, "user-"+user, provider.Server.URL, "subject-"+user, "Anonymous "+user, true, auditlog.Event{Actor: "fixture"}); err != nil {
			t.Fatal(err)
		}
		project := "P001"
		if user == "b" {
			project = "P002"
		}
		if err = identity.ChangeGrant(ctx, db, "user-"+user, project, role, false, auditlog.Event{Actor: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	var delegate atomic.Value
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { delegate.Load().(http.Handler).ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	manager, err := webauth.New(ctx, db, webauth.Config{PublicURL: server.URL, Issuer: provider.Server.URL, ClientID: testsupport.OIDCClient, ClientSecret: []byte(testsupport.OIDCSecret), ServerSecret: []byte(strings.Repeat("anonymous-server-key-", 3)), AbsoluteTTL: time.Hour, IdleTTL: 5 * time.Minute, HTTPClient: provider.Server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handlers := &apiHandler{config: platform.Config{Env: "production"}, db: db, store: store, leases: session.New(db, time.Minute, 2*time.Second), active: func(contract.Shadow) bool { return true }, list: store.List, logger: platform.Logger("a1-fixture"), wsClients: &atomic.Int64{}, dbErrors: &atomic.Int64{}, web: manager}
	if err = handlers.register(mux); err != nil {
		t.Fatal(err)
	}
	delegate.Store(http.Handler(mux))
	return securityFixture{db: db, provider: provider, handler: mux, server: server}
}

func (f securityFixture) login(t *testing.T, user string) (*http.Cookie, contract.WebSession) {
	t.Helper()
	f.provider.Set(testsupport.OIDCScenario{Subject: "subject-" + user})
	return testsupport.Login(t, f.handler, f.provider, f.server.URL)
}

func (f securityFixture) request(cookie *http.Cookie, csrf, path, method string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, f.server.URL+path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if stateChanging(method) {
		r.Header.Set("Origin", f.server.URL)
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestA1ProjectScopeFirstDirectObjectsAndViewerOwnership(t *testing.T) {
	f := newSecurityFixture(t)
	a, sessionA := f.login(t, "a")
	b, sessionB := f.login(t, "b")
	for path, want := range map[string]string{"/api/v1/projects": "P001", "/api/v1/assets/devices": "ASSET-P001-01", "/api/v1/devices": "SIM-P001-01", "/api/v1/alarms": "SIM-P001-01"} {
		w := f.request(a, sessionA.CsrfToken, path, "GET")
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) || strings.Contains(w.Body.String(), "P002") {
			t.Fatal("list leaked another project", path, w.Code, w.Body.String())
		}
	}
	openB := f.request(b, sessionB.CsrfToken, "/api/v1/devices/SIM-P002-01/live-sessions", "POST")
	var lease contract.LiveSession
	if openB.Code != 201 || json.Unmarshal(openB.Body.Bytes(), &lease) != nil {
		t.Fatal("own project LIVE denied", openB.Code, openB.Body.String())
	}
	for _, test := range []struct{ path, method string }{
		{"/api/v1/devices/SIM-P002-01", "GET"}, {"/api/v1/devices/SIM-P002-01/shadow", "GET"},
		{"/api/v1/alarms?deviceId=SIM-P002-01", "GET"}, {"/api/v1/devices/SIM-P002-01/stream", "GET"},
		{"/api/v1/devices/SIM-P002-01/live-sessions", "POST"}, {"/api/v1/live-sessions/" + lease.SessionId + "/renew", "POST"}, {"/api/v1/live-sessions/" + lease.SessionId, "DELETE"},
	} {
		w := f.request(a, sessionA.CsrfToken, test.path, test.method)
		if w.Code != 404 {
			t.Fatal("direct cross-project object not hidden", test.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "P002") {
			t.Fatal("denial exposed project metadata")
		}
	}
	var denies int
	if err := f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE user_id='user-a' AND action='authorization_denied' AND project_id='P002' AND request_id<>''`).Scan(&denies); err != nil || denies < 7 {
		t.Fatal("cross-project denial missing audit", denies, err)
	}
	if w := f.request(a, sessionA.CsrfToken, "/api/v1/devices/SIM-P001-01/live-sessions", "POST"); w.Code != 403 {
		t.Fatal("viewer obtained LIVE", w.Code)
	}
	if w := f.request(b, sessionB.CsrfToken, "/api/v1/live-sessions/"+lease.SessionId+"/renew", "POST"); w.Code != 200 {
		t.Fatal("owner renew denied", w.Code)
	}
	if w := f.request(b, sessionB.CsrfToken, "/api/v1/live-sessions/"+lease.SessionId, "DELETE"); w.Code != 204 {
		t.Fatal("owner stop denied", w.Code)
	}
}

func TestA1FourRoleHTTPMatrixAndScopedAdministrator(t *testing.T) {
	f := newSecurityFixture(t)
	for _, role := range []string{"a", "operator", "maintainer", "admin"} {
		t.Run(role, func(t *testing.T) {
			cookie, info := f.login(t, role)
			if len(info.Projects) != 1 || info.Projects[0].ProjectId != "P001" {
				t.Fatal("role received implicit projects")
			}
			if w := f.request(cookie, info.CsrfToken, "/api/v1/devices/SIM-P001-01/shadow", "GET"); w.Code != 200 {
				t.Fatal("role cannot read own project", w.Code)
			}
			if w := f.request(cookie, info.CsrfToken, "/api/v1/devices/SIM-P002-01/shadow", "GET"); w.Code != 404 {
				t.Fatal("role bypassed scope", w.Code)
			}
			w := f.request(cookie, info.CsrfToken, "/api/v1/devices/SIM-P001-01/live-sessions", "POST")
			if role == "a" {
				if w.Code != 403 {
					t.Fatal("viewer write accepted")
				}
				return
			}
			var lease contract.LiveSession
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &lease) != nil {
				t.Fatal("operator permission failed", w.Code, w.Body.String())
			}
			if w = f.request(cookie, info.CsrfToken, "/api/v1/live-sessions/"+lease.SessionId+"/renew", "POST"); w.Code != 200 {
				t.Fatal("renew failed")
			}
			if role == "operator" {
				other, otherInfo := f.login(t, "maintainer")
				if denied := f.request(other, otherInfo.CsrfToken, "/api/v1/live-sessions/"+lease.SessionId, "DELETE"); denied.Code != 404 {
					t.Fatal("same-project other user stole viewer lease")
				}
			}
			if w = f.request(cookie, info.CsrfToken, "/api/v1/live-sessions/"+lease.SessionId, "DELETE"); w.Code != 204 {
				t.Fatal("stop failed")
			}
			if w = f.request(cookie, info.CsrfToken, "/api/v1/devices/SIM-P001-01/remote-sessions", "POST"); w.Code != 501 {
				t.Fatal("Remote must remain disabled", w.Code)
			}
		})
	}
	var starts, stops int
	if err := f.db.QueryRow(`SELECT count(*) FILTER(WHERE action='live_start'),count(*) FILTER(WHERE action='live_stop') FROM audit_logs WHERE result='success' AND project_id='P001' AND device_id='SIM-P001-01' AND request_id<>''`).Scan(&starts, &stops); err != nil || starts != 3 || stops != 3 {
		t.Fatal("LIVE mutation audit missing", starts, stops, err)
	}
}

func TestA1LiveTransactionRechecksRevokedSessionAndMembership(t *testing.T) {
	for _, revoked := range []string{"session", "membership", "disabled"} {
		t.Run(revoked, func(t *testing.T) {
			f := newSecurityFixture(t)
			_, _ = f.login(t, "operator")
			var hash []byte
			if err := f.db.QueryRow(`SELECT session_hash FROM web_sessions WHERE user_id='user-operator'`).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			// Simulate an HTTP request that passed middleware before logout/revoke committed.
			ctx := identity.WithAuthorization(context.Background(), "user-operator", hash)
			ctx = auditlog.WithEvent(ctx, auditlog.Event{Actor: "user-operator", Project: "P001", Device: "SIM-P001-01", Action: "live_start", Result: "success"})
			statement := map[string]string{"session": `DELETE FROM web_sessions WHERE user_id='user-operator'`, "membership": `DELETE FROM project_memberships WHERE user_id='user-operator'`, "disabled": `UPDATE users SET enabled=false WHERE id='user-operator'`}[revoked]
			if _, err := f.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			_, err := session.New(f.db, time.Minute, time.Second).Open(ctx, platform.ID(), "SIM-P001-01", "user-operator", time.Now())
			if !errors.Is(err, identity.ErrDenied) {
				t.Fatal("stale authorization accepted inside LIVE transaction", err)
			}
			var count int
			if err = f.db.QueryRow(`SELECT count(*) FROM live_sessions WHERE user_id='user-operator'`).Scan(&count); err != nil || count != 0 {
				t.Fatal("denied LIVE transaction left a lease")
			}
		})
	}
}

func TestA1ProductionHasNoDevelopmentRouteOrCredentialFallback(t *testing.T) {
	f := newSecurityFixture(t)
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, f.server.URL+"/api/v1/auth/dev-session", nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("d", 64))
		r.AddCookie(&http.Cookie{Name: "crane_dev", Value: strings.Repeat("d", 64)})
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 404 || w.Header().Get("Set-Cookie") != "" {
			t.Fatal("production development route exists", w.Code)
		}
	}
	r := httptest.NewRequest("GET", f.server.URL+"/api/v1/devices", nil)
	r.AddCookie(&http.Cookie{Name: "crane_dev", Value: strings.Repeat("d", 64)})
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("development Cookie accepted in production")
	}
}

func TestA1WebSocketOriginScopeAndSessionRevocation(t *testing.T) {
	for _, change := range []string{"logout", "absolute_expiry", "idle_expiry", "membership_revoke", "disabled_user"} {
		t.Run(change, func(t *testing.T) {
			f := newSecurityFixture(t)
			cookie, info := f.login(t, "operator")
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			dial := func(id, origin string) (*websocket.Conn, *http.Response, error) {
				return websocket.Dial(ctx, "wss"+strings.TrimPrefix(f.server.URL, "https")+"/api/v1/devices/"+id+"/stream", &websocket.DialOptions{HTTPClient: f.server.Client(), HTTPHeader: http.Header{"Cookie": []string{cookie.String()}, "Origin": []string{origin}}})
			}
			for _, test := range []struct {
				id, origin string
				status     int
			}{{"SIM-P002-01", f.server.URL, 404}, {"SIM-P001-01", "https://evil.example.test", 403}, {"SIM-P001-01", "null", 403}} {
				connection, response, err := dial(test.id, test.origin)
				if connection != nil {
					connection.CloseNow()
				}
				if err == nil || response == nil || response.StatusCode != test.status {
					t.Fatal("WebSocket bypass", test.id, test.origin, err)
				}
			}
			connection, _, err := dial("SIM-P001-01", f.server.URL)
			if err != nil {
				t.Fatal("own authorized stream failed; response headers redacted", err)
			}
			defer connection.CloseNow()
			if _, _, err = connection.Read(ctx); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "logout":
				if w := f.request(cookie, info.CsrfToken, "/auth/logout", "POST"); w.Code != 204 {
					t.Fatal("logout failed", w.Code)
				}
			case "absolute_expiry":
				_, err = f.db.Exec(`UPDATE web_sessions SET expires_at=created_at+interval '1 microsecond',idle_expires_at=created_at+interval '1 microsecond'`)
			case "idle_expiry":
				_, err = f.db.Exec(`UPDATE web_sessions SET idle_expires_at=now()-interval '1 second'`)
			case "membership_revoke":
				err = identity.ChangeGrant(context.Background(), f.db, "user-operator", "P001", "", true, auditlog.Event{Actor: "fixture"})
			case "disabled_user":
				_, err = f.db.Exec(`UPDATE users SET enabled=false WHERE id='user-operator'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			for {
				_, _, err = connection.Read(ctx)
				if err != nil {
					break
				}
			}
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatal("revoked stream survived or ended incorrectly", err)
			}
		})
	}
}
