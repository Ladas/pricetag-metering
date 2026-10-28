package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementJSONSingleBoundedObject(t *testing.T) {
	for _, body := range []string{`{"name":"alice"} {"name":"admin"}`, `{"name":"` + strings.Repeat("x", 65<<10) + `"}`, `{"unexpected":"value"}`} {
		r := httptest.NewRequest("PUT", "/api/v1/management/users", strings.NewReader(body))
		w := httptest.NewRecorder()
		var value struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &value) || w.Code != 400 {
			t.Fatal("invalid body accepted")
		}
	}
}
