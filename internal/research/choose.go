package research

import (
	"fmt"
	"slices"
	"strings"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Choice is the agent a research run goes to or, when no candidate
// qualifies, what the run waits for.
type Choice struct {
	// Agent is nil when no candidate qualifies.
	Agent *aiv1.ResearchAgent
	// Reason says why, e.g. "chosen: claude/claude-sonnet-5-5 (opus: 7-day
	// window at 82 %)", or which candidates are blocked until when.
	Reason string
	// WaitEngine and WaitUntil are the earliest reset or limit end among the
	// blocked candidates; zero with an Agent, and when no candidate can run
	// on the project at all.
	WaitEngine aiv1.Engine
	WaitUntil  time.Time
}

// blocked is a candidate over the threshold or limited, and until when.
type blocked struct {
	note  string
	until time.Time
	// limit tells that note already says until when.
	limit bool
}

// Choose returns the first candidate whose engine is not limited and whose
// fullest usage window, not reset since, is below maxPercent. Candidates the
// project cannot run (an engine it does not use, a model it does not allow)
// are skipped; the reason names them only when no candidate qualifies. The
// chosen effort is capped at agents.maxEffort; engines other than claude get
// none.
func Choose(project *aiv1.AgentProject, candidates []aiv1.ResearchAgent, maxPercent int32, now time.Time) Choice {
	spec := project.Spec.WithDefaults()
	var blocks []blocked
	var skipped []string
	var c Choice
	for _, cand := range candidates {
		o, ok := spec.FindModel(cand.Engine, cand.Model)
		if !ok {
			skipped = append(skipped, unusable(spec, cand))
			continue
		}
		var b blocked
		if l, limited := project.Status.ActiveUsageLimit(o.Engine, now); limited {
			b = blocked{fmt.Sprintf("%s limited until %s", o.Engine, utcMinute(l.Until.Time)), l.Until.Time, true}
		} else if w, ok := project.Status.FullestWindow(o.Engine, now); ok && w.Percent >= maxPercent {
			b = blocked{fmt.Sprintf("%s: %s window at %d %%", agentLabel(o), windowLabel(w.Name), w.Percent), w.ResetsAt.Time, false}
		}
		if b.note == "" {
			a := aiv1.ResearchAgent{Engine: o.Engine, Model: o.Model}
			if o.Engine == aiv1.EngineClaude {
				a.Effort, _ = spec.CapEffort(cand.Effort)
			}
			c = Choice{Agent: &a, Reason: "chosen: " + string(a.Engine) + "/" + a.Model}
			if len(blocks) > 0 {
				c.Reason += " (" + strings.Join(notes(blocks, false), "; ") + ")"
			}
			return c
		}
		if c.WaitUntil.IsZero() || b.until.Before(c.WaitUntil) {
			c.WaitEngine, c.WaitUntil = o.Engine, b.until
		}
		if !slices.Contains(blocks, b) {
			blocks = append(blocks, b)
		}
	}
	if len(candidates) == 0 {
		c.Reason = "no candidates"
		return c
	}
	c.Reason = "no candidate qualifies: " + strings.Join(append(notes(blocks, true), skipped...), "; ")
	return c
}

// notes describe the blocked candidates; withUntil adds when a window
// resets (a limit says it already).
func notes(blocks []blocked, withUntil bool) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if withUntil && !b.limit {
			b.note += " until " + utcMinute(b.until)
		}
		out = append(out, b.note)
	}
	return out
}

// unusable says why the project cannot run a candidate.
func unusable(spec aiv1.AgentProjectSpec, a aiv1.ResearchAgent) string {
	if !slices.ContainsFunc(spec.AllowedModels(), func(o aiv1.ModelOption) bool { return o.Engine == a.Engine }) {
		return fmt.Sprintf("%s/%s: the project does not use %s", a.Engine, a.Model, a.Engine)
	}
	return fmt.Sprintf("%s/%s: not an allowed model", a.Engine, a.Model)
}

// agentLabel is a model's alias, else "<engine>/<model>".
func agentLabel(o aiv1.ModelOption) string {
	if o.Alias != "" {
		return o.Alias
	}
	return string(o.Engine) + "/" + o.Model
}

// windowLabel names a usage window, e.g. seven_day → "7-day",
// seven_day_opus → "7-day opus".
func windowLabel(name string) string {
	for _, p := range [][2]string{{"five_hour", "5-hour"}, {"seven_day", "7-day"}} {
		if rest, ok := strings.CutPrefix(name, p[0]); ok {
			name = p[1] + rest
			break
		}
	}
	return strings.ReplaceAll(name, "_", " ")
}

// utcMinute formats a time for reasons, e.g. "2026-11-13 00:00 UTC".
func utcMinute(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" }

// ParseCandidates parses a comma-separated list of "engine/model[@effort]",
// e.g. "claude/claude-opus-5-5@high,cursor/auto".
func ParseCandidates(s string) ([]aiv1.ResearchAgent, error) {
	var out []aiv1.ResearchAgent
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		rest, effort, _ := strings.Cut(item, "@")
		engine, model, ok := strings.Cut(rest, "/")
		a := aiv1.ResearchAgent{Engine: aiv1.Engine(engine), Model: model, Effort: effort}
		switch {
		case !ok || model == "":
			return nil, fmt.Errorf("%q: want engine/model[@effort]", item)
		case a.Engine != aiv1.EngineClaude && a.Engine != aiv1.EngineCursor:
			return nil, fmt.Errorf("%q: unknown engine %q", item, engine)
		case effort != "" && !aiv1.ValidEffort(effort):
			return nil, fmt.Errorf("%q: the effort %q is no effort level (%s)", item, effort, strings.Join(aiv1.Efforts, ", "))
		case effort != "" && a.Engine != aiv1.EngineClaude:
			return nil, fmt.Errorf("%q: the %s engine has no effort setting", item, engine)
		}
		out = append(out, a)
	}
	return out, nil
}
