package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

const bearerPrefix = "Bearer "

// RequireM2MAuth protects gateway-to-metering APIs. Partner-facing APIs are
// authenticated by their OpenShift Route/AuthPolicy instead.
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
