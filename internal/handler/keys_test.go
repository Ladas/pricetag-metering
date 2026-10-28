package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

type keyTransport func(*http.Request) (*http.Response, error)

func (f keyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPersonalKeysRejectUnsafeRequests(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"DELETE", "/me/keys/../other", ""},
		{"DELETE", "/me/keys/a?route=other", ""},
		{"DELETE", "/me/keys/a/b", ""},
		{"POST", "/me/keys", `{"name":"test","username":"admin"}`},
		{"POST", "/me/keys", `{"name":"test","expiresIn":"30d"} {"username":"admin"}`},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			cfg := config.Config{UserHeader: "X-User", KeyService: config.KeyService{URL: "https://keys.invalid", UserHeader: "X-User", GroupsHeader: "X-Groups"}}
			h := NewKeysHandler(nil, cfg, nil)
			h.client.Transport = keyTransport(func(r *http.Request) (*http.Response, error) {
				t.Fatal("unsafe request reached upstream")
				return nil, nil
			})
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("X-User", "alice")
			w := httptest.NewRecorder()
			h.HandleKeys(w, r)
			if w.Code != 400 {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestPersonalKeyIdentityAndNoCache(t *testing.T) {
	cfg := config.Config{UserHeader: "X-User", GroupsHeader: "X-Groups", DefaultGroup: "users", KeyService: config.KeyService{URL: "https://keys.invalid", UserHeader: "X-Username", GroupsHeader: "X-Groups"}}
	h := NewKeysHandler(nil, cfg, nil)
	h.client.Transport = keyTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"username":"alice"`) || r.Header.Get("X-Username") != "alice" {
			t.Fatal("list not scoped to session identity")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
	})
	r := httptest.NewRequest("GET", "/me/keys", nil)
	r.Header.Set("X-User", "alice")
	w := httptest.NewRecorder()
	h.HandleKeys(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unexpected response")
	}
}

func TestStandalonePersonalKeysNeverForwardAdministratorGroups(t *testing.T) {
	for _, method := range []string{"GET", "POST", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			cfg := config.Config{ManagementAPISecret: "fixture", UserHeader: "X-User", GroupsHeader: "X-Groups", DefaultGroup: "system:authenticated", KeyService: config.KeyService{URL: "https://keys.invalid", UserHeader: "X-User", GroupsHeader: "X-Groups"}}
			h := NewKeysHandler(nil, cfg, nil)
			h.client.Transport = keyTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("X-Groups") != `["system:authenticated"]` {
					t.Errorf("personal endpoint forwarded privileged groups: %s", r.Header.Get("X-Groups"))
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"own","username":"alice"}`))}, nil
			})
			path := "/me/keys"
			if method == "DELETE" {
				path += "/own"
			}
			r := httptest.NewRequest(method, path, strings.NewReader(`{"name":"personal"}`))
			r.Header.Set("X-User", "alice")
			// Represents either a current administrator or a stale pre-demotion session.
			r.Header.Set("X-Groups", `["single-server-admin"]`)
			w := httptest.NewRecorder()
			h.HandleKeys(w, r)
			if w.Code != 200 {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestStandalonePersonalRevokeChecksOwner(t *testing.T) {
	cfg := config.Config{ManagementAPISecret: "fixture", UserHeader: "X-User", GroupsHeader: "X-Groups", DefaultGroup: "users", KeyService: config.KeyService{URL: "https://keys.invalid", UserHeader: "X-User", GroupsHeader: "X-Groups"}}
	h := NewKeysHandler(nil, cfg, nil)
	h.client.Transport = keyTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("foreign-key mutation reached upstream")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"foreign","username":"bob"}`))}, nil
	})
	r := httptest.NewRequest("DELETE", "/me/keys/foreign", nil)
	r.Header.Set("X-User", "alice")
	r.Header.Set("X-Groups", `["single-server-admin"]`)
	w := httptest.NewRecorder()
	h.HandleKeys(w, r)
	if w.Code != 404 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestPersonalKeyPagination(t *testing.T) {
	for _, tc := range []struct {
		query string
		valid bool
	}{
		{"?offset=50&status=active", true}, {"?offset=0&status=inactive", true},
		{"?offset=-1", false}, {"?offset=word", false}, {"?username=bob", false},
		{"?status=admin", false}, {"?offset=0&offset=50", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			cfg := config.Config{UserHeader: "X-User", KeyService: config.KeyService{URL: "https://keys.invalid", UserHeader: "X-User"}}
			h := NewKeysHandler(nil, cfg, nil)
			h.client.Transport = keyTransport(func(r *http.Request) (*http.Response, error) {
				if !tc.valid {
					t.Fatal("invalid pagination reached upstream")
				}
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), `"username":"alice"`) {
					t.Fatal("lost owner scope")
				}
				if strings.Contains(tc.query, "status=active") && (!strings.Contains(string(body), `"status":["active"]`) || !strings.Contains(string(body), `"offset":50`)) {
					t.Fatal("missing pagination/filter")
				}
				if strings.Contains(tc.query, "status=inactive") && !strings.Contains(string(body), `"status":["revoked","expired"]`) {
					t.Fatal("missing archived filter")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[],"hasMore":false}`))}, nil
			})
			r := httptest.NewRequest("GET", "/me/keys"+tc.query, nil)
			r.Header.Set("X-User", "alice")
			w := httptest.NewRecorder()
			h.HandleKeys(w, r)
			expected := 400
			if tc.valid {
				expected = 200
			}
			if w.Code != expected {
				t.Fatalf("got %d want %d", w.Code, expected)
			}
		})
	}
}
