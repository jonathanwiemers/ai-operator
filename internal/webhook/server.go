package webhook

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Server is the manager runnable that serves only POST Path on Addr until
// its context ends.
type Server struct {
	Addr    string
	Handler http.Handler
}

// NeedLeaderElection is true: only the leader runs the controller that acts
// on a wake, so other replicas do not listen and GitLab retries the delivery.
func (s *Server) NeedLeaderElection() bool { return true }

// Start listens on Addr and serves until ctx ends, then shuts down gracefully.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	return s.serve(ctx, ln)
}

func (s *Server) serve(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()
	mux.Handle("POST "+Path, s.Handler)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
