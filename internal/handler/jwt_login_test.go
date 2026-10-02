package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redhat-et/pricetag-metering/internal/config"
)

func jwtFixture(t *testing.T) (*AuthHandler, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "public.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SESSION_SECRET", strings.Repeat("s", 32))
	cfg := config.Config{DashboardJWTKeyFile: path, DashboardJWTIssuer: "https://issuer.test",
		DashboardJWTAudience: "praxis-gateway", DashboardOrigin: "https://localhost:8443",
		UserHeader: "X-Forwarded-User", GroupsHeader: "X-Forwarded-Groups", SuperAdminUsers: []string{"admin@example.test"}}
	h, err := NewAuthHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, key
}

func callerToken(t *testing.T, key any, method jwt.SigningMethod, change func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now().Unix()
	claims := jwt.MapClaims{"iss": "https://issuer.test", "aud": "praxis-gateway", "sub": "alice",
		"iat": now, "exp": now + 3600, "jti": "test-caller"}
	if change != nil {
		change(claims)
	}
	raw, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func loginRequest(h *AuthHandler, token, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"api_key": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	h.HandleLogin(w, r)
	return w
}

func TestJWTLoginValidatesTrustAndBoundsSession(t *testing.T) {
	h, key := jwtFixture(t)
	expires := time.Now().Add(5 * time.Minute).Unix()
	token := callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { c["exp"] = expires })
	w := loginRequest(h, token, h.jwt.origin)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("login failed: %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge > 300 || c.Expires.Unix() > expires {
		t.Fatalf("cookie is not secure or exceeds token lifetime: %+v", c)
	}
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.AddCookie(c)
	session := h.readCookie(r)
	if session == nil || session.Username != "alice" || session.Exp != expires {
		t.Fatal("wrong session identity or expiration")
	}
	h.jwt.keyID = "rotated-trust"
	if h.readCookie(r) != nil {
		t.Fatal("session survived a trust rotation")
	}
}

func TestJWTLoginConfigurationAndMaximumSession(t *testing.T) {
	h, key := jwtFixture(t)
	for _, change := range []func(*config.Config){
		func(c *config.Config) { c.DashboardJWTIssuer = "" },
		func(c *config.Config) { c.DashboardJWTAudience = "" },
		func(c *config.Config) { c.DashboardJWTKeyFile = "/missing-key" },
		func(c *config.Config) { c.DashboardOrigin = "http://localhost:8443" },
		func(c *config.Config) { c.DashboardOrigin = "https://localhost:8443/path" },
	} {
		cfg := h.cfg
		change(&cfg)
		if _, err := NewAuthHandler(cfg); err == nil {
			t.Fatal("invalid JWT trust configuration accepted")
		}
	}
	t.Setenv("SESSION_SECRET", "short")
	if _, err := NewAuthHandler(h.cfg); err == nil {
		t.Fatal("weak session secret accepted")
	}
	raw := callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) {
		c["exp"] = time.Now().Add(30 * 24 * time.Hour).Unix()
	})
	session, err := h.jwt.validate(raw)
	if err != nil || session.Exp > time.Now().Add(8*time.Hour).Unix() {
		t.Fatal("long-lived JWT created an unbounded session")
	}
}

func TestJWTLoginRejectsInvalidCredentials(t *testing.T) {
	h, key := jwtFixture(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"wrong signature":   callerToken(t, other, jwt.SigningMethodRS256, nil),
		"wrong algorithm":   callerToken(t, []byte("secret"), jwt.SigningMethodHS256, nil),
		"wrong issuer":      callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { c["iss"] = "https://other.test" }),
		"wrong audience":    callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { c["aud"] = "other" }),
		"expired":           callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { c["exp"] = time.Now().Unix() - 1 }),
		"missing expiry":    callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { delete(c, "exp") }),
		"missing issued at": callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { delete(c, "iat") }),
		"future issued at":  callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { c["iat"] = time.Now().Unix() + 300 }),
		"not yet valid":     callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { c["nbf"] = time.Now().Unix() + 300 }),
		"missing subject":   callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { delete(c, "sub") }),
		"unsafe subject":    callerToken(t, key, jwt.SigningMethodRS256, func(c jwt.MapClaims) { c["sub"] = "alice/../admin" }),
		"malformed":         "not-a-jwt",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			w := loginRequest(h, raw, h.jwt.origin)
			if w.Header().Get("Location") != "/login?error=invalid" || len(w.Result().Cookies()) != 0 {
				t.Fatal("invalid credential created a session")
			}
		})
	}
}

func TestJWTLoginAndSessionRejectCrossOriginMutation(t *testing.T) {
	h, key := jwtFixture(t)
	raw := callerToken(t, key, jwt.SigningMethodRS256, nil)
	for _, origin := range []string{"", "https://attacker.test"} {
		if w := loginRequest(h, raw, origin); w.Code != http.StatusForbidden {
			t.Fatal("cross-origin login was allowed")
		}
	}
	cookie := loginRequest(h, raw, h.jwt.origin).Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/quota/policy", nil)
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://attacker.test")
	w := httptest.NewRecorder()
	h.RequireAuth(func(http.ResponseWriter, *http.Request) { t.Fatal("cross-origin mutation reached handler") })(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d", w.Code)
	}
}

func TestJWTBearerIdentityIgnoresForgedHeadersAndRequiresExactAdmin(t *testing.T) {
	h, key := jwtFixture(t)
	raw := callerToken(t, key, jwt.SigningMethodRS256, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me/quota", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	r.Header.Set(h.cfg.UserHeader, "admin@example.test")
	r.Header.Set(h.cfg.GroupsHeader, `["admin"]`)
	r.Header.Set(realUserHeader, "admin@example.test")
	w := httptest.NewRecorder()
	h.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(h.cfg.UserHeader) != "alice" || r.Header.Get(h.cfg.GroupsHeader) != "" || r.Header.Get(realUserHeader) != "" || IsSuperAdmin(h.cfg, r) {
			t.Fatal("forged identity survived")
		}
		w.WriteHeader(http.StatusNoContent)
	})(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d", w.Code)
	}
	for _, user := range []string{"admin", "ADMIN@example.test", "alice"} {
		if IsSuperAdminUsername(h.cfg, user) {
			t.Fatalf("inexact admin match: %s", user)
		}
	}
	if !IsSuperAdminUsername(h.cfg, "admin@example.test") {
		t.Fatal("exact admin not recognized")
	}
	r.Header.Set("Authorization", "Bearer invalid")
	w = httptest.NewRecorder()
	h.RequireAuth(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid bearer reached handler") })(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("invalid bearer was not rejected")
	}
}

func TestLegacyMaaSLoginRemainsAvailable(t *testing.T) {
	validator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["key"] != "opaque-key" {
			t.Error("wrong validation request")
		}
		json.NewEncoder(w).Encode(map[string]any{"valid": true, "username": "legacy", "groups": []string{"users"}})
	}))
	defer validator.Close()
	t.Setenv("MAAS_VALIDATE_URL", validator.URL)
	t.Setenv("SESSION_SECRET", strings.Repeat("s", 32))
	h, err := NewAuthHandler(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if w := loginRequest(h, "opaque-key", ""); w.Header().Get("Location") != "/dashboard" {
		t.Fatal("legacy login failed")
	}
}
