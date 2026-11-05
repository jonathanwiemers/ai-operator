package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

type featuresResponse struct {
	Features []Feature `json:"features"`
}

func withResearch(c *Config, allowCreate bool) *Config {
	c.Research = ResearchConfig{Database: DatabaseConfig{Host: "db.example.svc", Name: "ai_operator"}, Namespace: "agents", AllowCreate: allowCreate}
	c.ResearchDBUser, c.ResearchDBPassword = "research_reader", "secret"
	return c
}

func TestFeaturesAuthNone(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want []Feature
	}{
		{"without a database", &Config{Auth: AuthConfig{Mode: AuthNone}}, []Feature{{Name: FeatureNameFactory}}},
		{"with a database", withResearch(&Config{Auth: AuthConfig{Mode: AuthNone}}, false),
			[]Feature{{Name: FeatureNameFactory}, {Name: FeatureNameHistory}, {Name: FeatureNameResearch}}},
		{"with allowCreate", withResearch(&Config{Auth: AuthConfig{Mode: AuthNone}}, true),
			[]Feature{{Name: FeatureNameFactory}, {Name: FeatureNameHistory}, {Name: FeatureNameResearch, Write: true, Namespace: "agents"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, tc.cfg)
			var got featuresResponse
			if code := env.get(t, "/api/v1/features", &got); code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			if !reflect.DeepEqual(got.Features, tc.want) {
				t.Errorf("features = %+v, want %+v", got.Features, tc.want)
			}
		})
	}
}

func TestFeaturesOIDC(t *testing.T) {
	rules := []Rule{
		{Roles: []string{"shop-devs"}, Projects: []string{"team-a/shop"}},
		{Roles: []string{"readers"}, Research: ResearchRead},
		{Roles: []string{"writers"}, Research: ResearchWrite, Researches: []string{"agents/*"}},
	}
	cases := []struct {
		name     string
		research bool
		roles    []string
		want     []Feature
	}{
		{"factory only", true, []string{"shop-devs"}, []Feature{{Name: FeatureNameFactory}, {Name: FeatureNameHistory}}},
		{"research reader", true, []string{"readers"}, []Feature{{Name: FeatureNameResearch}}},
		{"both", true, []string{"shop-devs", "writers"},
			[]Feature{{Name: FeatureNameFactory}, {Name: FeatureNameHistory}, {Name: FeatureNameResearch, Write: true, Namespace: "agents"}}},
		{"research rules without a database", false, []string{"readers", "writers"}, []Feature{}},
		{"no rule", true, []string{"other"}, []Feature{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := oidcConfig(rules...)
			if tc.research {
				withResearch(cfg, true)
			}
			env := newTestEnv(t, cfg)
			if code := env.get(t, "/api/v1/features", nil); code != http.StatusUnauthorized {
				t.Fatalf("before login: status %d", code)
			}
			roles := map[string]any{}
			for _, r := range tc.roles {
				roles[r] = map[string]any{"1": "acme"}
			}
			env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
			env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": roles}
			env.login(t, "/")
			var got featuresResponse
			if code := env.get(t, "/api/v1/features", &got); code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			if !reflect.DeepEqual(got.Features, tc.want) {
				t.Errorf("features = %+v, want %+v", got.Features, tc.want)
			}
		})
	}
}

func TestResearchHandlerWaitsForStore(t *testing.T) {
	serve := func(s *Server) int {
		h := s.researchHandler(func(w http.ResponseWriter, _ *http.Request, st *store.Store) {
			if st == nil {
				t.Error("handler called without a store")
			}
			w.WriteHeader(http.StatusNoContent)
		})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/researches", nil))
		return rec.Code
	}

	off := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	if code := serve(off.server); code != http.StatusNotFound {
		t.Errorf("research off: status %d, want 404", code)
	}

	env := newTestEnv(t, withResearch(&Config{Auth: AuthConfig{Mode: AuthNone}}, false))
	if code := serve(env.server); code != http.StatusServiceUnavailable {
		t.Errorf("before the store connects: status %d, want 503", code)
	}
	// The factory does not wait for the database.
	if code := env.get(t, "/api/v1/projects", nil); code != http.StatusOK {
		t.Errorf("projects before the store connects: status %d", code)
	}

	opened := &store.Store{}
	env.server.ConnectResearch(context.Background(), func(context.Context) (*store.Store, error) { return opened, nil })
	if code := serve(env.server); code != http.StatusNoContent {
		t.Errorf("after the store connected: status %d, want 204", code)
	}
}

func TestConnectResearchStopsWithContext(t *testing.T) {
	env := newTestEnv(t, withResearch(&Config{Auth: AuthConfig{Mode: AuthNone}}, false))
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	env.server.ConnectResearch(ctx, func(context.Context) (*store.Store, error) {
		calls++
		cancel()
		return nil, errors.New("connection refused")
	})
	if calls != 1 || env.server.research.Load() != nil {
		t.Errorf("calls = %d, store = %v", calls, env.server.research.Load())
	}
}
