package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBatchUserUsageValidatesRequestBeforeDatabaseAccess(t *testing.T) {
	h := NewPartnerUserUsageHandler(nil)
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "missing IDs", body: `{"user_ids":[],"from":"2026-09-01T00:00:00Z","to":"2026-09-02T00:00:00Z"}`, want: http.StatusBadRequest},
		{name: "invalid UUID", body: `{"user_ids":["not-a-uuid"],"from":"2026-09-01T00:00:00Z","to":"2026-09-02T00:00:00Z"}`, want: http.StatusBadRequest},
		{name: "malformed timestamp", body: `{"user_ids":["123e4567-e89b-12d3-a456-426614174000"],"from":"yesterday","to":"2026-09-02T00:00:00Z"}`, want: http.StatusBadRequest},
		{name: "future range", body: `{"user_ids":["123e4567-e89b-12d3-a456-426614174000"],"from":"2999-09-01T00:00:00Z","to":"2999-09-02T00:00:00Z"}`, want: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/usage/reports", strings.NewReader(tc.body))
			recorder := httptest.NewRecorder()
			h.HandleBatchUserUsage(recorder, req)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}
