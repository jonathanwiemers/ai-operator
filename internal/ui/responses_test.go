package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteError checks that the error bodies are byte for byte the maps
// they replaced, with each optional field.
func TestWriteError(t *testing.T) {
	cases := []struct {
		name  string
		write func(http.ResponseWriter)
		code  int
		want  any
	}{
		{"message", func(w http.ResponseWriter) { writeError(w, http.StatusNotFound, "not found") },
			http.StatusNotFound, map[string]string{"error": "not found"}},
		{"field", func(w http.ResponseWriter) {
			writeErrorResponse(w, http.StatusBadRequest, ErrorResponse{Error: "project is required", Field: "project"})
		}, http.StatusBadRequest, map[string]string{"error": "project is required", "field": "project"}},
		{"login", func(w http.ResponseWriter) {
			writeErrorResponse(w, http.StatusUnauthorized, ErrorResponse{Error: "login required", Login: pathLogin})
		}, http.StatusUnauthorized, map[string]string{"error": "login required", "login": pathLogin}},
		{"unknown", func(w http.ResponseWriter) {
			writeErrorResponse(w, http.StatusBadRequest, ErrorResponse{Error: "no such follow-up", Unknown: []int{3, 5}})
		}, http.StatusBadRequest, map[string]any{"error": "no such follow-up", "unknown": []int{3, 5}}},
		{"taken", func(w http.ResponseWriter) {
			writeErrorResponse(w, http.StatusConflict, ErrorResponse{Error: "taken", Taken: []int{2}})
		}, http.StatusConflict, map[string]any{"error": "taken", "taken": []int{2}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.write(rec)
			if rec.Code != c.code {
				t.Errorf("status %d, want %d", rec.Code, c.code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type %q", ct)
			}
			want, err := json.Marshal(c.want)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != string(want) {
				t.Errorf("body %s, want %s", got, want)
			}
		})
	}
}
