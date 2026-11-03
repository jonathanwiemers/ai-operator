package webhook

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var _ manager.LeaderElectionRunnable = &Server{}

func TestServerServesOnlyHookPathUntilContextEnds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})}
	if !s.NeedLeaderElection() {
		t.Error("the receiver must run on the leader only")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx, ln) }()

	// A client of its own, not http.DefaultClient: Shutdown closes idle
	// connections at once, but waits for one the server still sees as new
	// (accepted, no request read) until it is about 5s old. A transport that
	// raced a dial can hold such a connection in its pool.
	tr := &http.Transport{}
	client := &http.Client{Transport: tr}
	base := "http://" + ln.Addr().String()
	for _, c := range []struct {
		method, path string
		code         int
	}{
		{http.MethodPost, Path, http.StatusAccepted},
		{http.MethodGet, Path, http.StatusMethodNotAllowed},
		{http.MethodPost, "/metrics", http.StatusNotFound},
		{http.MethodPost, Path + "/x", http.StatusNotFound},
	} {
		req, _ := http.NewRequest(c.method, base+c.path, strings.NewReader("{}"))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != c.code {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, resp.StatusCode, c.code)
		}
	}

	// Closes pooled connections, cancels dials in flight and closes those
	// that become idle later, so none keeps Shutdown waiting.
	tr.CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v after the context ended", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop when the context ended")
	}
	// Without keep-alive the request has to dial the closed port.
	after := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	if _, err := after.Post(base+Path, "application/json", strings.NewReader("{}")); err == nil {
		t.Error("still listening after shutdown")
	}
}
