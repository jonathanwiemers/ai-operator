package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
