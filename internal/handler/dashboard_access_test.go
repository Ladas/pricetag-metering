package handler

import (
	"github.com/redhat-et/pricetag-metering/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScopedDashboardOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, user := range []string{"", "alice", "admin"} {
			cfg := config.Config{UserHeader: "X-Forwarded-User", AdminUsers: []string{"admin"}, UserDashboardsEnabled: enabled}
			r := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/overview", nil)
			r.Header.Set(cfg.UserHeader, user)
			w := httptest.NewRecorder()
			RequirePartnerAdminAPI(cfg, nil, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })(w, r)
			want := 403
			if user == "admin" || (enabled && user != "") {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("enabled=%v user=%q: got %d want %d", enabled, user, w.Code, want)
			}
			// Enabling scoped views must never change administrative authorization.
			if IsPartnerSuperAdmin(r.Context(), cfg, nil, r) {
				t.Fatal("dashboard option granted super-admin")
			}
		}
	}
}
