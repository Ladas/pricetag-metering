package handler

import (
	"github.com/redhat-et/pricetag-metering/internal/dashboard"
	"io/fs"
	"net/http"
	"strings"
)

func ServeManagement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	data, err := fs.ReadFile(dashboard.FS, "management.html")
	if err != nil {
		http.Error(w, "page unavailable", 500)
		return
	}
	controls, err := fs.ReadFile(dashboard.FS, "management-key-controls.js")
	if err != nil {
		http.Error(w, "key controls unavailable", 500)
		return
	}
	data = []byte(strings.Replace(string(data), "/* KEY_CONTROLS */", string(controls), 1))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	_, _ = w.Write(data)
}
