package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

type gatewayOperation struct {
	ID         string          `json:"id"`
	State      string          `json:"state"`
	Result     json.RawMessage `json:"result,omitempty"`
	HTTPStatus int             `json:"http_status,omitempty"`
}

// NewGatewayManagementHandler is installed behind RequireManagement. Only the
// backend holds the capability; browser sessions cannot call the host directly.
// Activation runs independently of the initiating request because Praxis must
// restart. Polling reports success only after the adapter's health check passes.
func NewGatewayManagementHandler(cfg config.Config) http.HandlerFunc {
	client := &http.Client{Timeout: 180 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", cfg.GatewayManagementSocket)
		}},
	}
	var mu sync.Mutex
	operation := gatewayOperation{State: "idle"}
	var sequence uint64
	call := func(ctx context.Context, method, route string, body []byte) (int, []byte) {
		request, err := http.NewRequestWithContext(ctx, method, "http://adapter"+route, bytes.NewReader(body))
		if err != nil {
			return 400, []byte(`{"error":"invalid request"}`)
		}
		request.Header.Set("Authorization", "Bearer "+cfg.GatewayManagementSecret)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return 503, []byte(`{"error":"gateway management unavailable"}`)
		}
		defer response.Body.Close()
		result, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil || len(result) > 1<<20 || !json.Valid(result) {
			return 502, []byte(`{"error":"invalid adapter response"}`)
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return 502, []byte(`{"error":"adapter redirect rejected"}`)
		}
		return response.StatusCode, result
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !filepath.IsAbs(cfg.GatewayManagementSocket) || len(cfg.GatewayManagementSecret) < 32 {
			http.Error(w, "gateway management is not configured", 503)
			return
		}
		route := strings.TrimPrefix(r.URL.Path, "/api/v1/management/gateway")
		allowed := (route == "/config" && (r.Method == "GET" || r.Method == "PUT")) || (route == "/discover" && r.Method == "POST") || (route == "/status" && r.Method == "GET")
		if !allowed || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if route == "/status" {
			mu.Lock()
			snapshot := operation
			mu.Unlock()
			writeJSON(w, snapshot)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
		if err != nil {
			http.Error(w, "request too large", 413)
			return
		}
		if r.Method == "PUT" {
			if !json.Valid(body) {
				http.Error(w, "invalid JSON request", 400)
				return
			}
			mu.Lock()
			if operation.State == "running" {
				mu.Unlock()
				http.Error(w, "another activation is running", 409)
				return
			}
			sequence++
			operation = gatewayOperation{ID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), sequence), State: "running"}
			snapshot := operation
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			writeJSON(w, snapshot)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
				defer cancel()
				status, result := call(ctx, http.MethodPut, "/config", body)
				for i := range body {
					body[i] = 0
				}
				state := "failed"
				if status >= 200 && status < 300 {
					state = "complete"
				}
				mu.Lock()
				operation = gatewayOperation{ID: snapshot.ID, State: state, HTTPStatus: status, Result: result}
				mu.Unlock()
			}()
			return
		}
		status, result := call(r.Context(), r.Method, route, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(result)
	}
}
