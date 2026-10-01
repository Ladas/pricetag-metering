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

func TestUserScopedKeyCallsPresentTheOwner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-MaaS-Username"); got != "alice@example.com" {
			t.Errorf("X-MaaS-Username = %q, want the key owner", got)
		}
		if got := r.Header.Get("X-MaaS-Group"); got != `["team-a"]` {
			t.Errorf("X-MaaS-Group = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want no admin bearer", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/api-keys/search":
			var body struct {
				Filters struct{ Username string } `json:"filters"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Filters.Username != "alice@example.com" {
				t.Errorf("search filter username = %q", body.Filters.Username)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": "k1"}}, "has_more": false})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/api-keys/k1":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-tenant")
	keys, err := c.ListUserAPIKeys(context.Background(), " alice@example.com ", "team-a")
	if err != nil || len(keys) != 1 || keys[0].ID != "k1" {
		t.Fatalf("ListUserAPIKeys = %v, %v", keys, err)
	}
	if status, err := c.RevokeUserAPIKey(context.Background(), "k1", "alice@example.com", "team-a"); err != nil || status != http.StatusOK {
		t.Fatalf("RevokeUserAPIKey = %d, %v", status, err)
	}
	if _, err := c.RevokeUserAPIKey(context.Background(), "k1", "", "team-a"); err == nil {
		t.Fatal("revoke without owner should fail locally")
	}
}
