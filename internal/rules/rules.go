// Package rules reads the standing rules of a milestone: lines
// "<prefix> rule R<n>: <kind> <text>" in its description. Like the owners
// line they are for the operator, not part of the spec. A rule is a switch
// (a per-milestone value of a setting from a fixed list), a rule for the
// pilot, or a rule for the prompts of the milestone's implementers and
// reviewers. No rule can widen what the operator or its agents may do.
package rules

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// The kinds of rules.
const (
	Switch = "switch"
	Pilot  = "pilot"
	Prompt = "prompt"
)

// Rule is a standing rule.
type Rule struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Problem says why the rule is not applied; "" for an active rule.
	Problem string `json:"problem,omitempty"`
}

// Active reports whether r is applied.
func (r Rule) Active() bool { return r.Problem == "" }

func line(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(prefix) + `[ \t]+rule(?:[ \t]+(.*?))?[ \t]*\r?(?:\n|\z)`)
}

var (
	ruleRE   = regexp.MustCompile(`(?i)^(R[0-9]+)[ \t]*:[ \t]*(switch|pilot|prompt)\b[ \t]*:?[ \t]*(.*)$`)
	switchRE = regexp.MustCompile(`^([A-Za-z.]{1,60})[ \t]*=[ \t]*(\S{1,20})$`)
	idRE     = regexp.MustCompile(`^R[0-9]+$`)
	idLineRE = regexp.MustCompile(`(?i)^(R[0-9]+)[ \t]*:`)
)

// idOf is the ID a rule line starts with ("R2"), "" for none.
func idOf(l string) string {
	if m := idLineRE.FindStringSubmatch(l); m != nil {
		return strings.ToUpper(m[1])
	}
	return ""
}

// Spec is a milestone description without the operator's lines (owners,
// owner-subjects, rules, budget, followup-of): the spec the planner and the
// agents get, and what is hashed for re-planning.
func Spec(description, prefix string) string {
	d := notify.WithoutOwnerSubjects(notify.WithoutOwners(description, prefix), prefix)
	d = notify.WithoutLines(notify.WithoutLines(d, line(prefix)), budgetLine(prefix))
	return notify.WithoutLines(d, followUpLine(prefix))
}

// Lines returns the rule lines of a description without "<prefix> rule ".
func Lines(description, prefix string) []string {
	var out []string
	for _, m := range line(prefix).FindAllStringSubmatch(description, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// Parse reads rule lines and says which are not applied, and why: not a
// rule, a switch that is not allowed, a used ID, two switches that
// conflict (neither applies until a human decides).
func Parse(lines []string) []Rule {
	var out []Rule
	for _, l := range lines {
		// A line with an ID but no kind keeps its ID: it is not used again.
		r := Rule{ID: idOf(l), Text: l, Problem: "not a rule: write `R<n>: switch <setting>=<value>`, `R<n>: pilot <text>` or `R<n>: prompt <text>`"}
		if m := ruleRE.FindStringSubmatch(l); m != nil {
			r = Rule{ID: r.ID, Kind: strings.ToLower(m[2]), Text: m[3]}
			if err := Check(r.Kind, r.Text); err != nil {
				r.Problem = err.Error()
			}
		}
		if r.ID != "" && slices.ContainsFunc(out, func(o Rule) bool { return o.ID == r.ID }) {
			r.Problem = r.ID + " is used by an earlier rule"
		}
		out = append(out, r)
	}
	// Two switches of one setting: neither applies until a human decides.
	conflicts := map[int][]string{}
	for i, r := range out {
		for j, o := range out {
			if i != j && r.Active() && o.Active() && r.Kind == Switch && o.Kind == Switch && settingOf(r.Text) == settingOf(o.Text) {
				conflicts[i] = append(conflicts[i], o.ID)
			}
		}
	}
	for i, ids := range conflicts {
		out[i].Problem = fmt.Sprintf("conflicts with %s: ask the pilot or edit the description", strings.Join(ids, ", "))
	}
	return out
}

// For returns spec with the active switches of a milestone description.
func For(spec aiv1.AgentProjectSpec, description, prefix string) aiv1.AgentProjectSpec {
	return Apply(spec, Parse(Lines(description, prefix)))
}

// Apply returns spec with the active switches of rs; spec is not changed.
func Apply(spec aiv1.AgentProjectSpec, rs []Rule) aiv1.AgentProjectSpec {
	for _, r := range rs {
		if r.Active() && r.Kind == Switch {
			key, v, _ := parseSwitch(r.Text)
			switches[key].set(&spec, v)
		}
	}
	return spec
}

// Check checks a rule of kind with text: a known kind, some text, and for
// a switch a setting from the list with a value it allows.
func Check(kind, text string) error {
	switch {
	case kind != Switch && kind != Pilot && kind != Prompt:
		return fmt.Errorf("a rule is a switch, a pilot rule or a prompt rule")
	case strings.TrimSpace(text) == "":
		return fmt.Errorf("the rule is empty")
	case kind == Switch:
		_, _, err := parseSwitch(text)
		return err
	}
	return nil
}

// ValidID reports whether id names a rule ("R2").
func ValidID(id string) bool { return idRE.MatchString(id) }

// Add appends a rule with the next free ID to a description; it returns
// the new description and the ID.
func Add(description, prefix, kind, text string) (string, string) {
	n := 0
	for _, l := range Lines(description, prefix) {
		if id := idOf(l); id != "" {
			k, _ := strconv.Atoi(id[1:])
			n = max(n, k)
		}
	}
	id := "R" + strconv.Itoa(n+1)
	description = strings.TrimRight(description, " \t\r\n")
	lines := strings.Split(description, "\n")
	switch {
	case description == "":
	case line(prefix).MatchString(lines[len(lines)-1]):
		description += "\n" // next to the other rules
	default:
		description += "\n\n"
	}
	return description + prefix + " rule " + id + ": " + kind + " " + text, id
}

// Remove removes the lines of rule id from a description.
func Remove(description, prefix, id string) string {
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(prefix) + `[ \t]+rule[ \t]+(?i:` + regexp.QuoteMeta(id) + `)[ \t]*:.*?\r?(?:\n|\z)`)
	return notify.WithoutLines(description, re)
}

// setting is a setting a switch may change per milestone, with the
// highest value it may set (-1: on or off).
type setting struct {
	max int
	set func(s *aiv1.AgentProjectSpec, v int)
}

func i32(v int) *int32 { n := int32(v); return &n }

// switches are the settings a rule may switch. Budgets and limits, the
// merge gate, the freeze, the owners and the pilot's autonomy are none of
// them; the counts are capped, and maxRunsPerTask still applies.
var switches = map[string]setting{
	"ci.maxFixRounds":      {5, func(s *aiv1.AgentProjectSpec, v int) { s.CI.MaxFixRounds = i32(v) }},
	"ci.retryFailedJobs":   {-1, func(s *aiv1.AgentProjectSpec, v int) { b := v == 1; s.CI.RetryFailedJobs = &b }},
	"review.retries":       {5, func(s *aiv1.AgentProjectSpec, v int) { s.Agents.Review.Retries = i32(v) }},
	"review.maxIterations": {5, func(s *aiv1.AgentProjectSpec, v int) { s.Agents.Review.MaxIterations = i32(v) }},
}

// Switches lists the settings a rule may switch, with their values.
func Switches() []string {
	keys := make([]string, 0, len(switches))
	for k, s := range switches {
		if s.max < 0 {
			keys = append(keys, fmt.Sprintf("`%s` (true or false)", k))
		} else {
			keys = append(keys, fmt.Sprintf("`%s` (0 to %d)", k, s.max))
		}
	}
	slices.Sort(keys)
	return keys
}

// Setting is the setting of a switch rule's text, "" for none.
func Setting(text string) string { return settingOf(text) }

func settingOf(text string) string {
	key, _, _ := parseSwitch(text)
	return key
}

// parseSwitch reads "<setting>=<value>".
func parseSwitch(text string) (string, int, error) {
	m := switchRE.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", 0, fmt.Errorf("a switch is `<setting>=<value>`, one of %s", strings.Join(Switches(), ", "))
	}
	key := m[1]
	s, ok := switches[key]
	if !ok {
		for _, p := range []string{"pilot.", "lifecycle.", "agents.", "mainHealth.", "maxConcurrentRuns", "ci.disabled", "review.reviewers"} {
			if strings.HasPrefix(strings.ToLower(key), strings.ToLower(p)) {
				return "", 0, fmt.Errorf("`%s` cannot be switched by a rule: rules do not change the merge gate, the freeze, budgets and limits, "+
					"the owners, the pilot's autonomy or project-wide settings", key)
			}
		}
		return "", 0, fmt.Errorf("`%s` is no setting a rule can switch; these are: %s", key, strings.Join(Switches(), ", "))
	}
	if s.max < 0 {
		switch strings.ToLower(m[2]) {
		case "true", "on":
			return key, 1, nil
		case "false", "off":
			return key, 0, nil
		}
		return "", 0, fmt.Errorf("`%s` is true or false", key)
	}
	v, err := strconv.Atoi(m[2])
	if err != nil || v < 0 || v > s.max {
		return "", 0, fmt.Errorf("`%s` is a number from 0 to %d", key, s.max)
	}
	return key, v, nil
}
