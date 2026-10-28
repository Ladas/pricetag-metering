package handler

import (
	"net/http"
	"strings"

	"github.com/redhat-et/pricetag-metering/internal/config"
	"github.com/redhat-et/pricetag-metering/internal/maasapi"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

// NewManagementKeysHandler is protected by RequireManagement. It issues an
// onboarding key as the selected user, never as the operator's MaaS identity.
func NewManagementKeysHandler(store *storage.Store, cfg config.Config, maas *maasapi.Client) http.HandlerFunc {
	slots := make(chan struct{}, partnerMintConcurrency)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		if cfg.PartnerUserKeyGroup == "" {
			http.Error(w, "key group not configured", 503)
			return
		}
		var body struct {
			Subject   string `json:"subject"`
			Name      string `json:"name"`
			ExpiresIn string `json:"expiresIn"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.Name) == "" || len(body.Name) > 128 || body.ExpiresIn == "" || len(body.ExpiresIn) > 64 {
			http.Error(w, "name and finite lifetime are required", 400)
			return
		}
		user, err := store.PartnerUserByMaaSUsername(r.Context(), body.Subject)
		if err != nil || !user.Active || user.KeyRevocationPending {
			http.Error(w, "active user required", 409)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "key issuance busy", 429)
			return
		}
		key, err := maas.CreateAPIKeyWithLifetime(r.Context(), body.Subject, cfg.PartnerUserKeyGroup, body.Name, body.ExpiresIn)
		if err != nil {
			http.Error(w, "key service rejected issuance", 502)
			return
		}
		if err = store.RecordPartnerUserKey(r.Context(), r.Header.Get(cfg.UserHeader), user.UserID, key.ID, body.Subject, body.Name); err != nil {
			_, revokeErr := maas.RevokeUserAPIKey(r.Context(), key.ID, body.Subject, cfg.PartnerUserKeyGroup)
			if revokeErr != nil {
				http.Error(w, "key issuance failed; operator reconciliation required", 503)
				return
			}
			http.Error(w, "key issuance cancelled", 409)
			return
		}
		writeJSON(w, key)
	}
}
