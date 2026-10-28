package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

func TestAdministrationReusesUpstreamControls(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := config.Config{}
		if enabled {
			cfg.ManagementAPISecret = "test-only"
		}
		h := NewAdminHandler(nil, nil, cfg)
		w := httptest.NewRecorder()
		h.ServeAdmin(w, httptest.NewRequest("GET", "/admin", nil))
		body := w.Body.String()
		for _, control := range []string{"partner-users-list", "updatePartnerAccess", "saveSafetyNet", "savePricingDiscounts"} {
			if !strings.Contains(body, control) {
				t.Fatalf("missing upstream control %s", control)
			}
		}
		if strings.Contains(body, `id="onboard"`) != enabled {
			t.Fatal("standalone extensions must be opt-in")
		}
		if strings.Contains(body, `id="team"`) {
			t.Fatal("duplicate hierarchy editor")
		}
	}
}

func TestPersonalPageHasNoAdministrationEditor(t *testing.T) {
	w := httptest.NewRecorder()
	ServeManagement(w, httptest.NewRequest("GET", "/management", nil))
	body := w.Body.String()
	for _, component := range []string{`class="appbar"`, `class="logo"`, `href="/dashboard"`, `class="tbl-input"`, `class="btn btn-grn"`} {
		if !strings.Contains(body, component) {
			t.Fatalf("missing shared UI component %s", component)
		}
	}
	if !strings.Contains(body, `id="key"`) {
		t.Fatal("personal key controls missing")
	}
	for _, duplicate := range []string{`id="user"`, `id="team"`, `id="budget"`, `id="price"`} {
		if strings.Contains(body, duplicate) {
			t.Fatalf("personal page contains admin editor %s", duplicate)
		}
	}
}
