package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/redhat-et/pricetag-metering/internal/maasapi"
	"github.com/redhat-et/pricetag-metering/internal/storage"
)

const partnerUserKeyGroup = "GE"

type PartnerUsersHandler struct {
	store *storage.Store
	maas  *maasapi.Client
}

func NewPartnerUsersHandler(store *storage.Store, maas *maasapi.Client) *PartnerUsersHandler {
	return &PartnerUsersHandler{store: store, maas: maas}
}

// HandleUsers serves authenticated partner CRUD, directory search, and
// per-user MaaS key minting at /api/v1/users[/{user_id}[/keys]].
func (h *PartnerUsersHandler) HandleUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	const prefix = "/api/v1/users"
	if r.URL.Path == prefix || r.URL.Path == prefix+"/" {
		h.handleCollection(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.EscapedPath(), prefix+"/")
	parts := strings.Split(path, "/")
	userID, err := url.PathUnescape(parts[0])
	if err != nil || userID == "" || strings.Contains(userID, "/") {
		http.Error(w, "invalid user_id path segment", http.StatusBadRequest)
		return
	}
	if len(parts) == 2 && parts[1] == "keys" {
		h.handleKeys(w, r, userID)
		return
	}
	if len(parts) == 2 && parts[1] == "reactivate" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		user, err := h.store.ReactivatePartnerUser(r.Context(), "partner-m2m", userID)
		if err != nil {
			h.userError(w, r, err)
			return
		}
		writeJSON(w, user)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		user, err := h.store.GetPartnerUser(r.Context(), userID)
		if err != nil {
			h.userError(w, r, err)
			return
		}
		writeJSON(w, user)
	case http.MethodPut:
		var body struct {
			Tags map[string]any `json:"tags"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.Tags == nil {
			http.Error(w, "tags is required", http.StatusBadRequest)
			return
		}
		user, err := h.store.UpdatePartnerUser(r.Context(), "partner-m2m", userID, body.Tags)
		if err != nil {
			h.userError(w, r, err)
			return
		}
		writeJSON(w, user)
	case http.MethodDelete:
		h.deactivate(w, r, userID)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *PartnerUsersHandler) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			UserID string         `json:"user_id"`
			Tags   map[string]any `json:"tags"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.UserID == "" || body.Tags == nil {
			http.Error(w, "user_id and tags are required", http.StatusBadRequest)
			return
		}
		user, err := h.store.CreatePartnerUser(r.Context(), "partner-m2m", body.UserID, body.Tags)
		if err != nil {
			h.userError(w, r, err)
			return
		}
		w.Header().Set("Location", "/api/v1/users/"+user.UserID)
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, user)
	case http.MethodGet:
		query := r.URL.Query()
		filters := make(map[string]string)
		for key, values := range query {
			if strings.HasPrefix(key, "tag.") {
				if len(values) != 1 {
					http.Error(w, "each tag filter must have one value", http.StatusBadRequest)
					return
				}
				filters[strings.TrimPrefix(key, "tag.")] = values[0]
			}
		}
		limit, err := queryInt(query.Get("limit"), 50)
		if err != nil {
			http.Error(w, "limit must be an integer", http.StatusBadRequest)
			return
		}
		offset, err := queryInt(query.Get("offset"), 0)
		if err != nil {
			http.Error(w, "offset must be an integer", http.StatusBadRequest)
			return
		}
		includeInactive := false
		if raw := query.Get("include_inactive"); raw != "" {
			includeInactive, err = strconv.ParseBool(raw)
			if err != nil {
				http.Error(w, "include_inactive must be true or false", http.StatusBadRequest)
				return
			}
		}
		for key := range query {
			if key != "limit" && key != "offset" && key != "include_inactive" && !strings.HasPrefix(key, "tag.") {
				http.Error(w, "unsupported query parameter", http.StatusBadRequest)
				return
			}
		}
		page, err := h.store.ListPartnerUsers(r.Context(), filters, includeInactive, limit, offset)
		if err != nil {
			if errors.Is(err, storage.ErrInvalidPartnerUserQuery) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			slog.Error("partner user list failed", "error", err)
			http.Error(w, "partner user list failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, page)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func queryInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func (h *PartnerUsersHandler) handleKeys(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 128 {
		http.Error(w, "name is required and must be at most 128 characters", http.StatusBadRequest)
		return
	}
	var key *maasapi.APIKeyResponse
	var mintErr error
	err := h.store.WithActivePartnerUser(r.Context(), userID, func(user storage.PartnerUser) error {
		email, ok := user.Tags["email"].(string)
		if !ok || email == "" {
			return storage.ErrInvalidPartnerUser
		}
		key, mintErr = h.maas.CreateAPIKey(r.Context(), email, partnerUserKeyGroup, body.Name)
		return mintErr
	})
	if mintErr != nil {
		slog.Error("partner MaaS key mint failed", "user_id", userID, "error", mintErr)
		http.Error(w, "MaaS key mint failed", http.StatusBadGateway)
		return
	}
	if err != nil {
		h.userError(w, r, err)
		return
	}
	// The secret key is returned only by MaaS at mint time. No secret is
	// persisted or logged by Metering; clients must store it immediately.
	writeJSON(w, key)
}

func (h *PartnerUsersHandler) deactivate(w http.ResponseWriter, r *http.Request, userID string) {
	deletion, err := h.store.BeginPartnerUserDeactivation(r.Context(), "partner-m2m", userID)
	if err != nil {
		h.userError(w, r, err)
		return
	}
	for _, username := range deletion.Usernames {
		if err := h.maas.BulkRevokeAPIKeys(r.Context(), username, partnerUserKeyGroup); err != nil {
			slog.Error("partner MaaS key revocation remains pending", "user_id", deletion.UserID, "error", err)
			http.Error(w, "user deactivated; MaaS key revocation is pending", http.StatusBadGateway)
			return
		}
	}
	if err := h.store.CompletePartnerUserKeyRevocation(r.Context(), "partner-m2m", deletion.UserID); err != nil {
		slog.Error("partner user deactivation completion failed", "user_id", deletion.UserID, "error", err)
		http.Error(w, "user deactivated; key revocation completion is pending", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"user_id": deletion.UserID, "active": false, "keys_revoked": true})
}

func (h *PartnerUsersHandler) userError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrPartnerUserNotFound):
		http.Error(w, "user not found", http.StatusNotFound)
	case errors.Is(err, storage.ErrPartnerUserConflict):
		http.Error(w, "user_id or email already exists", http.StatusConflict)
	case errors.Is(err, storage.ErrPartnerUserInactive):
		http.Error(w, "user is inactive", http.StatusConflict)
	case errors.Is(err, storage.ErrPartnerUserRevocationPending):
		http.Error(w, "user key revocation is pending", http.StatusConflict)
	case errors.Is(err, storage.ErrInvalidPartnerUser):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		slog.Error("partner user operation failed", "path", r.URL.Path, "error", err)
		http.Error(w, "partner user operation failed", http.StatusInternalServerError)
	}
}
