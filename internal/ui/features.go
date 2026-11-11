package ui

import (
	"context"
	"net/http"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// Feature is a top-level section of the UI the user may see.
type Feature struct {
	Name FeatureName `json:"name"`
	// Write: the user may start one-off researches, in Namespace.
	Write     bool   `json:"write,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// FeatureName names a section of the UI.
type FeatureName string

// Features of the UI.
const (
	FeatureNameFactory  FeatureName = "factory"
	FeatureNameResearch FeatureName = "research"
	// FeatureNameHistory: the projects' streams reach back through the
	// database (the research database holds the history).
	FeatureNameHistory FeatureName = "history"
	// FeatureNameChat: Desk chat with the agents (chat writer login and grant).
	FeatureNameChat FeatureName = "chat"
)

// FeaturesResponse is GET /api/v1/features.
type FeaturesResponse struct {
	Features []Feature `json:"features"`
}

// features lists the sections the user may see: the factory with a factory
// rule, research with a database and a research rule, chat with the writer
// login and a chat grant.
func (s *Server) features(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	out := []Feature{}
	if s.authz.SeesFactory(p) {
		out = append(out, Feature{Name: FeatureNameFactory})
		if s.cfg.Research.Enabled() {
			out = append(out, Feature{Name: FeatureNameHistory})
		}
	}
	if s.cfg.ChatEnabled() && s.authz.SeesChat(p) {
		out = append(out, Feature{Name: FeatureNameChat})
	}
	if read, write := s.authz.ResearchAccess(p); read {
		f := Feature{Name: FeatureNameResearch, Write: write}
		if write {
			f.Namespace = s.cfg.Research.Namespace
		}
		out = append(out, f)
	}
	writeJSON(w, http.StatusOK, FeaturesResponse{Features: out})
}

// SetResearchStore connects the research endpoints to the database.
func (s *Server) SetResearchStore(st *store.Store) { s.research.Store(st) }

// SetChatStore connects the chat writer endpoints to the database.
func (s *Server) SetChatStore(st *store.Store) { s.chat.Store(st) }

// Reconnect delays of ConnectResearch.
const (
	minResearchRetry = 5 * time.Second
	maxResearchRetry = 5 * time.Minute
)

// ConnectResearch opens the research store with open, retrying with backoff
// until it succeeds or ctx ends. Until then the research endpoints answer
// 503; the factory does not depend on it.
func (s *Server) ConnectResearch(ctx context.Context, open func(context.Context) (*store.Store, error)) {
	s.connectStore(ctx, open, s.SetResearchStore, "research database")
}

// ConnectChat opens the chat writer store with open, retrying with backoff
// until it succeeds or ctx ends. Until then chat posts answer 503.
func (s *Server) ConnectChat(ctx context.Context, open func(context.Context) (*store.Store, error)) {
	s.connectStore(ctx, open, s.SetChatStore, "chat database")
}

func (s *Server) connectStore(ctx context.Context, open func(context.Context) (*store.Store, error), set func(*store.Store), what string) {
	for delay := minResearchRetry; ; delay = min(2*delay, maxResearchRetry) {
		st, err := open(ctx)
		if err == nil {
			set(st)
			s.log.Info(what + " connected")
			return
		}
		s.log.Error(err, what+" not reachable, retrying", "in", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// researchHandler runs h for logged-in users with the research store: 404
// while research is off or no research rule matches the user, 503 until the
// database is connected. Access to the research itself is h's to check.
func (s *Server) researchHandler(h func(http.ResponseWriter, *http.Request, *store.Store)) http.Handler {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if read, _ := s.authz.ResearchAccess(principalOf(r)); !read || !s.cfg.Research.Enabled() {
			notFound(w)
			return
		}
		st := s.research.Load()
		if st == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the research database is not connected yet"})
			return
		}
		h(w, r, st)
	})
}
