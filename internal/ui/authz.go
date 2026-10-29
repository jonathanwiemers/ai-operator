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

// Authorizer decides which projects and researches a principal may see.
// Access is denied unless a rule grants it. Factory rules grant projects,
// research rules (with Research set) researches, never the other way round.
type Authorizer struct {
	mode  AuthMode
	rules []Rule
	// research is on (a database is configured); allowCreate lets the write
	// grant start and delete one-off researches.
	research, allowCreate bool
}

// NewAuthorizer returns the authorizer of the config. With auth mode none
// every project and research is visible, and researches are writable with
// research.allowCreate.
func NewAuthorizer(c *Config) *Authorizer {
	return &Authorizer{mode: c.Auth.Mode, rules: c.Authorization.Rules,
		research: c.Research.Enabled(), allowCreate: c.Research.AllowCreate}
}

// CanView reports whether p may see the AgentProject namespace/name and its
// tasks.
func (a *Authorizer) CanView(p Principal, namespace, name string) bool {
	return a.allows(p, namespace, name, func(Rule) bool { return true })
}

// CanSeeTranscript reports whether p may see the live transcripts (and
// prompts) of the project's runs.
func (a *Authorizer) CanSeeTranscript(p Principal, namespace, name string) bool {
	return a.allows(p, namespace, name, func(r Rule) bool { return r.Transcript })
}

// CanSteer reports whether p may write comments on the project's issues and
// merge requests from the UI.
func (a *Authorizer) CanSteer(p Principal, namespace, name string) bool {
	return a.allows(p, namespace, name, func(r Rule) bool { return r.Steer })
}

// CanReadResearch reports whether p may see the AgentResearch
// namespace/name and its results.
func (a *Authorizer) CanReadResearch(p Principal, namespace, name string) bool {
	return a.allowsResearch(p, func(r Rule) bool { return matchesAny(r.Researches, namespace, name) }, false)
}

// CanWriteResearch reports whether p may start (or delete) the one-off
// research namespace/name.
func (a *Authorizer) CanWriteResearch(p Principal, namespace, name string) bool {
	return a.allowsResearch(p, func(r Rule) bool { return matchesAny(r.Researches, namespace, name) }, true)
}

// SeesFactory reports whether any factory rule matches p, i.e. whether the
// factory is a feature of theirs.
func (a *Authorizer) SeesFactory(p Principal) bool {
	return a.mode == AuthNone || slices.ContainsFunc(a.rules, func(r Rule) bool {
		return r.Research == "" && hasRole(p, r)
	})
}

// ResearchAccess reports whether any research rule lets p read (or write)
// some research.
func (a *Authorizer) ResearchAccess(p Principal) (read, write bool) {
	everyResearch := func(Rule) bool { return true }
	return a.allowsResearch(p, everyResearch, false), a.allowsResearch(p, everyResearch, true)
}

// allows reports whether a factory rule that grants grant matches p and the
// project.
func (a *Authorizer) allows(p Principal, namespace, name string, grant func(Rule) bool) bool {
	if a.mode == AuthNone {
		return true
	}
	return slices.ContainsFunc(a.rules, func(r Rule) bool {
		return r.Research == "" && grant(r) && hasRole(p, r) && matchesAny(r.Projects, namespace, name)
	})
}

// allowsResearch reports whether a research rule matching p and match
// grants read, or write if write is set.
func (a *Authorizer) allowsResearch(p Principal, match func(Rule) bool, write bool) bool {
	switch {
	case !a.research || write && !a.allowCreate:
		return false
	case a.mode == AuthNone:
		return true
	}
	return slices.ContainsFunc(a.rules, func(r Rule) bool {
		return (r.Research == ResearchWrite || r.Research == ResearchRead && !write) && hasRole(p, r) && match(r)
	})
}

// hasRole reports whether p has one of the rule's roles.
func hasRole(p Principal, r Rule) bool {
	return slices.Contains(r.Roles, "*") || slices.ContainsFunc(r.Roles, func(role string) bool {
		return slices.Contains(p.Roles, role)
	})
}

// matchesAny reports whether namespace/name matches one of patterns; no
// patterns match everything.
func matchesAny(patterns []string, namespace, name string) bool {
	return len(patterns) == 0 || slices.ContainsFunc(patterns, func(pattern string) bool {
		ok, _ := path.Match(pattern, namespace+"/"+name)
		return ok
	})
}
