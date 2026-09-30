package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/maasapi"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

// End-to-end partner API flow against a real Postgres and a MaaS stand-in:
// create → search → mint (fixed GE group) → report → UUID policy →
// deactivate (revocation failure, retry, success) → reactivate. Skipped
// without DATABASE_URL, like the storage integration suites.
func TestPartnerUserAPIEndToEnd(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — partner API integration test needs a Postgres")
	}
	store := openFreshStore(t, dsn)

	var mu sync.Mutex
	var minted []string
	var revoked []string
	failRevoke := true
	maas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-MaaS-Group") != `["GE"]` {
			t.Errorf("MaaS group header = %q, want [\"GE\"]", r.Header.Get("X-MaaS-Group"))
		}
		switch r.URL.Path {
		case "/v1/api-keys":
			minted = append(minted, r.Header.Get("X-MaaS-Username"))
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "key-1", "name": "n", "key": "sk-test-secret", "username": r.Header.Get("X-MaaS-Username"), "status": "active"})
		case "/v1/api-keys/search":
			var body struct {
				Filters struct {
					Username string `json:"username"`
				} `json:"filters"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Filters.Username != r.Header.Get("X-MaaS-Username") {
				t.Errorf("key search must be self-scoped: filter %q header %q", body.Filters.Username, r.Header.Get("X-MaaS-Username"))
			}
			data := []map[string]any{}
			for _, owner := range minted {
				if owner == body.Filters.Username {
					data = append(data, map[string]any{"id": "key-1", "name": "primary", "username": owner, "status": "active"})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "has_more": false})
		case "/v1/api-keys/key-1":
			if r.Method != http.MethodDelete {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("X-MaaS-Username") != "alice@example.com" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "key-1", "status": "revoked"})
		case "/v1/api-keys/key-other":
			w.WriteHeader(http.StatusNotFound)
		case "/v1/api-keys/bulk-revoke":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["username"] != r.Header.Get("X-MaaS-Username") {
				t.Errorf("bulk revoke must be self-scoped: body %q header %q", body["username"], r.Header.Get("X-MaaS-Username"))
			}
			if failRevoke {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			revoked = append(revoked, body["username"])
			_ = json.NewEncoder(w).Encode(map[string]any{"revokedCount": 1})
		default:
			t.Errorf("unexpected MaaS call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer maas.Close()

	users := NewPartnerUsersHandler(store, maasapi.NewClient(maas.URL, "tenant"))
	usage := NewPartnerUserUsageHandler(store)
	policy := NewUserModelPolicyHandler(store)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users", users.HandleUsers)
	mux.HandleFunc("/api/v1/users/", users.HandleUsers)
	mux.HandleFunc("/api/v1/usage/reports", usage.HandleBatchUserUsage)
	mux.HandleFunc("/api/v1/model-policies/users/", policy.HandleUserModelPolicy)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const alice = "123e4567-e89b-12d3-a456-426614174000"
	const bob = "123e4567-e89b-12d3-a456-426614174001"
	const carol = "123e4567-e89b-12d3-a456-426614174002" // never created
	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		}
		req, _ := http.NewRequest(method, srv.URL+path, reader)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	tags := map[string]any{"email": "alice@example.com", "first_name": "Alice", "last_name": "Example", "manager_uuid": nil, "department": "eng"}
	if code, _ := do(http.MethodPost, "/api/v1/users", map[string]any{"user_id": alice, "tags": tags}); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	if code, _ := do(http.MethodPost, "/api/v1/users", map[string]any{"user_id": bob, "tags": map[string]any{"email": "ALICE@example.com", "first_name": "B", "last_name": "B"}}); code != http.StatusConflict {
		t.Fatalf("duplicate email create = %d, want 409", code)
	}
	if code, _ := do(http.MethodPost, "/api/v1/users", map[string]any{"user_id": bob, "tags": map[string]any{"email": "bob@example.com", "first_name": "Bob", "last_name": "Example", "employee_number": 7}}); code != http.StatusBadRequest {
		t.Fatalf("non-string tag create = %d, want 400", code)
	}
	if code, out := do(http.MethodGet, "/api/v1/users?tag.department=eng", nil); code != http.StatusOK || len(out["users"].([]any)) != 1 {
		t.Fatalf("tag search = %d %v", code, out)
	}
	if code, out := do(http.MethodGet, "/api/v1/users?tag.department=eng&unknown=1", nil); code != http.StatusBadRequest {
		t.Fatalf("unsupported query = %d %v", code, out)
	}

	if code, out := do(http.MethodPost, "/api/v1/users/"+alice+"/keys", map[string]any{"name": "primary"}); code != http.StatusOK || out["key"] != "sk-test-secret" {
		t.Fatalf("mint = %d %v", code, out)
	}
	mu.Lock()
	if len(minted) != 1 || minted[0] != "alice@example.com" {
		t.Fatalf("minted usernames = %v", minted)
	}
	mu.Unlock()
	if code, out := do(http.MethodGet, "/api/v1/users/"+alice+"/keys", nil); code != http.StatusOK || len(out["keys"].([]any)) != 1 {
		t.Fatalf("key list = %d %v", code, out)
	}
	if code, out := do(http.MethodDelete, "/api/v1/users/"+alice+"/keys/key-other", nil); code != http.StatusNotFound {
		t.Fatalf("revoke key not owned = %d %v, want 404", code, out)
	}
	if code, out := do(http.MethodDelete, "/api/v1/users/"+alice+"/keys/key-1", nil); code != http.StatusOK || out["status"] != "revoked" {
		t.Fatalf("revoke own key = %d %v", code, out)
	}

	// PUT is an upsert: unknown UUID is created (201), known UUID replaced (200).
	if code, out := do(http.MethodPut, "/api/v1/users/"+bob, map[string]any{"tags": map[string]any{"email": "bob@example.com", "first_name": "Bob", "last_name": "Example"}}); code != http.StatusCreated || out["active"] != true {
		t.Fatalf("upsert create = %d %v", code, out)
	}
	if code, out := do(http.MethodPut, "/api/v1/users/"+bob, map[string]any{"tags": map[string]any{"email": "bob@example.com", "first_name": "Robert", "last_name": "Example"}}); code != http.StatusOK || out["tags"].(map[string]any)["first_name"] != "Robert" {
		t.Fatalf("upsert replace = %d %v", code, out)
	}
	if code, _ := do(http.MethodDelete, "/api/v1/users/"+bob, nil); code != http.StatusBadGateway {
		t.Fatalf("bob delete with MaaS failure = %d, want 502", code)
	}

	if err := store.InsertEvent(t.Context(), storage.UsageEvent{
		EventID: "e2e-1", Timestamp: time.Now().Add(-time.Hour), Username: "alice@example.com",
		Provider: "anthropic", Model: "model-a", PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7,
	}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	report := map[string]any{"user_ids": []string{alice, carol}, "from": time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339), "to": time.Now().UTC().Format(time.RFC3339)}
	code, out := do(http.MethodPost, "/api/v1/usage/reports", report)
	if code != http.StatusOK {
		t.Fatalf("report = %d %v", code, out)
	}
	reportUsers := out["users"].([]any)
	if len(reportUsers) != 1 || reportUsers[0].(map[string]any)["totals"].(map[string]any)["totalTokens"].(float64) != 7 {
		t.Fatalf("report users = %v", reportUsers)
	}
	if missing := out["missing_user_ids"].([]any); len(missing) != 1 || missing[0] != carol {
		t.Fatalf("missing ids = %v", missing)
	}

	if code, out := do(http.MethodPut, "/api/v1/model-policies/users/"+alice+"/allowlist", map[string]any{"models": []string{"model-a"}}); code != http.StatusOK || out["enabled"] != true {
		t.Fatalf("policy put = %d %v", code, out)
	}
	if code, _ := do(http.MethodPut, "/api/v1/model-policies/users/"+carol+"/allowlist", map[string]any{"models": []string{"model-a"}}); code != http.StatusNotFound {
		t.Fatalf("policy put unknown user = %d, want 404", code)
	}
	if code, _ := do(http.MethodPut, "/api/v1/model-policies/users/"+bob+"/allowlist", map[string]any{"models": []string{"model-a"}}); code != http.StatusConflict {
		t.Fatalf("policy put inactive user = %d, want 409", code)
	}
	if code, _ := do(http.MethodPut, "/api/v1/model-policies/users/alice%40example.com/allowlist", map[string]any{"models": []string{"model-a"}}); code != http.StatusBadRequest {
		t.Fatalf("policy put by email = %d, want 400", code)
	}
	if decision, err := store.GetMonthlyUsage(t.Context(), "alice@example.com", "model-b", false); err != nil || decision.ModelAllowed {
		t.Fatalf("gateway login must resolve UUID policy: %#v err %v", decision, err)
	}

	// Deactivate while MaaS revocation fails: user must be inactive and the
	// call reported as pending, then a retry completes it.
	if code, _ := do(http.MethodDelete, "/api/v1/users/"+alice, nil); code != http.StatusBadGateway {
		t.Fatalf("delete with MaaS failure = %d, want 502", code)
	}
	if code, out := do(http.MethodGet, "/api/v1/users/"+alice, nil); code != http.StatusOK || out["active"] != false || out["key_revocation_pending"] != true {
		t.Fatalf("user after failed revoke = %d %v", code, out)
	}
	if code, _ := do(http.MethodPost, "/api/v1/users/"+alice+"/keys", map[string]any{"name": "again"}); code != http.StatusConflict {
		t.Fatalf("mint for inactive = %d, want 409", code)
	}
	if code, _ := do(http.MethodPost, "/api/v1/users/"+alice+"/reactivate", nil); code != http.StatusConflict {
		t.Fatalf("reactivate while pending = %d, want 409", code)
	}
	mu.Lock()
	failRevoke = false
	mu.Unlock()
	if code, out := do(http.MethodDelete, "/api/v1/users/"+alice, nil); code != http.StatusOK || out["keys_revoked"] != true {
		t.Fatalf("delete retry = %d %v", code, out)
	}
	mu.Lock()
	if strings.Join(revoked, ",") != "alice@example.com" {
		t.Fatalf("revoked usernames = %v", revoked)
	}
	mu.Unlock()
	if code, out := do(http.MethodGet, "/api/v1/model-policies/users/"+alice+"/allowlist", nil); code != http.StatusOK || out["enabled"] != true || len(out["models"].([]any)) != 0 {
		t.Fatalf("inactive policy must be deny-all: %d %v", code, out)
	}
	if code, out := do(http.MethodPost, "/api/v1/users/"+alice+"/reactivate", nil); code != http.StatusOK || out["active"] != true {
		t.Fatalf("reactivate = %d %v", code, out)
	}
	if code, out := do(http.MethodGet, "/api/v1/model-policies/users/"+alice+"/allowlist", nil); code != http.StatusOK || len(out["models"].([]any)) != 1 {
		t.Fatalf("policy must survive deactivation cycle: %d %v", code, out)
	}
}

func openFreshStore(t *testing.T, dsn string) *storage.Store {
	t.Helper()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	dbName := fmt.Sprintf("partnerapi_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + dbName); err != nil {
		t.Fatalf("create test db: %v", err)
	}
	slash := strings.LastIndex(dsn, "/")
	rest := dsn[slash+1:]
	query := ""
	if q := strings.Index(rest, "?"); q >= 0 {
		query = rest[q:]
	}
	store, err := storage.New(dsn[:slash+1]+dbName+query, 0)
	if err != nil {
		t.Fatalf("open fresh store: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
		admin.Exec("DROP DATABASE " + dbName) //nolint:errcheck
		admin.Close()
	})
	return store
}
