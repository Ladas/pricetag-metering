package handler

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/k8s"
	"github.com/redhat-et/pricetag-metering/internal/metrics"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

// KeysHandler proxies API key management to an upstream key service on
// behalf of the signed-in user. The upstream is expected to expose:
//
//	POST   {url}/v1/api-keys/search   list the caller's keys
//	POST   {url}/v1/api-keys          issue a key
//	DELETE {url}/v1/api-keys/{id}     revoke a key
//
// The handler is inert unless KEY_SERVICE_URL is set, in which case the
// endpoints report 501 and the account page simply omits key management.
type KeysHandler struct {
	k8sClient *k8s.Client
	cfg       config.Config
	store     *storage.Store
	client    *http.Client
}

func NewKeysHandler(k8sClient *k8s.Client, cfg config.Config, store *storage.Store) *KeysHandler {
	return &KeysHandler{
		k8sClient: k8sClient,
		cfg:       cfg,
		store:     store,
		client: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: cfg.KeyService.InsecureSkipVerify, //nolint:gosec // opt-in for internal self-signed certificates
				},
			},
		},
	}
}

// identityHeaders forwards the caller's identity upstream, resolving group
// membership from the request, then the optional cluster adapter, then the
// configured default.
func (h *KeysHandler) identityHeaders(r *http.Request) map[string]string {
	user := r.Header.Get(h.cfg.UserHeader)
	groups := r.Header.Get(h.cfg.GroupsHeader)
	// Standalone personal operations must never inherit administrative authority
	// from a current or stale login session. Administrator operations have their
	// own fresh role checks and are not routed through /me/keys.
	if h.cfg.ManagementAPISecret != "" {
		encoded, _ := json.Marshal([]string{h.cfg.DefaultGroup})
		groups = string(encoded)
	}

	if groups == "" && h.k8sClient != nil && user != "" {
		userGroups, _ := h.k8sClient.GetUserGroups(user)
		if len(userGroups) > 0 {
			groups = `["` + strings.Join(userGroups, `","`) + `"]`
		}
	}
	if groups == "" {
		groups = `["` + h.cfg.DefaultGroup + `"]`
	}

	headers := map[string]string{
		h.cfg.KeyService.UserHeader:   user,
		h.cfg.KeyService.GroupsHeader: groups,
		"Content-Type":                "application/json",
	}
	if h.cfg.KeyService.Tenant != "" {
		headers[h.cfg.KeyService.TenantHeader] = h.cfg.KeyService.Tenant
	}
	return headers
}

func (h *KeysHandler) proxy(method, path string, headers map[string]string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, h.cfg.KeyService.URL+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return h.client.Do(req)
}

func (h *KeysHandler) HandleKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get(realUserHeader) != "" {
		http.Error(w, "key management is unavailable while impersonating", http.StatusForbidden)
		return
	}
	if !h.cfg.KeyService.Enabled() {
		http.Error(w, "key management is not configured — set KEY_SERVICE_URL to enable it", http.StatusNotImplemented)
		return
	}

	if r.URL.RawQuery != "" && !(r.Method == http.MethodGet && r.URL.Path == "/me/keys") {
		http.Error(w, "query parameters are not supported", http.StatusBadRequest)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/me/keys")
	path = strings.TrimSuffix(path, "/")
	hdrs := h.identityHeaders(r)

	switch {
	case r.Method == http.MethodGet && path == "":
		h.listKeys(w, r, hdrs)
	case r.Method == http.MethodPost && path == "":
		h.createKey(w, r, hdrs)
	case r.Method == http.MethodDelete && path != "":
		keyID := strings.TrimPrefix(path, "/")
		if !keyIDPattern.MatchString(keyID) {
			http.Error(w, "invalid key ID", http.StatusBadRequest)
			return
		}
		h.revokeKey(w, keyID, hdrs)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *KeysHandler) listKeys(w http.ResponseWriter, r *http.Request, hdrs map[string]string) {
	query := r.URL.Query()
	for name, values := range query {
		if (name != "offset" && name != "status") || len(values) != 1 {
			http.Error(w, "invalid key pagination", 400)
			return
		}
	}
	offset := 0
	if raw, ok := query["offset"]; ok {
		parsed, err := strconv.Atoi(raw[0])
		if err != nil || parsed < 0 || parsed > 1000000 {
			http.Error(w, "invalid key offset", 400)
			return
		}
		offset = parsed
	}
	filters := map[string]any{"username": hdrs[h.cfg.KeyService.UserHeader]}
	switch query.Get("status") {
	case "":
	case "active":
		filters["status"] = []string{"active"}
	case "inactive":
		filters["status"] = []string{"revoked", "expired"}
	default:
		http.Error(w, "invalid key status", 400)
		return
	}
	encoded, _ := json.Marshal(map[string]any{"filters": filters, "pagination": map[string]int{"limit": 50, "offset": offset}})
	h.forward(w, http.MethodPost, "/v1/api-keys/search", hdrs, bytes.NewReader(encoded))
}

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (h *KeysHandler) createKey(w http.ResponseWriter, r *http.Request, hdrs map[string]string) {
	// Identity is derived exclusively from the validated session. MaaS validates
	// the requested lifetime against its administrator-configured maximum.
	var body struct {
		Name         string            `json:"name"`
		Description  string            `json:"description,omitempty"`
		Subscription string            `json:"subscription,omitempty"`
		ExpiresIn    string            `json:"expiresIn,omitempty"`
		Ephemeral    bool              `json:"ephemeral,omitempty"`
		Labels       map[string]string `json:"labels,omitempty"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "invalid key request", 400)
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		http.Error(w, "expected one key request", 400)
		return
	}
	if len(body.Name) > 128 || (!body.Ephemeral && strings.TrimSpace(body.Name) == "") {
		http.Error(w, "invalid key name", 400)
		return
	}
	encoded, _ := json.Marshal(body)
	h.forward(w, http.MethodPost, "/v1/api-keys", hdrs, bytes.NewReader(encoded))
}

func (h *KeysHandler) revokeKey(w http.ResponseWriter, keyID string, hdrs map[string]string) {
	if h.cfg.ManagementAPISecret != "" {
		// Enforce ownership here as well as at MaaS. Even an accidentally
		// privileged default group must not turn a personal route into admin API.
		resp, err := h.proxy(http.MethodGet, "/v1/api-keys/"+keyID, hdrs, nil)
		if err != nil {
			http.Error(w, "failed to reach key service", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		var key struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		}
		if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&key) != nil ||
			key.ID != keyID || key.Username == "" || key.Username != hdrs[h.cfg.KeyService.UserHeader] {
			http.Error(w, "key not found", http.StatusNotFound)
			return
		}
	}
	h.forward(w, http.MethodDelete, "/v1/api-keys/"+keyID, hdrs, nil)
}

func (h *KeysHandler) forward(w http.ResponseWriter, method, path string, hdrs map[string]string, body io.Reader) {
	resp, err := h.proxy(method, path, hdrs, body)
	if err != nil {
		http.Error(w, "failed to reach key service", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if method == http.MethodPost && path == "/v1/api-keys" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		metrics.KeysMintedTotal.Inc()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck // response already committed
}

// HandleWhoAmI echoes the identity the authenticating proxy asserted, which
// the account page uses to decide what to show. While an admin is "viewing
// as" another user, `user` is the impersonated identity and `real_user` the
// admin's own, so the page can render a banner with a way back.
func (h *KeysHandler) HandleWhoAmI(w http.ResponseWriter, r *http.Request) {
	user := r.Header.Get(h.cfg.UserHeader)
	real := r.Header.Get(realUserHeader)
	// real is only set while an admin holds a "view as" claim (RequireAuth
	// writes it), including the "My view as user" self case where real==user.
	impersonating := real != ""

	// Groups arrive as the JSON array string the login flow stored in the
	// session; expose them as a real array (CSV fallback) so pages don't
	// have to guess the encoding.
	var groups []string
	if raw := r.Header.Get(h.cfg.GroupsHeader); raw != "" {
		var gs []string
		if err := json.Unmarshal([]byte(raw), &gs); err != nil {
			for _, g := range strings.Split(raw, ",") {
				if g = strings.TrimSpace(g); g != "" {
					gs = append(gs, g)
				}
			}
		}
		groups = gs
	}
	if groups == nil {
		groups = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"user":              user,
		"groups":            groups,
		"isAdmin":           IsPartnerAdmin(r.Context(), h.cfg, h.store, r),
		"isSuperAdmin":      IsPartnerSuperAdmin(r.Context(), h.cfg, h.store, r),
		"keyServiceEnabled": h.cfg.KeyService.Enabled(),
		"managementEnabled": h.cfg.ManagementAPISecret != "",
		"impersonating":     impersonating,
		"real_user":         "",
		// "First Last" for the identity the page is showing (the impersonated
		// user while an admin is "viewing as", else the caller themselves).
		"display_name": "",
	}
	if h.store != nil && user != "" {
		if p, err := h.store.GetUserProfile(r.Context(), user); err == nil {
			resp["display_name"] = p.DisplayName()
		}
		// Org scope fields (additive): the tabs use isManager to show the
		// /manager entry, and the manager page uses scopeSize for its KPI.
		isManager, scopeSize := WhoAmIScope(r, h.store, h.cfg)
		resp["isManager"] = isManager
		resp["scopeSize"] = scopeSize
		// Quota carrier (additive): the dashboard's over-limit popup/banner
		// renders straight off this — no extra round-trip on boot. A quota
		// lookup failure degrades the banner away, not the whole whoami.
		if q, err := h.store.GetQuotaView(r.Context(), user, IsPartnerSuperAdmin(r.Context(), h.cfg, h.store, r)); err == nil {
			resp["quota"] = q
		} else {
			slog.Error("quota view lookup failed", "user", user, "error", err)
		}
	}
	// githubId backs the redesigned user dashboard's avatar; only available
	// when the kubernetes adapter can read OpenShift users.
	if h.k8sClient != nil && user != "" {
		if users, err := h.k8sClient.GetOpenShiftUsers(r.Context()); err == nil {
			for _, u := range users {
				if u.Name == user && u.GitHubID != "" {
					resp["githubId"] = u.GitHubID
					break
				}
			}
		}
	}
	if impersonating {
		resp["real_user"] = real
		if h.store != nil && real != "" {
			if p, err := h.store.GetUserProfile(r.Context(), real); err == nil {
				resp["real_user_display"] = p.DisplayName()
			}
		}
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck // response already committed
}
