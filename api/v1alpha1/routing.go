package v1alpha1

import (
	"fmt"
	"slices"
	"strings"
)

// Efforts are the effort levels, least first.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// ValidEffort reports whether e is an effort level.
func ValidEffort(e string) bool { return slices.Contains(Efforts, e) }

// DefaultModels are the models an issue may switch to when an AgentProject
// lists none.
func DefaultModels() []ModelOption {
	return []ModelOption{
		{Engine: EngineClaude, Model: "claude-opus-5-5", Alias: "opus"},
		{Engine: EngineClaude, Model: "claude-sonnet-5-5", Alias: "sonnet"},
		{Engine: EngineClaude, Model: "claude-haiku-5-5", Alias: "haiku"},
	}
}

// Name is how commands and labels refer to the model: its alias, else the
// model itself.
func (o ModelOption) Name() string {
	if o.Alias != "" {
		return o.Alias
	}
	return o.Model
}

// String is "<engine>/<name>", the value of a model label.
func (o ModelOption) String() string { return string(o.Engine) + "/" + o.Name() }

// AllowedModels are agents.models plus the engines and models the profiles
// and agents.implement use.
func (s AgentProjectSpec) AllowedModels() []ModelOption {
	out := slices.Clone(s.Agents.Models)
	add := func(r RoleSpec) {
		if r.Engine == "" || r.Model == "" {
			return
		}
		for _, o := range out {
			if o.Engine == r.Engine && o.Model == r.Model {
				return
			}
		}
		out = append(out, ModelOption{Engine: r.Engine, Model: r.Model})
	}
	for _, p := range s.Agents.Profiles {
		add(p.RoleSpec)
	}
	add(s.Agents.Implement)
	return out
}

// FindModel returns the allowed model of an engine by its model name or
// alias.
func (s AgentProjectSpec) FindModel(engine Engine, name string) (ModelOption, bool) {
	for _, o := range s.AllowedModels() {
		if o.Engine == engine && (strings.EqualFold(o.Model, name) || o.Alias != "" && strings.EqualFold(o.Alias, name)) {
			return o, true
		}
	}
	return ModelOption{}, false
}

// ModelLabel returns the label that selects a model.
func (s AgentProjectSpec) ModelLabel(o ModelOption) string {
	return s.Workflow.ModelLabelPrefix + o.String()
}

// EffortLabel returns the label that selects an effort.
func (s AgentProjectSpec) EffortLabel(effort string) string {
	return s.Workflow.EffortLabelPrefix + effort
}

// CapEffort lowers an effort to agents.maxEffort; lowered tells whether it
// did.
func (s AgentProjectSpec) CapEffort(effort string) (string, bool) {
	limit := slices.Index(Efforts, s.Agents.MaxEffort)
	if limit >= 0 && slices.Index(Efforts, effort) > limit {
		return s.Agents.MaxEffort, true
	}
	return effort, false
}

// labelValue returns the value of the issue's first label with prefix.
func labelValue(labels []string, prefix string) (string, bool) {
	for _, l := range labels {
		if v, ok := strings.CutPrefix(l, prefix); ok {
			return v, true
		}
	}
	return "", false
}

// IssueRole returns the implementing role for an issue with the given
// labels: its profile's (or agents.implement), with the engine and model of
// its model label and the effort of its effort label. notes say which labels
// were ignored or lowered, for the run's report.
func (s AgentProjectSpec) IssueRole(labels []string) (role RoleSpec, profile string, notes []string) {
	role, profile = s.ImplementRole(labels)
	if v, ok := labelValue(labels, s.Workflow.ModelLabelPrefix); ok {
		engine, name, _ := strings.Cut(v, "/")
		if o, found := s.FindModel(Engine(engine), name); found {
			role.Engine, role.Model = o.Engine, o.Model
		} else {
			notes = append(notes, fmt.Sprintf("The label `%s%s` names no allowed model, so it is ignored.", s.Workflow.ModelLabelPrefix, v))
		}
	}
	if v, ok := labelValue(labels, s.Workflow.EffortLabelPrefix); ok {
		switch capped, lowered := s.CapEffort(v); {
		case !ValidEffort(v):
			notes = append(notes, fmt.Sprintf("The label `%s%s` is no effort level (%s), so it is ignored.", s.Workflow.EffortLabelPrefix, v, strings.Join(Efforts, ", ")))
		case role.Engine != EngineClaude:
			notes = append(notes, fmt.Sprintf("The effort `%s` is ignored: the %s engine has no effort setting.", v, role.Engine))
		default:
			if lowered {
				notes = append(notes, fmt.Sprintf("The effort `%s` is above the project's maximum, so `%s` is used.", v, capped))
			}
			role.Effort = capped
		}
	}
	return role, profile, notes
}
