package ui

import (
	"path"
	"slices"
)

// Principal is the logged-in user.
type Principal struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}

// Authorizer decides which projects a principal may see. Access is denied
// unless a rule grants it.
type Authorizer struct {
	mode  AuthMode
	rules []Rule
}

// NewAuthorizer returns the authorizer of the config. With auth mode none
// every project is visible.
func NewAuthorizer(c *Config) *Authorizer {
	return &Authorizer{mode: c.Auth.Mode, rules: c.Authorization.Rules}
}

// CanView reports whether p may see the AgentProject namespace/name and its
// tasks.
func (a *Authorizer) CanView(p Principal, namespace, name string) bool {
	if a.mode == AuthNone {
		return true
	}
	for _, r := range a.rules {
		if !slices.Contains(r.Roles, "*") && !slices.ContainsFunc(r.Roles, func(role string) bool {
			return slices.Contains(p.Roles, role)
		}) {
			continue
		}
		if len(r.Projects) == 0 {
			return true
		}
		for _, pattern := range r.Projects {
			if ok, _ := path.Match(pattern, namespace+"/"+name); ok {
				return true
			}
		}
	}
	return false
}
