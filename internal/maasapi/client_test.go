package maasapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// maas-api's auth middleware 500s (AUTH_FAILURE refId 002) on any v1 call
// missing a non-empty X-MaaS-Group header. The client must refuse those
// calls locally, naming the real problem, instead of sending a request that
// fails with a confusing auth error.
func TestClientRefusesGrouplessCalls(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "test-tenant") // never dialed: guard must fire first
	tests := []struct {
		name string
		run  func() error
	}{
		{"search nil groups", func() error { _, err := c.SearchAPIKeys(context.Background(), "u@x.com", nil); return err }},
		{"search empty groups", func() error { _, err := c.SearchAPIKeys(context.Background(), "u@x.com", []string{}); return err }},
		{"revoke nil groups", func() error { return c.RevokeAPIKey(context.Background(), "key-1", nil) }},
		{"revoke empty groups", func() error { return c.RevokeAPIKey(context.Background(), "key-1", []string{}) }},
	}
	for _, tc := range tests {
		err := tc.run()
		if err == nil {
			t.Fatalf("%s: want local refusal, got nil error", tc.name)
		}
		if !strings.Contains(err.Error(), "no groups") {
			t.Fatalf("%s: error %q should name the missing groups, not leak a transport/auth error", tc.name, err)
		}
	}
}

func TestBulkRevokeAPIKeysUsesUserScopedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/api-keys/bulk-revoke" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-MaaS-Username"); got != "alice@example.com" {
			t.Errorf("X-MaaS-Username = %q, want alice@example.com", got)
		}
		if got := r.Header.Get("X-MaaS-Group"); got != `["GE"]` {
			t.Errorf("X-MaaS-Group = %q, want [\"GE\"]", got)
		}
		if got := r.Header.Get("X-MaaS-Tenant"); got != "test-tenant" {
			t.Errorf("X-MaaS-Tenant = %q, want test-tenant", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want no admin bearer", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if body["username"] != "alice@example.com" {
			t.Errorf("username = %q, want alice@example.com", body["username"])
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-tenant")
	if err := c.BulkRevokeAPIKeys(context.Background(), " alice@example.com ", "GE"); err != nil {
		t.Fatalf("BulkRevokeAPIKeys() error = %v", err)
	}
}

func TestBulkRevokeAPIKeysRejectsMissingScope(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "test-tenant")
	if err := c.BulkRevokeAPIKeys(context.Background(), "", "GE"); err == nil {
		t.Fatal("BulkRevokeAPIKeys with no username should fail locally")
	}
}
