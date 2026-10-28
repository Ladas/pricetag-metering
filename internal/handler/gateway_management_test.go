package handler

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redhat-et/pricetag-metering/internal/config"
)

func TestGatewayAdapterUsesOnlyDedicatedCapability(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "pt-socket-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "adapter.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "synthetic-adapter-capability-for-tests"
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("wrong capability")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-User") != "" {
			t.Error("browser credentials forwarded")
		}
		if r.URL.Path != "/config" {
			t.Error("wrong adapter path")
		}
		_, _ = io.WriteString(w, `{"revision":"fixture"}`)
	})}
	go server.Serve(listener)
	defer server.Close()
	h := NewGatewayManagementHandler(config.Config{GatewayManagementSocket: socket, GatewayManagementSecret: secret})
	r := httptest.NewRequest("GET", "/api/v1/management/gateway/config", nil)
	r.Header.Set("Cookie", "untrusted-session")
	r.Header.Set("X-Forwarded-User", "untrusted")
	r.Header.Set("Authorization", "Bearer user-key")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d", w.Code)
	}
	for _, url := range []string{"/api/v1/management/gateway/config?path=other", "/api/v1/management/gateway/other"} {
		w = httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 404 {
			t.Fatal("unknown operation accepted")
		}
	}
	w = httptest.NewRecorder()
	h(w, httptest.NewRequest("PUT", "/api/v1/management/gateway/config", strings.NewReader(strings.Repeat("x", (256<<10)+1))))
	if w.Code != 413 {
		t.Fatal("oversize request accepted")
	}
}

func TestGatewayAdapterDisabledByDefault(t *testing.T) {
	w := httptest.NewRecorder()
	NewGatewayManagementHandler(config.Config{})(w, httptest.NewRequest("GET", "/api/v1/management/gateway/config", nil))
	if w.Code != 503 {
		t.Fatal("unconfigured adapter enabled")
	}
}

func TestGatewayActivationSurvivesRequestDisconnectAndReportsResult(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "pt-job-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "adapter.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, `{"revision":"updated","configuration":{"models":[]}}`)
	})}
	go server.Serve(listener)
	defer server.Close()
	h := NewGatewayManagementHandler(config.Config{GatewayManagementSocket: socket, GatewayManagementSecret: "synthetic-adapter-capability-for-tests"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("PUT", "/api/v1/management/gateway/config", strings.NewReader(`{"revision":"old"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 202 {
		t.Fatalf("status=%d", w.Code)
	}
	cancel() // The browser connection ends before adapter activation completes.
	<-entered
	duplicate := httptest.NewRecorder()
	h(duplicate, httptest.NewRequest("PUT", "/api/v1/management/gateway/config", strings.NewReader(`{}`)))
	if duplicate.Code != 409 {
		t.Fatal("overlapping activation accepted")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := httptest.NewRecorder()
		h(status, httptest.NewRequest("GET", "/api/v1/management/gateway/status", nil))
		var result gatewayOperation
		if err := json.Unmarshal(status.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.State == "complete" {
			if result.HTTPStatus != 200 {
				t.Fatal("wrong final status")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operation did not complete independently of browser request")
}
