// Package ui serves the read-only web UI of the operator: a JSON API over
// the AgentProjects and AgentTasks in the cluster, a stream of change events,
// OIDC login and the Vue single page app.
package ui

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// AuthMode selects how users are authenticated.
type AuthMode string

const (
	// AuthOIDC logs users in with an OpenID Connect provider.
	AuthOIDC AuthMode = "oidc"
	// AuthNone serves everything without login. Only allowed on a loopback
	// address, i.e. through kubectl port-forward.
	AuthNone AuthMode = "none"
)

// Environment variables that carry the secrets, so they never end up in the
// config file (a ConfigMap).
const (
	EnvSessionKey   = "AI_OPERATOR_UI_SESSION_KEY"
	EnvClientSecret = "AI_OPERATOR_UI_OIDC_CLIENT_SECRET"
)

// Default claim mappings: the email (or the subject) as user name, the
// "groups" claim as roles.
const (
	DefaultUsernameExpr = `has(claims.email) ? claims.email : claims.sub`
	DefaultRolesExpr    = `has(claims.groups) ? claims.groups : []`
)

// Config is the UI's configuration file.
type Config struct {
	// ExternalURL is the URL users open, e.g. https://ai.example.com. The
	// OIDC redirect URI is ExternalURL + /oauth2/callback.
	ExternalURL string `json:"externalURL"`

	Auth          AuthConfig          `json:"auth"`
	OIDC          OIDCConfig          `json:"oidc"`
	Authorization AuthorizationConfig `json:"authorization"`

	// Set from the environment.
	SessionKey   string `json:"-"`
	ClientSecret string `json:"-"`
}

// AuthConfig selects the authentication mode.
type AuthConfig struct {
	// Mode is oidc (default) or none.
	Mode AuthMode `json:"mode"`
	// SessionTTL is how long a login lasts. Roles are read at login, so a
	// role change takes effect at the next login at the latest.
	SessionTTL Duration `json:"sessionTTL"`
}

// OIDCConfig is the OpenID Connect client.
type OIDCConfig struct {
	IssuerURL string   `json:"issuerURL"`
	ClientID  string   `json:"clientID"`
	Scopes    []string `json:"scopes"`
	Claims    Claims   `json:"claims"`
}

// Claims are CEL expressions over the token's claims (variable "claims",
// the ID token's claims plus those only the userinfo endpoint returns).
type Claims struct {
	// Username must evaluate to a string.
	Username string `json:"username"`
	// Roles must evaluate to a list of strings, a string, or a map whose
	// keys are the roles (as Zitadel's role claims are).
	Roles string `json:"roles"`
}

// AuthorizationConfig grants access to projects by role.
type AuthorizationConfig struct {
	Rules []Rule `json:"rules"`
}

// Rule lets users with any of Roles see the projects matching Projects.
type Rule struct {
	// Roles of which the user needs one. "*" matches every logged-in user.
	Roles []string `json:"roles"`
	// Projects are namespace/name patterns (path.Match syntax, e.g.
	// "team-a/*"). Empty matches every project.
	Projects []string `json:"projects,omitempty"`
	// Transcript also shows the live transcript of the projects' runs: the
	// agent's messages, tool calls and prompts, which can quote code, issue
	// text and CI logs.
	Transcript bool `json:"transcript,omitempty"`
}

// Duration is a time.Duration that reads "12h" from YAML.
type Duration struct{ time.Duration }

// UnmarshalJSON parses a Go duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		d.Duration = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// LoadConfig reads the config file and the secrets from the environment,
// applies defaults and validates the result for the given listen address.
func LoadConfig(file, bindAddress string) (*Config, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.UnmarshalStrict(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	cfg.SessionKey = os.Getenv(EnvSessionKey)
	cfg.ClientSecret = os.Getenv(EnvClientSecret)
	cfg.Default()
	if err := cfg.Validate(bindAddress); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Default fills in unset fields.
func (c *Config) Default() {
	if c.Auth.Mode == "" {
		c.Auth.Mode = AuthOIDC
	}
	if c.Auth.SessionTTL.Duration == 0 {
		c.Auth.SessionTTL.Duration = 12 * time.Hour
	}
	if len(c.OIDC.Scopes) == 0 {
		c.OIDC.Scopes = []string{"openid", "profile", "email"}
	}
	if c.OIDC.Claims.Username == "" {
		c.OIDC.Claims.Username = DefaultUsernameExpr
	}
	if c.OIDC.Claims.Roles == "" {
		c.OIDC.Claims.Roles = DefaultRolesExpr
	}
	c.ExternalURL = strings.TrimSuffix(c.ExternalURL, "/")
}

// Validate refuses configurations that would expose the UI insecurely.
func (c *Config) Validate(bindAddress string) error {
	var errs []error
	switch c.Auth.Mode {
	case AuthNone:
		host, _, err := net.SplitHostPort(bindAddress)
		if err != nil || !isLoopback(host) {
			errs = append(errs, fmt.Errorf("auth.mode none serves without login and needs a loopback bind address (127.0.0.1:<port>, reached with kubectl port-forward), not %q", bindAddress))
		}
	case AuthOIDC:
		u, err := url.Parse(c.ExternalURL)
		switch {
		case c.ExternalURL == "" || err != nil || u.Host == "":
			errs = append(errs, errors.New("externalURL is required, e.g. https://ai.example.com"))
		case u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())):
			errs = append(errs, errors.New("externalURL must use https (http only for localhost)"))
		case u.Path != "" || u.RawQuery != "" || u.Fragment != "":
			errs = append(errs, errors.New("externalURL must not have a path: the UI is served at the root of its own host name"))
		}
		if c.OIDC.IssuerURL == "" {
			errs = append(errs, errors.New("oidc.issuerURL is required"))
		}
		if c.OIDC.ClientID == "" {
			errs = append(errs, errors.New("oidc.clientID is required"))
		}
		if len(c.SessionKey) < 32 {
			errs = append(errs, fmt.Errorf("%s must hold at least 32 characters, e.g. openssl rand -hex 32", EnvSessionKey))
		}
		if !slices.Contains(c.OIDC.Scopes, "openid") {
			errs = append(errs, errors.New(`oidc.scopes must include "openid"`))
		}
	default:
		errs = append(errs, fmt.Errorf("auth.mode must be oidc or none, not %q", c.Auth.Mode))
	}
	for i, r := range c.Authorization.Rules {
		if len(r.Roles) == 0 {
			errs = append(errs, fmt.Errorf("authorization.rules[%d]: roles is empty", i))
		}
		for _, p := range r.Projects {
			if _, err := path.Match(p, ""); err != nil || strings.Count(p, "/") != 1 {
				errs = append(errs, fmt.Errorf("authorization.rules[%d]: project pattern %q must look like namespace/name (globs allowed)", i, p))
			}
		}
	}
	return errors.Join(errs...)
}

// Secure reports whether cookies are sent over https only.
func (c *Config) Secure() bool {
	return strings.HasPrefix(c.ExternalURL, "https://")
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
