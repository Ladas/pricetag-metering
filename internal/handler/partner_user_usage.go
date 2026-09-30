package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/storage"
)

const (
	partnerUsageMaxUsers = 1000
	partnerUsageMaxRange = 366 * 24 * time.Hour
)

type PartnerUserUsageHandler struct {
	store *storage.Store
}

func NewPartnerUserUsageHandler(store *storage.Store) *PartnerUserUsageHandler {
	return &PartnerUserUsageHandler{store: store}
}

// HandleBatchUserUsage accepts a set of stable user UUIDs and returns their
// usage over the supplied half-open RFC3339 interval [from,to).
func (h *PartnerUserUsageHandler) HandleBatchUserUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		UserIDs []string `json:"user_ids"`
		From    string   `json:"from"`
		To      string   `json:"to"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.UserIDs) == 0 || len(body.UserIDs) > partnerUsageMaxUsers {
		http.Error(w, "user_ids must contain between 1 and 1000 UUIDs", http.StatusBadRequest)
		return
	}
	from, err := time.Parse(time.RFC3339, body.From)
	if err != nil {
		http.Error(w, "from must be an RFC3339 timestamp", http.StatusBadRequest)
		return
	}
	to, err := time.Parse(time.RFC3339, body.To)
	if err != nil {
		http.Error(w, "to must be an RFC3339 timestamp", http.StatusBadRequest)
		return
	}
	if !from.Before(to) || to.Sub(from) > partnerUsageMaxRange || to.After(time.Now()) {
		http.Error(w, "time range must be ordered, at most 366 days, and not in the future", http.StatusBadRequest)
		return
	}
	report, err := h.store.GetPartnerUsersUsageReport(r.Context(), body.UserIDs, from, to)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidPartnerUser) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "usage report unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, report)
}
