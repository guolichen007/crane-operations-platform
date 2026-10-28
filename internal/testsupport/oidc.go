package testsupport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

const OIDCClient = "anonymous-crane-bff"
const OIDCSecret = "anonymous-integration-client-secret-only"

type OIDCScenario struct {
	Subject, Nonce, Issuer, Audience, AZP string
	RejectPKCE                            bool
}

type authorization struct{ Nonce, Challenge, Redirect string }

type OIDCProvider struct {
	Server   *httptest.Server
	mu       sync.Mutex
	scenario OIDCScenario
	codes    map[string]authorization
	key      *rsa.PrivateKey
}

func NewOIDC(t *testing.T) *OIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &OIDCProvider{key: key, codes: map[string]authorization{}, scenario: OIDCScenario{Subject: "subject-a"}}
	p.Server = httptest.NewTLSServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *OIDCProvider) Set(scenario OIDCScenario) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scenario = scenario
}

func (p *OIDCProvider) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.Server.URL, "authorization_endpoint": p.Server.URL + "/authorize", "token_endpoint": p.Server.URL + "/token", "jwks_uri": p.Server.URL + "/keys", "end_session_endpoint": p.Server.URL + "/logout", "id_token_signing_alg_values_supported": []string{"RS256"}})
	case "/keys":
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
	case "/authorize":
		q := r.URL.Query()
		if q.Get("client_id") != OIDCClient || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "invalid authorization request", 400)
			return
		}
		code := platform.ID()
		p.codes[code] = authorization{Nonce: q.Get("nonce"), Challenge: q.Get("code_challenge"), Redirect: q.Get("redirect_uri")}
		target, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "invalid redirect", 400)
			return
		}
		values := target.Query()
		values.Set("code", code)
		values.Set("state", q.Get("state"))
		target.RawQuery = values.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
	case "/token":
		if r.ParseForm() != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		client, secret, ok := r.BasicAuth()
		if !ok {
			client, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		code, exists := p.codes[r.PostForm.Get("code")]
		delete(p.codes, r.PostForm.Get("code"))
		digest := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		challenge := base64.RawURLEncoding.EncodeToString(digest[:])
		if client != OIDCClient || secret != OIDCSecret || !exists || p.scenario.RejectPKCE || subtle.ConstantTimeCompare([]byte(challenge), []byte(code.Challenge)) != 1 || r.PostForm.Get("redirect_uri") != code.Redirect {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		s := p.scenario
		issuer, audience, nonce := p.Server.URL, OIDCClient, code.Nonce
		if s.Issuer != "" {
			issuer = s.Issuer
		}
		if s.Audience != "" {
			audience = s.Audience
		}
		if s.Nonce != "" {
			nonce = s.Nonce
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: p.key, KeyID: "fixture"}}, nil)
		if err != nil {
			http.Error(w, "signing failed", 500)
			return
		}
		token, err := jwt.Signed(signer).Claims(map[string]any{"iss": issuer, "sub": s.Subject, "aud": audience, "azp": s.AZP, "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()}).Serialize()
		if err != nil {
			http.Error(w, "signing failed", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "anonymous-discarded-access-token", "token_type": "Bearer", "expires_in": 300, "id_token": token})
	default:
		http.NotFound(w, r)
	}
}

// LoginResponse runs a real TLS authorization request and returns its callback URL.
func (p *OIDCProvider) LoginResponse(t *testing.T, authorize string) string {
	t.Helper()
	client := *p.Server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(authorize)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 302 {
		t.Fatal("fixture authorization failed", response.StatusCode)
	}
	return response.Header.Get("Location")
}
