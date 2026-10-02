package handler

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redhat-et/pricetag-metering/internal/config"
)

var jwtSubject = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,127}$`)

type jwtLogin struct {
	key      *rsa.PublicKey
	keyID    string
	issuer   string
	audience string
	origin   string
}

func newJWTLogin(cfg config.Config) (*jwtLogin, error) {
	if cfg.DashboardJWTKeyFile == "" && cfg.DashboardJWTIssuer == "" && cfg.DashboardJWTAudience == "" {
		return nil, nil
	}
	if cfg.DashboardJWTKeyFile == "" || cfg.DashboardJWTIssuer == "" || cfg.DashboardJWTAudience == "" {
		return nil, errors.New("dashboard JWT login requires public key, issuer and audience")
	}
	origin, err := url.Parse(cfg.DashboardOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil ||
		origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("DASHBOARD_ORIGIN must be an HTTPS origin without path or credentials")
	}
	data, err := os.ReadFile(cfg.DashboardJWTKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read dashboard JWT public key: %w", err)
	}
	key, err := jwt.ParseRSAPublicKeyFromPEM(data)
	if err != nil || key.N.BitLen() < 2048 {
		return nil, errors.New("dashboard JWT key must be an RSA public key of at least 2048 bits")
	}
	// Bind sessions to the whole trust configuration, not just the cookie secret.
	fingerprint := sha256.Sum256(append(append(data, []byte("\x00"+cfg.DashboardJWTIssuer)...), []byte("\x00"+cfg.DashboardJWTAudience)...))
	return &jwtLogin{key: key, keyID: hex.EncodeToString(fingerprint[:]), issuer: cfg.DashboardJWTIssuer,
		audience: cfg.DashboardJWTAudience, origin: cfg.DashboardOrigin}, nil
}

func (j *jwtLogin) validate(raw string) (*sessionPayload, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) { return j.key, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(j.issuer), jwt.WithAudience(j.audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !jwtSubject.MatchString(claims.Subject) || claims.IssuedAt == nil {
		return nil, errors.New("invalid caller credential")
	}
	expires := claims.ExpiresAt.Time
	if maximum := time.Now().Add(8 * time.Hour); expires.After(maximum) {
		expires = maximum
	}
	return &sessionPayload{Username: claims.Subject, Exp: expires.Unix(), KeyID: j.keyID}, nil
}

// checkBrowserOrigin protects JWT-mode form and cookie mutations. Bearer calls
// are handled separately and do not rely on ambient browser credentials.
func (h *AuthHandler) checkBrowserOrigin(w http.ResponseWriter, r *http.Request) bool {
	if h.jwt == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	if r.Header.Get("Origin") != h.jwt.origin {
		http.Error(w, "invalid request origin", http.StatusForbidden)
		return false
	}
	return true
}

func (h *AuthHandler) bearerSession(r *http.Request) (*sessionPayload, bool) {
	if h.jwt == nil || r.Header.Get("Authorization") == "" {
		return nil, false
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || len(raw) > 16384 {
		return nil, true
	}
	session, err := h.jwt.validate(raw)
	if err != nil {
		return nil, true
	}
	return session, true
}

// standalonePage hides integrations that are not part of manual JWT deployments.
// Authentication and API authorization remain enforced independently of the UI.
func standalonePage(data []byte, cfg config.Config) []byte {
	if cfg.DashboardJWTKeyFile == "" {
		return data
	}
	const mode = `<script>window.standaloneJWT=true;</script><style>
#vtab-users,#vtab-keys,#vtab-platform,#my-view-btn,#addUserInviteBtn,
a[href="/routing"],a[href^="/admin/impersonate"],button[onclick^="openInviteFor"]
{display:none!important}</style>`
	page := strings.Replace(string(data), "</head>", mode+"</head>", 1)
	page = strings.ReplaceAll(page, "MaaS Admin Console", "PriceTag Admin")
	page = strings.ReplaceAll(page, "To send their gateway-key invite, press <strong>Invite</strong> on their new row.",
		"An administrator can issue their gateway JWT after linking its subject to this directory entry.")
	page = strings.ReplaceAll(page, "While this service is unreachable the gateway admits traffic (fail-open) — enforcement pauses, it never blocks on an outage.",
		"If metering becomes unavailable, gateway configuration determines whether requests continue.")
	return []byte(page)
}
