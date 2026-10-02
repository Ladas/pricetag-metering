package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/dashboard"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

const (
	cookieName   = "metering_session"
	cookieMaxAge = 7 * 24 * time.Hour

	// realUserHeader carries the true session identity while an admin is
	// "viewing as" another user, so the UI can render a banner and the
	// whoami endpoint can report both identities.
	realUserHeader = "X-Forwarded-Real-User"
)

type sessionPayload struct {
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
	// As, when set by an admin via HandleImpersonate, swaps the identity
	// header to this user for the rest of the session.
	As    string `json:"as,omitempty"`
	Exp   int64  `json:"exp"`
	KeyID string `json:"key_id,omitempty"`
}

type AuthHandler struct {
	cfg        config.Config
	secret     []byte
	httpClient *http.Client
	jwt        *jwtLogin
	// orgStore backs the post-login manager redirect: people whose directory
	// record has reports land on /manager, the view they actually came for.
	// Optional — a nil store (or a lookup failure) keeps the old behaviour
	// and lands everyone on /dashboard.
	orgStore *storage.Store
}

// SetOrgStore wires the directory lookup used by the login redirect. Kept a
// setter so login stays constructible without a database (tests, lite
// deployments).
func (h *AuthHandler) SetOrgStore(s *storage.Store) { h.orgStore = s }

func NewAuthHandler(cfg config.Config) (*AuthHandler, error) {
	login, err := newJWTLogin(cfg)
	if err != nil {
		return nil, err
	}
	secret := []byte(os.Getenv("SESSION_SECRET"))
	if login != nil && len(secret) < 32 {
		return nil, fmt.Errorf("JWT login requires a persistent SESSION_SECRET of at least 32 bytes")
	}
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic("failed to generate session secret: " + err.Error())
		}
		slog.Warn("SESSION_SECRET not set — using random secret (sessions won't survive restarts)")
	}
	return &AuthHandler{
		cfg:        cfg,
		secret:     secret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		jwt:        login,
	}, nil
}

func (h *AuthHandler) sign(data []byte) string {
	mac := hmac.New(sha256.New, h.secret)
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *AuthHandler) verify(data []byte, sig string) bool {
	expected := h.sign(data)
	return hmac.Equal([]byte(expected), []byte(sig))
}

func (h *AuthHandler) setCookie(w http.ResponseWriter, username string, groups []string, as string, expires time.Time) {
	payload := sessionPayload{
		Username: username,
		Groups:   groups,
		As:       as,
		Exp:      expires.Unix(),
	}
	if h.jwt != nil {
		payload.KeyID = h.jwt.keyID
	}
	data, _ := json.Marshal(payload)
	sig := h.sign(data)
	value := base64.RawURLEncoding.EncodeToString(data) + "." + sig

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   max(1, int(time.Until(expires).Seconds())),
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *AuthHandler) readCookie(r *http.Request) *sessionPayload {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil
	}
	if !h.verify(data, parts[1]) {
		return nil
	}
	var payload sessionPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	if time.Now().Unix() >= payload.Exp || (h.jwt != nil && payload.KeyID != h.jwt.keyID) {
		return nil
	}
	return &payload
}

func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		data, err := fs.ReadFile(dashboard.FS, "login.html")
		if err != nil {
			http.Error(w, "login page not found", http.StatusInternalServerError)
			return
		}
		if h.jwt != nil {
			page := strings.NewReplacer(
				"Sign in with your MaaS API key", "Sign in with your gateway JWT",
				"Invalid API key. Check your key and try again.", "Invalid or expired JWT. Ask your administrator for a new token.",
				"API Key", "Gateway JWT",
				"sk-oai-...", "Paste your gateway token",
				"Your key was provided by your team lead.", "Use the same token as your harness. Your session lasts at most eight hours and never outlives the token.",
			).Replace(string(data))
			data = []byte(page)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.checkBrowserOrigin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid login form", http.StatusBadRequest)
		return
	}
	apiKey := strings.TrimSpace(r.PostForm.Get("api_key"))
	if apiKey == "" {
		http.Redirect(w, r, "/login?error=invalid", http.StatusFound)
		return
	}
	if h.jwt != nil {
		session, err := h.jwt.validate(apiKey)
		if err != nil {
			http.Redirect(w, r, "/login?error=invalid", http.StatusFound)
			return
		}
		h.setCookie(w, session.Username, nil, "", time.Unix(session.Exp, 0))
		http.Redirect(w, r, h.loginDestination(r, session.Username), http.StatusFound)
		return
	}

	validateURL := os.Getenv("MAAS_VALIDATE_URL")
	if validateURL == "" {
		validateURL = "http://maas-api:8080/internal/v1/api-keys/validate"
	}

	body, _ := json.Marshal(map[string]string{"key": apiKey})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, validateURL, bytes.NewReader(body))
	if err != nil {
		slog.Error("failed to build validate request", "error", err)
		http.Redirect(w, r, "/login?error=invalid", http.StatusFound)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		slog.Error("maas-api validate failed", "error", err)
		http.Redirect(w, r, "/login?error=invalid", http.StatusFound)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Redirect(w, r, "/login?error=invalid", http.StatusFound)
		return
	}

	var result struct {
		Valid    bool     `json:"valid"`
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || !result.Valid || result.Username == "" {
		http.Redirect(w, r, "/login?error=invalid", http.StatusFound)
		return
	}

	slog.Info("user logged in", "username", result.Username)
	h.setCookie(w, result.Username, result.Groups, "", time.Now().Add(cookieMaxAge))
	http.Redirect(w, r, h.loginDestination(r, result.Username), http.StatusFound)
}

// loginDestination is where a successful login lands. Admins and everyone
// without a directory tree keep the Usage page; a manager — someone the
// directory says has reports — goes straight to their team view, which is
// what they came for. Fails closed to /dashboard on any lookup error.
func (h *AuthHandler) loginDestination(r *http.Request, username string) string {
	if h.orgStore == nil {
		return "/dashboard"
	}
	for _, admin := range h.cfg.AdminUsers {
		if username == admin {
			return "/dashboard"
		}
	}
	for _, admin := range h.cfg.SuperAdminUsers {
		if username == admin {
			return "/dashboard"
		}
	}
	if _, isManager, _, err := h.orgStore.ScopeUsernames(r.Context(), username); err == nil && isManager {
		return "/manager"
	}
	return "/dashboard"
}

// HandleImpersonate lets a super-admin open another user's view. With
// ?as=USER the target is stored in the signed session and the browser is
// sent to that user's account page; without it any active impersonation is
// cleared and the browser returns to the dashboard. The check uses the real
// session identity (not the swapped header), so it still works
// mid-impersonation. "View as" is an operator tool — regular admins get the
// org-wide usage view instead, not another person's personal page.
func (h *AuthHandler) HandleImpersonate(w http.ResponseWriter, r *http.Request) {
	// The legacy UI changes impersonation through GET. Keep that optional
	// feature disabled in standalone JWT mode until it has a POST form.
	if h.jwt != nil {
		http.NotFound(w, r)
		return
	}
	session := h.readCookie(r)
	if session == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	isSuperAdmin := false
	for _, admin := range h.cfg.SuperAdminUsers {
		if session.Username == admin {
			isSuperAdmin = true
			break
		}
	}
	if !isSuperAdmin {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	target := strings.TrimSpace(r.URL.Query().Get("as"))
	if target == "" {
		h.setCookie(w, session.Username, session.Groups, "", time.Unix(session.Exp, 0))
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	slog.Info("admin viewing as user", "admin", session.Username, "as", target)
	h.setCookie(w, session.Username, session.Groups, target, time.Unix(session.Exp, 0))
	// /dashboard serves the personal user page while impersonating (the
	// operator dashboard is only shown for a real, non-swapped admin view).
	// ?to= lets the admin land on /manager instead, to see exactly what a
	// manager on their team sees. Strict allowlist — never an open redirect.
	dest := "/dashboard"
	switch r.URL.Query().Get("to") {
	case "/manager", "manager":
		dest = "/manager"
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// RequireAuth gates a handler behind a valid session cookie. On success it
// sets the identity headers so downstream handlers (IsAdmin, whoami, etc.)
// work unchanged.
func (h *AuthHandler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.jwt != nil {
			w.Header().Set("Cache-Control", "no-store")
		}
		session, bearer := h.bearerSession(r)
		if !bearer {
			if !h.checkBrowserOrigin(w, r) {
				return
			}
			session = h.readCookie(r)
		}
		if session == nil {
			if bearer {
				http.Error(w, "invalid caller credential", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		r.Header.Del(h.cfg.UserHeader)
		r.Header.Del(h.cfg.GroupsHeader)
		r.Header.Del(realUserHeader)
		r.Header.Set(h.cfg.UserHeader, session.Username)
		if len(session.Groups) > 0 {
			groups, _ := json.Marshal(session.Groups)
			r.Header.Set(h.cfg.GroupsHeader, string(groups))
		}
		// Admin "view as" mode: swap the identity header to the target user
		// so every downstream handler (per-user scoping, IsAdmin) behaves
		// exactly as it would for that user. The real identity is preserved
		// in realUserHeader for the UI banner and the impersonate-clear
		// endpoint. Only super-admins can hold or activate this claim —
		// HandleImpersonate writes it for super-admins only, and the cookie
		// is HMAC-signed, so a signed-out admin cannot forge one.
		if session.As != "" && IsSuperAdmin(h.cfg, r) {
			r.Header.Set(realUserHeader, session.Username)
			r.Header.Set(h.cfg.UserHeader, session.As)
		}
		next(w, r)
	}
}

// AuthenticatedUser returns the username from the session cookie, or "".
func (h *AuthHandler) AuthenticatedUser(r *http.Request) string {
	if s := h.readCookie(r); s != nil {
		return s.Username
	}
	return ""
}
