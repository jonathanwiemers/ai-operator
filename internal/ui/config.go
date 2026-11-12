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

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
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
	EnvSessionKey         = "AI_OPERATOR_UI_SESSION_KEY"
	EnvClientSecret       = "AI_OPERATOR_UI_OIDC_CLIENT_SECRET"
	EnvGitLabClientSecret = "AI_OPERATOR_UI_GITLAB_CLIENT_SECRET"
	EnvGitLabToken        = "AI_OPERATOR_UI_GITLAB_TOKEN"
	EnvResearchDBUser     = "AI_OPERATOR_UI_RESEARCH_DB_USER"
	EnvResearchDBPassword = "AI_OPERATOR_UI_RESEARCH_DB_PASSWORD"
	EnvChatDBUser         = "AI_OPERATOR_UI_CHAT_DB_USER"
	EnvChatDBPassword     = "AI_OPERATOR_UI_CHAT_DB_PASSWORD"
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
	GitLab        GitLabConfig        `json:"gitlab"`
	Research      ResearchConfig      `json:"research"`

	// Set from the environment.
	SessionKey         string `json:"-"`
	ClientSecret       string `json:"-"`
	GitLabClientSecret string `json:"-"`
	GitLabToken        string `json:"-"`
	ResearchDBUser     string `json:"-"`
	ResearchDBPassword string `json:"-"`
	ChatDBUser         string `json:"-"`
	ChatDBPassword     string `json:"-"`
}

// ResearchConfig is the Research section. It is on exactly when
// Database.Host is set.
type ResearchConfig struct {
	Database DatabaseConfig `json:"database"`
	// Namespace the UI creates one-off researches in.
	Namespace string `json:"namespace,omitempty"`
	// AllowCreate lets users with the research write grant start and delete
	// one-off researches.
	AllowCreate bool `json:"allowCreate,omitempty"`
}

// DatabaseConfig is where the research database is. The UI logs in as the
// reader, with the login from the environment.
type DatabaseConfig struct {
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
	Name string `json:"name"`
	// SSLMode is disable (loopback hosts only), require (the default),
	// verify-ca or verify-full.
	SSLMode string `json:"sslMode,omitempty"`
}

// Enabled reports whether the Research section is on.
func (c ResearchConfig) Enabled() bool { return c.Database.Host != "" }

// ResearchLogin returns the reader's connection to the research database.
func (c *Config) ResearchLogin() store.Config {
	db := c.Research.Database
	return store.Config{Host: db.Host, Port: db.Port, Database: db.Name, SSLMode: db.SSLMode,
		User: c.ResearchDBUser, Password: c.ResearchDBPassword}
}

// ChatEnabled reports whether the Desk chat writer login is configured.
func (c *Config) ChatEnabled() bool {
	return c.Research.Enabled() && c.ChatDBUser != "" && c.ChatDBPassword != ""
}

// ChatLogin returns the chat writer's connection to the research database.
func (c *Config) ChatLogin() store.Config {
	db := c.Research.Database
	return store.Config{Host: db.Host, Port: db.Port, Database: db.Name, SSLMode: db.SSLMode,
		User: c.ChatDBUser, Password: c.ChatDBPassword}
}

// GitLabConfig lets users connect their GitLab account (an OAuth
// application on that GitLab), so the UI shows the issues' and merge
// requests' comments and posts comments in their name. Projects on other
// GitLab hosts get neither.
type GitLabConfig struct {
	// URL of the GitLab, e.g. https://gitlab.com. Empty turns it off.
	URL string `json:"url"`
	// ClientID of the OAuth application (scope api, redirect URI
	// <externalURL>/oauth2/gitlab/callback). Without it, only TokenUser has
	// a connection.
	ClientID string `json:"clientID"`
	// TokenUser is the UI user (the login's user name) a fixed GitLab token
	// (AI_OPERATOR_UI_GITLAB_TOKEN, e.g. a fine-grained personal access
	// token) belongs to: that user is connected without OAuth, and posts
	// with that token. Nobody else may use it.
	TokenUser string `json:"tokenUser"`
}

// AuthConfig selects the authentication mode.
type AuthConfig struct {
	// Mode is oidc (default) or none.
	Mode AuthMode `json:"mode"`
	// SessionTTL is how long a login lasts when the provider returns no
	// refresh token (the login is then not renewed). Roles are read at login,
	// so a role change takes effect at the next login at the latest.
	SessionTTL Duration `json:"sessionTTL"`
	// RenewAfter is how long a session cookie is valid before it is renewed
	// with the refresh token. Default 15m.
	RenewAfter Duration `json:"renewAfter"`
	// IdleTimeout ends a login that was not renewed for this long. Default 168h.
	IdleTimeout Duration `json:"idleTimeout"`
	// MaxLifetime caps a login from its start. 0 (default) leaves it to the
	// provider.
	MaxLifetime Duration `json:"maxLifetime"`
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

// Research grants of a rule.
const (
	ResearchRead  = "read"
	ResearchWrite = "write"
)

// Rule lets users with any of Roles see the projects matching Projects. A
// rule with Research set is a research rule instead: it grants the
// researches matching Researches and no projects.
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
	// Steer lets its roles write comments on the projects' issues and merge
	// requests from the UI, in their own GitLab name.
	Steer bool `json:"steer,omitempty"`
	// Conversation shows the bodies of the comments in the projects'
	// streams (mirrored from GitLab, so readable without GitLab access).
	// Without it a comment shows as who commented where, with its link.
	Conversation bool `json:"conversation,omitempty"`
	// Chat lets its roles post Desk chat messages to the projects' agents
	// (no GitLab token). Default off.
	Chat bool `json:"chat,omitempty"`
	// Research is read (see researches and their results) or write (also
	// start one-off researches and delete them). A rule with it grants no
	// projects and must not set Projects, Transcript, Steer, Conversation
	// or Chat.
	Research string `json:"research,omitempty"`
	// Researches are namespace/name patterns like Projects. Empty matches
	// every research.
	Researches []string `json:"researches,omitempty"`
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
	cfg.GitLabClientSecret = os.Getenv(EnvGitLabClientSecret)
	cfg.GitLabToken = os.Getenv(EnvGitLabToken)
	cfg.ResearchDBUser = os.Getenv(EnvResearchDBUser)
	cfg.ResearchDBPassword = os.Getenv(EnvResearchDBPassword)
	cfg.ChatDBUser = os.Getenv(EnvChatDBUser)
	cfg.ChatDBPassword = os.Getenv(EnvChatDBPassword)
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
	if c.Auth.RenewAfter.Duration == 0 {
		c.Auth.RenewAfter.Duration = 15 * time.Minute
	}
	if c.Auth.IdleTimeout.Duration == 0 {
		c.Auth.IdleTimeout.Duration = 168 * time.Hour
	}
	if len(c.OIDC.Scopes) == 0 {
		c.OIDC.Scopes = []string{"openid", "profile", "email", "offline_access"}
	}
	if c.OIDC.Claims.Username == "" {
		c.OIDC.Claims.Username = DefaultUsernameExpr
	}
	if c.OIDC.Claims.Roles == "" {
		c.OIDC.Claims.Roles = DefaultRolesExpr
	}
	c.ExternalURL = strings.TrimSuffix(c.ExternalURL, "/")
	c.GitLab.URL = strings.TrimSuffix(c.GitLab.URL, "/")
	if c.Research.Enabled() && c.Research.Database.SSLMode == "" {
		c.Research.Database.SSLMode = "require"
	}
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
		a := c.Auth
		switch {
		case a.RenewAfter.Duration <= 0:
			errs = append(errs, errors.New("auth.renewAfter must be greater than 0"))
		case a.IdleTimeout.Duration <= 0:
			errs = append(errs, errors.New("auth.idleTimeout must be greater than 0"))
		case a.RenewAfter.Duration >= a.IdleTimeout.Duration:
			errs = append(errs, errors.New("auth.renewAfter must be less than auth.idleTimeout"))
		}
		if a.MaxLifetime.Duration < 0 {
			errs = append(errs, errors.New("auth.maxLifetime must not be negative"))
		} else if a.MaxLifetime.Duration > 0 && a.MaxLifetime.Duration <= a.RenewAfter.Duration {
			errs = append(errs, errors.New("auth.maxLifetime must be greater than auth.renewAfter when set"))
		}
	default:
		errs = append(errs, fmt.Errorf("auth.mode must be oidc or none, not %q", c.Auth.Mode))
	}
	if c.GitLab.URL != "" {
		u, err := url.Parse(c.GitLab.URL)
		switch {
		case c.Auth.Mode != AuthOIDC:
			errs = append(errs, errors.New("gitlab needs auth.mode oidc: comments are posted in the name of a logged-in user"))
		case err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname()))):
			errs = append(errs, errors.New("gitlab.url must be an https URL, e.g. https://gitlab.com"))
		case c.GitLab.ClientID == "" && c.GitLab.TokenUser == "":
			errs = append(errs, errors.New("gitlab.clientID (an OAuth application) or gitlab.tokenUser (a fixed token) is required with gitlab.url"))
		case c.GitLab.TokenUser != "" && c.GitLabToken == "":
			errs = append(errs, fmt.Errorf("gitlab.tokenUser needs the token in %s", EnvGitLabToken))
		}
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
		switch {
		case r.Research != "" && r.Research != ResearchRead && r.Research != ResearchWrite:
			errs = append(errs, fmt.Errorf("authorization.rules[%d]: research must be read or write, not %q", i, r.Research))
		case r.Research != "" && (len(r.Projects) > 0 || r.Transcript || r.Steer || r.Conversation || r.Chat):
			errs = append(errs, fmt.Errorf("authorization.rules[%d]: a research rule grants no projects; put projects, transcript, steer, conversation and chat into a rule of their own", i))
		case r.Research == "" && len(r.Researches) > 0:
			errs = append(errs, fmt.Errorf("authorization.rules[%d]: researches needs research: read or write", i))
		}
		for _, p := range r.Researches {
			if _, err := path.Match(p, ""); err != nil || strings.Count(p, "/") != 1 {
				errs = append(errs, fmt.Errorf("authorization.rules[%d]: research pattern %q must look like namespace/name (globs allowed)", i, p))
			}
		}
	}
	errs = append(errs, c.validateResearch()...)
	errs = append(errs, c.validateChat()...)
	return errors.Join(errs...)
}

// validateResearch checks the Research section, if it is on.
func (c *Config) validateResearch() []error {
	if !c.Research.Enabled() {
		return nil
	}
	var errs []error
	db := c.Research.Database
	if db.Name == "" {
		errs = append(errs, errors.New("research.database.name is required with research.database.host"))
	}
	if db.Port < 0 || db.Port > 65535 {
		errs = append(errs, fmt.Errorf("research.database.port %d is out of range", db.Port))
	}
	if c.ResearchDBUser == "" || c.ResearchDBPassword == "" {
		errs = append(errs, fmt.Errorf("research.database.host needs the reader login in %s and %s", EnvResearchDBUser, EnvResearchDBPassword))
	}
	switch db.SSLMode {
	case "require", "verify-ca", "verify-full":
	case "disable":
		if !isLoopback(db.Host) {
			errs = append(errs, errors.New("research.database.sslMode disable sends the password in clear text and is only allowed for a loopback host"))
		}
	default:
		errs = append(errs, fmt.Errorf("research.database.sslMode must be disable, require, verify-ca or verify-full, not %q", db.SSLMode))
	}
	if c.Research.AllowCreate && len(validation.IsDNS1123Label(c.Research.Namespace)) > 0 {
		errs = append(errs, fmt.Errorf("research.allowCreate needs research.namespace, a valid namespace name, not %q", c.Research.Namespace))
	}
	return errs
}

// validateChat checks the Desk chat writer login, if set.
func (c *Config) validateChat() []error {
	if c.ChatDBUser == "" && c.ChatDBPassword == "" {
		return nil
	}
	var errs []error
	if c.ChatDBUser == "" || c.ChatDBPassword == "" {
		errs = append(errs, fmt.Errorf("Desk chat needs both %s and %s", EnvChatDBUser, EnvChatDBPassword))
	}
	if !c.Research.Enabled() {
		errs = append(errs, errors.New("Desk chat needs research.database.host (the chat writer uses the same database)"))
	}
	return errs
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
