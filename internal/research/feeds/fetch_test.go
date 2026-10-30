package feeds

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestAllowedIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"2001:4860:4860::8888", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"::ffff:127.0.0.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"fd12::1", false},
		{"169.254.1.1", false},
		{"fe80::1", false},
		{"100.64.0.1", false},
		{"100.127.255.255", false},
		{"0.0.0.0", false},
		{"0.1.2.3", false},
		{"::", false},
		{"224.0.0.1", false},
		{"ff02::1", false},
		{"::ffff:10.0.0.1", false},
		{"::ffff:169.254.1.1", false},
		{"::ffff:100.64.0.1", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("parse %s", tc.ip)
		}
		if got := allowedIP(ip); got != tc.want {
			t.Errorf("allowedIP(%s)=%v want %v", tc.ip, got, tc.want)
		}
	}
}

func testTLSClient(t *testing.T, s *httptest.Server, control func(string, string, syscall.RawConn) error) *Client {
	t.Helper()
	return newClient("test-agent/1", control, s.Client().Transport.(*http.Transport).TLSClientConfig)
}

func TestFetchSuccess(t *testing.T) {
	body, err := os.ReadFile("testdata/rss.xml")
	if err != nil {
		t.Fatal(err)
	}
	var gotUA string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write(body)
	}))
	defer s.Close()
	ents, err := testTLSClient(t, s, nil).Fetch(context.Background(), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 3 {
		t.Fatalf("got %d entries", len(ents))
	}
	if gotUA != "test-agent/1" {
		t.Errorf("User-Agent: %q", gotUA)
	}
}

func TestFetchHTTPRefused(t *testing.T) {
	_, err := NewClient("x").Fetch(context.Background(), "http://example.com/feed")
	if err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Fatalf("got %v", err)
	}
}

func TestFetchNon2xx(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	_, err := testTLSClient(t, s, nil).Fetch(context.Background(), s.URL)
	if err == nil || err.Error() != "HTTP 503" {
		t.Fatalf("got %v", err)
	}
}

func TestFetchBodyCap(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, io.LimitReader(forever{}, MaxBody+1))
	}))
	defer s.Close()
	_, err := testTLSClient(t, s, nil).Fetch(context.Background(), s.URL)
	if err == nil || err.Error() != "feed larger than 4 MiB" {
		t.Fatalf("got %v", err)
	}
}

type forever struct{}

func (forever) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestFetchRedirectHTTP(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/feed", http.StatusFound)
	}))
	defer s.Close()
	_, err := testTLSClient(t, s, nil).Fetch(context.Background(), s.URL)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchTooManyRedirects(t *testing.T) {
	var s *httptest.Server
	s = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.URL+"/r", http.StatusFound)
	}))
	defer s.Close()
	_, err := testTLSClient(t, s, nil).Fetch(context.Background(), s.URL)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchGuardedRefusesLoopback(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<rss version="2.0"><channel></channel></rss>`))
	}))
	defer s.Close()
	_, err := testTLSClient(t, s, guardDial).Fetch(context.Background(), s.URL)
	if err == nil {
		t.Fatal("expected blocked dial")
	}
}

func TestFetchAllOrderAndError(t *testing.T) {
	body, err := os.ReadFile("testdata/rss.xml")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write(body)
		default:
			w.WriteHeader(500)
		}
	}))
	defer s.Close()
	c := newClient("test-agent/1", nil, s.Client().Transport.(*http.Transport).TLSClientConfig)
	res := c.FetchAll(context.Background(), []string{s.URL + "/ok", "http://nope", s.URL + "/bad"})
	if len(res) != 3 {
		t.Fatalf("len %d", len(res))
	}
	if res[0].Err != nil || len(res[0].Entries) != 3 {
		t.Errorf("first: ents=%d err=%v", len(res[0].Entries), res[0].Err)
	}
	if res[1].Err == nil {
		t.Error("second should err")
	}
	if res[2].Err == nil || res[2].Err.Error() != "HTTP 500" {
		t.Errorf("third: %v", res[2].Err)
	}
}
