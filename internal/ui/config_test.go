package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func TestLoadConfigDefaults(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(`
externalURL: https://ai.example.com/
oidc:
  issuerURL: https://zitadel.example.com
  clientID: "123"
authorization:
  rules:
    - roles: [admin]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvSessionKey, strings.Repeat("k", 32))
	cfg, err := LoadConfig(file, ":9080")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Mode != AuthOIDC || cfg.Auth.SessionTTL.Duration != 12*time.Hour {
		t.Errorf("auth defaults: %+v", cfg.Auth)
	}
	if cfg.ExternalURL != "https://ai.example.com" || !cfg.Secure() {
		t.Errorf("externalURL = %q", cfg.ExternalURL)
	}
	if cfg.OIDC.Claims.Username != DefaultUsernameExpr || cfg.OIDC.Claims.Roles != DefaultRolesExpr {
		t.Errorf("claim defaults: %+v", cfg.OIDC.Claims)
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte("auth:\n  mode: none\n  sesionTTL: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(file, "127.0.0.1:9080"); err == nil {
		t.Fatal("want an error for the misspelled field")
	}
}

func TestValidate(t *testing.T) {
	valid := func() *Config {
		c := &Config{
			ExternalURL: "https://ai.example.com",
			OIDC:        OIDCConfig{IssuerURL: "https://idp", ClientID: "c"},
			SessionKey:  strings.Repeat("k", 32),
		}
		c.Default()
		return c
	}
	cases := []struct {
		name    string
		mutate  func(*Config)
		bind    string
		wantErr string
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "http for localhost", mutate: func(c *Config) { c.ExternalURL = "http://localhost:9080" }},
		{name: "http elsewhere", mutate: func(c *Config) { c.ExternalURL = "http://ai.example.com" }, wantErr: "https"},
		{name: "path", mutate: func(c *Config) { c.ExternalURL = "https://ai.example.com/ui" }, wantErr: "path"},
		{name: "no external URL", mutate: func(c *Config) { c.ExternalURL = "" }, wantErr: "externalURL is required"},
		{name: "no issuer", mutate: func(c *Config) { c.OIDC.IssuerURL = "" }, wantErr: "issuerURL"},
		{name: "no client", mutate: func(c *Config) { c.OIDC.ClientID = "" }, wantErr: "clientID"},
		{name: "short key", mutate: func(c *Config) { c.SessionKey = "short" }, wantErr: EnvSessionKey},
		{name: "no openid scope", mutate: func(c *Config) { c.OIDC.Scopes = []string{"email"} }, wantErr: "openid"},
		{name: "unknown mode", mutate: func(c *Config) { c.Auth.Mode = "basic" }, wantErr: "auth.mode"},
		{name: "none on loopback", mutate: func(c *Config) { c.Auth.Mode = AuthNone }, bind: "127.0.0.1:9080"},
		{name: "none on all interfaces", mutate: func(c *Config) { c.Auth.Mode = AuthNone }, wantErr: "loopback"},
		{name: "rule without roles", mutate: func(c *Config) { c.Authorization.Rules = []Rule{{}} }, wantErr: "roles is empty"},
		{
			name:    "bad project pattern",
			mutate:  func(c *Config) { c.Authorization.Rules = []Rule{{Roles: []string{"a"}, Projects: []string{"shop"}}} },
			wantErr: "namespace/name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mutate(c)
			bind := tc.bind
			if bind == "" {
				bind = ":9080"
			}
			err := c.Validate(bind)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadConfigResearch(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(`
auth:
  mode: none
research:
  database:
    host: db.example.svc
    port: 5433
    name: ai_operator
  namespace: agents
  allowCreate: true
authorization:
  rules:
    - roles: [readers]
      research: read
      researches: ["agents/*"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvResearchDBUser, "research_reader")
	t.Setenv(EnvResearchDBPassword, "secret")
	cfg, err := LoadConfig(file, "127.0.0.1:9080")
	if err != nil {
		t.Fatal(err)
	}
	want := store.Config{Host: "db.example.svc", Port: 5433, Database: "ai_operator", SSLMode: "require", User: "research_reader", Password: "secret"}
	if got := cfg.ResearchLogin(); got != want {
		t.Errorf("login = %+v, want %+v", got, want)
	}
	if !cfg.Research.Enabled() || !cfg.Research.AllowCreate || cfg.Research.Namespace != "agents" {
		t.Errorf("research = %+v", cfg.Research)
	}
}

func TestValidateResearch(t *testing.T) {
	valid := func() *Config {
		c := &Config{
			Auth:               AuthConfig{Mode: AuthNone},
			Research:           ResearchConfig{Database: DatabaseConfig{Host: "db.example.svc", Name: "ai_operator"}},
			ResearchDBUser:     "research_reader",
			ResearchDBPassword: "secret",
		}
		c.Default()
		return c
	}
	rule := func(r Rule) func(*Config) {
		return func(c *Config) { c.Authorization.Rules = []Rule{r} }
	}
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "off ignores the rest", mutate: func(c *Config) { c.Research = ResearchConfig{AllowCreate: true}; c.ResearchDBUser = "" }},
		{name: "no name", mutate: func(c *Config) { c.Research.Database.Name = "" }, wantErr: "research.database.name"},
		{name: "no user", mutate: func(c *Config) { c.ResearchDBUser = "" }, wantErr: EnvResearchDBUser},
		{name: "no password", mutate: func(c *Config) { c.ResearchDBPassword = "" }, wantErr: EnvResearchDBPassword},
		{name: "bad port", mutate: func(c *Config) { c.Research.Database.Port = 70000 }, wantErr: "port"},
		{name: "verify-full", mutate: func(c *Config) { c.Research.Database.SSLMode = "verify-full" }},
		{name: "unknown sslMode", mutate: func(c *Config) { c.Research.Database.SSLMode = "prefer" }, wantErr: "sslMode must be"},
		{name: "disable remote", mutate: func(c *Config) { c.Research.Database.SSLMode = "disable" }, wantErr: "loopback"},
		{name: "disable on loopback", mutate: func(c *Config) {
			c.Research.Database.Host, c.Research.Database.SSLMode = "127.0.0.1", "disable"
		}},
		{name: "disable on localhost", mutate: func(c *Config) {
			c.Research.Database.Host, c.Research.Database.SSLMode = "localhost", "disable"
		}},
		{name: "allowCreate without namespace", mutate: func(c *Config) { c.Research.AllowCreate = true }, wantErr: "research.namespace"},
		{name: "allowCreate with bad namespace", mutate: func(c *Config) { c.Research.AllowCreate, c.Research.Namespace = true, "Agents" }, wantErr: "research.namespace"},
		{name: "allowCreate", mutate: func(c *Config) { c.Research.AllowCreate, c.Research.Namespace = true, "agents" }},
		{name: "read rule", mutate: rule(Rule{Roles: []string{"a"}, Research: ResearchRead, Researches: []string{"agents/*"}})},
		{name: "write rule", mutate: rule(Rule{Roles: []string{"a"}, Research: ResearchWrite})},
		{name: "unknown grant", mutate: rule(Rule{Roles: []string{"a"}, Research: "admin"}), wantErr: "read or write"},
		{name: "research with projects", mutate: rule(Rule{Roles: []string{"a"}, Research: ResearchRead, Projects: []string{"a/b"}}), wantErr: "grants no projects"},
		{name: "research with transcript", mutate: rule(Rule{Roles: []string{"a"}, Research: ResearchRead, Transcript: true}), wantErr: "grants no projects"},
		{name: "research with steer", mutate: rule(Rule{Roles: []string{"a"}, Research: ResearchWrite, Steer: true}), wantErr: "grants no projects"},
		{name: "research with chat", mutate: rule(Rule{Roles: []string{"a"}, Research: ResearchRead, Chat: true}), wantErr: "grants no projects"},
		{name: "researches without research", mutate: rule(Rule{Roles: []string{"a"}, Researches: []string{"a/b"}}), wantErr: "needs research"},
		{name: "bad research pattern", mutate: rule(Rule{Roles: []string{"a"}, Research: ResearchRead, Researches: []string{"news"}}), wantErr: "namespace/name"},
		{name: "research rule without roles", mutate: rule(Rule{Research: ResearchRead}), wantErr: "roles is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mutate(c)
			err := c.Validate("127.0.0.1:9080")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
