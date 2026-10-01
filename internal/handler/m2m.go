package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

const bearerPrefix = "Bearer "

// RequireM2MAuth protects gateway-to-metering APIs. Partner-facing APIs use
// RequirePartnerAPIAuth below so Service and port-forward access is protected
// independently of the edge.
func RequireM2MAuth(cfg config.Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.M2MAuthRequired {
			next(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		provided := ""
		if strings.HasPrefix(header, bearerPrefix) {
			provided = strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
		}
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.M2MSharedSecret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// RequirePartnerAPIAuth protects one partner API with its endpoint-specific
// bearer secret. An unset secret fails closed: the endpoint is unavailable
// until deployment wiring is complete rather than accidentally unauthenticated.
func RequirePartnerAPIAuth(secret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if secret == "" {
			http.Error(w, "partner API authentication is not configured", http.StatusServiceUnavailable)
			return
		}
		header := r.Header.Get("Authorization")
		provided := ""
		if strings.HasPrefix(header, bearerPrefix) {
			provided = strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
		}
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
