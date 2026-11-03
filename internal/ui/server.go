package ui

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// Server serves the UI, its API and the login flow.
type Server struct {
	cfg    *Config
	reader client.Reader
	hub    *Hub
	authz  *Authorizer
	oidc   *oidcClient
	// sessions is nil with auth mode none.
	sessions *Sessions
	// static is the built app (index.html, assets/), nil if it was not
	// built into the binary.
	static fs.FS
	log    logr.Logger

	// streams counts the open event streams per user.
	streamsMu sync.Mutex
	streams   map[string]int
	// logs reads the agent pods' logs for transcripts; nil disables them.
	logs LogSource
	// gitlab connects users' GitLab accounts; nil without gitlab.url.
	gitlab *gitlabConn
	// notes rate-limits the comments users post, creates the one-off
	// researches they start; chats rate-limits Desk chat posts.
	notes, creates, chats *rateLimiter
	// writer creates and deletes one-off researches; nil without
	// research.allowCreate.
	writer client.Client
	// research is the reader's store, nil until connected.
	research atomic.Pointer[store.Store]
	// chat is the chat writer's store, nil until connected or without
	// the writer login.
	chat atomic.Pointer[store.Store]
	// historyRetention is how long new chat rows expire after (matches
	// the operator's --history-retention).
	historyRetention time.Duration
}

// SetHistoryRetention sets how long new chat messages are kept.
func (s *Server) SetHistoryRetention(d time.Duration) {
	if d > 0 {
		s.historyRetention = d
	}
}

// SetLogSource enables live transcripts.
func (s *Server) SetLogSource(l LogSource) { s.logs = l }

// NewServer wires the server. reader is a cache-backed client; static holds
// the built single page app.
func NewServer(cfg *Config, reader client.Reader, hub *Hub, static fs.FS, log logr.Logger) (*Server, error) {
	s := &Server{cfg: cfg, reader: reader, hub: hub, authz: NewAuthorizer(cfg), log: log, streams: map[string]int{},
		notes: newRateLimiter(notesPerWindow, notesWindow), creates: newRateLimiter(createsPerWindow, createsWindow),
		chats: newRateLimiter(notesPerWindow, notesWindow), historyRetention: history.DefaultRetention}
	if _, err := fs.Stat(static, "index.html"); err == nil {
		s.static = static
	}
	if cfg.Auth.Mode == AuthOIDC {
		claims, err := NewClaimMapper(cfg.OIDC.Claims)
		if err != nil {
			return nil, err
		}
		if s.sessions, err = NewSessions(cfg.SessionKey, cfg.Secure(), cfg.Auth.SessionTTL.Duration); err != nil {
			return nil, err
		}
		s.oidc = &oidcClient{cfg: cfg, sessions: s.sessions, claims: claims, log: log.WithName("oidc")}
		if cfg.GitLab.URL != "" {
			s.gitlab = &gitlabConn{cfg: cfg, sessions: s.sessions,
				newClient: func(baseURL, token string) gitlab.API { return gitlab.NewOAuth(baseURL, token) },
				newFixed:  func(baseURL, token string) gitlab.API { return gitlab.New(baseURL, token) },
			}
		}
	}
	return s, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.oidc != nil {
		mux.HandleFunc("GET "+pathLogin, s.oidc.login)
		mux.HandleFunc("GET "+pathCallback, s.oidc.callback)
		mux.HandleFunc("POST "+pathLogout, s.oidc.logout)
	}
	if s.gitlab != nil && s.gitlab.oauth() {
		mux.Handle("GET "+pathGitLabLogin, s.authed(s.gitlab.login))
		mux.Handle("GET "+pathGitLabCallback, s.authed(s.gitlab.callback(s)))
		mux.Handle("POST "+pathGitLabLogout, s.authed(s.gitlab.logout))
	}
	mux.Handle("GET /api/v1/gitlab", s.authed(s.gitlabStatus))
	mux.Handle("GET /api/v1/projects/{namespace}/{name}/tasks/{task}/thread", s.authed(s.thread))
	mux.Handle("POST /api/v1/projects/{namespace}/{name}/tasks/{task}/notes", s.authed(s.postNote))
	mux.Handle("GET /api/v1/me", s.authed(s.me))
	mux.Handle("GET /api/v1/features", s.authed(s.features))
	mux.Handle("GET /api/v1/projects", s.authed(s.projects))
	mux.Handle("GET /api/v1/projects/{namespace}/{name}", s.authed(s.project))
	mux.Handle("GET /api/v1/projects/{namespace}/{name}/tasks/{task}", s.authed(s.task))
	mux.Handle("GET /api/v1/projects/{namespace}/{name}/milestones/{iid}", s.authed(s.milestone))
	mux.Handle("GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/thread", s.authed(s.milestoneThread))
	mux.Handle("POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/notes", s.authed(s.postMilestoneNote))
	mux.Handle("GET /api/v1/projects/{namespace}/{name}/tasks/{task}/transcript", s.authed(s.transcript))
	mux.Handle("GET /api/v1/projects/{namespace}/{name}/tasks/{task}/chat", s.authed(s.getChat))
	mux.Handle("POST /api/v1/projects/{namespace}/{name}/tasks/{task}/chat", s.authed(s.postChat))
	mux.Handle("GET /api/v1/events", s.authed(s.events))
	mux.Handle("GET /api/v1/stream", s.authed(s.stream))
	mux.Handle("GET /api/v1/inbox", s.authed(s.inbox))
	mux.Handle("GET /api/v1/projects/{namespace}/{name}/history", s.authed(s.history))
	if s.cfg.Research.Enabled() {
		mux.Handle("GET /api/v1/researches", s.researchHandler(s.researches))
		mux.Handle("GET /api/v1/researches/{namespace}/{name}", s.researchHandler(s.researchDetail))
		mux.Handle("GET /api/v1/researches/{namespace}/{name}/runs", s.researchHandler(s.researchRuns))
		mux.Handle("GET /api/v1/researches/{namespace}/{name}/runs/{id}", s.researchHandler(s.researchRun))
		mux.Handle("GET /api/v1/research/search", s.researchHandler(s.researchSearch))
	}
	// Always registered, so they answer 404 (not 405) without allowCreate.
	mux.Handle("GET /api/v1/research/form", s.writeHandler(s.researchForm))
	mux.Handle("POST /api/v1/researches", s.writeHandler(s.createResearch))
	mux.Handle("DELETE /api/v1/researches/{namespace}/{name}", s.writeHandler(s.deleteResearch))
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	mux.HandleFunc("GET /oauth2/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	mux.HandleFunc("GET /", s.app)
	// Rejects cross-origin requests that change state (the logout, comments,
	// one-off researches), on top of the SameSite cookies.
	var h http.Handler = http.NewCrossOriginProtection().Handler(mux)
	if s.cfg.Auth.Mode == AuthNone {
		h = localHostOnly(h)
	}
	return securityHeaders(s.cfg.Secure(), h)
}

type ctxKey int

const (
	principalKey ctxKey = iota
	expiryKey
)

// authed runs h for logged-in users and answers 401 to everyone else.
func (s *Server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.sessions == nil {
			ctx := context.WithValue(r.Context(), principalKey, Principal{Username: "anonymous"})
			h(w, r.WithContext(ctx))
			return
		}
		sess, ok := s.sessions.Get(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login required", "login": pathLogin})
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, sess.Principal)
		ctx = context.WithValue(ctx, expiryKey, time.Unix(sess.Expires, 0))
		h(w, r.WithContext(ctx))
	})
}

func principalOf(r *http.Request) Principal {
	p, _ := r.Context().Value(principalKey).(Principal)
	return p
}

func sessionExpiry(r *http.Request) time.Time {
	t, _ := r.Context().Value(expiryKey).(time.Time)
	return t
}

// app serves the single page app: files as they are, every other path as
// index.html, which routes in the browser. The app holds no data, so it is
// served without login; it asks for one when the API answers 401.
func (s *Server) app(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		http.Error(w, "The web UI was not built into this binary (make ui, then go build).", http.StatusNotFound)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if st, err := fs.Stat(s.static, name); err == nil && !st.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				// File names carry a content hash.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.static, name)
			return
		}
		// Missing assets are errors; any other path is a page of the app,
		// even with a dot in it (project names may have one).
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	b, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Error(err, "cannot read index.html")
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// localHostOnly refuses requests for other host names. Without login the UI
// relies on listening on localhost only; a page on another site could still
// reach it through a DNS name that resolves to 127.0.0.1 (DNS rebinding),
// but its requests carry that name.
func localHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !isLoopback(strings.Trim(host, "[]")) {
			http.Error(w, "Without login the UI only answers requests for localhost.", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy allows only the app's own scripts, styles and API.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(secure bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
