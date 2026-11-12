package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

func TestOIDCLogoutConfirmationAllowsOnlyConfiguredHTTPSIssuer(t *testing.T) {
	h := &AuthHandler{cfg: config.Config{ManagementAPISecret: "test-only"}}
	h.oidc = &oidcProvider{endSessionURL: "https://issuer.example:8443/realms/lab/protocol/openid-connect/logout"}
	request := httptest.NewRequest(http.MethodGet, "https://untrusted.example/logout", nil)
	response := httptest.NewRecorder()
	h.HandleLogout(response, request)
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "form-action 'self' https://issuer.example:8443") {
		t.Fatalf("logout policy must allow configured issuer origin, got %q", policy)
	}
	if strings.Contains(policy, "untrusted.example") {
		t.Fatal("request host must not influence logout policy")
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("GET confirmation must not mutate session")
	}
}

func TestLogoutFormPolicyDoesNotExpandOtherActionsOrUnsafeOrigins(t *testing.T) {
	for _, endpoint := range []string{"http://issuer.example/logout", "https://user:pass@issuer.example/logout", "https://issuer.example;evil/logout", "//issuer.example/logout", ""} {
		h := &AuthHandler{oidc: &oidcProvider{endSessionURL: endpoint}}
		if got := h.sessionFormAction(httptest.NewRequest(http.MethodGet, "/logout", nil)); got != "'self'" {
			t.Errorf("unsafe endpoint %q widened policy to %q", endpoint, got)
		}
	}
	h := &AuthHandler{oidc: &oidcProvider{endSessionURL: "https://issuer.example/logout"}}
	if got := h.sessionFormAction(httptest.NewRequest(http.MethodGet, "/admin/impersonate?as=alice", nil)); got != "'self'" {
		t.Errorf("impersonation must retain self-only form destination, got %q", got)
	}
}
