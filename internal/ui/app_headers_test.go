package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestAppServiceWorkerAndManifest: the service worker is revalidated on
// every load, the manifest has its media type, and the CSP allows both from
// the app's origin.
func TestAppServiceWorkerAndManifest(t *testing.T) {
	s := &Server{static: fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html>")},
		"sw.js":                {Data: []byte("self.addEventListener('notificationclick', () => {})")},
		"manifest.webmanifest": {Data: []byte(`{"name":"ai-operator Desk"}`)},
		"logo.svg":             {Data: []byte("<svg/>")},
	}}
	h := securityHeaders(false, http.HandlerFunc(s.app))
	get := func(path string) http.Header {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		return rec.Header()
	}
	if sw := get("/sw.js"); sw.Get("Cache-Control") != "no-cache" || !strings.Contains(sw.Get("Content-Type"), "javascript") {
		t.Errorf("sw.js headers: %v", sw)
	}
	m := get("/manifest.webmanifest")
	if m.Get("Content-Type") != "application/manifest+json" || m.Get("Cache-Control") != "" {
		t.Errorf("manifest headers: %v", m)
	}
	for _, d := range []string{"worker-src 'self'", "manifest-src 'self'"} {
		if !strings.Contains(m.Get("Content-Security-Policy"), d) {
			t.Errorf("CSP lacks %q: %s", d, m.Get("Content-Security-Policy"))
		}
	}
	if logo := get("/logo.svg"); logo.Get("Cache-Control") != "" {
		t.Errorf("other files keep their headers: %v", logo)
	}
}
