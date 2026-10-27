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
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != c.code {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, resp.StatusCode, c.code)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v after the context ended", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop when the context ended")
	}
	if _, err := http.Post(base+Path, "application/json", strings.NewReader("{}")); err == nil {
		t.Error("still listening after shutdown")
	}
}
