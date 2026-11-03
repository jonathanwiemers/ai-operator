package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// labelProject is the controller's LabelProject.
const labelProject = "ai.wiemers.me/project"

// runPilot answers the owners' messages to a milestone's pilot, one turn of
// its session each, and assesses the events the operator woke it for. The
// replies are posted where the messages were written, the assessments on
// the tracking issue; proposals are recorded for the operator, which runs
// them once an owner accepts. Nothing else changes. Once autopilot is done,
// the run asked for writes the retrospective instead (runRetrospective).
func (r *Runner) runPilot(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (*Outcome, error) {
	if task.Spec.Milestone == nil {
		return nil, fmt.Errorf("coordinate task has no milestone")
	}
	if rq := task.Spec.Source.Retrospective; rq > task.Status.Run.ObservedRunRequest && rq <= task.Spec.RunRequest {
		return r.runRetrospective(ctx, gl, task, project, spec)
	}
	out := &Outcome{Result: aiv1.RunResultSucceeded, Summary: "no message to answer"}
	var msgs []aiv1.PilotMessage
	for _, m := range task.Spec.Source.PilotMessages {
		if m.Request > task.Status.Run.ObservedRunRequest && m.Request <= task.Spec.RunRequest {
			msgs = append(msgs, m)
		}
	}
	var events []aiv1.PilotEvent
	for _, e := range task.Spec.Source.PilotEvents {
		if e.Request > task.Status.Run.ObservedRunRequest && e.Request <= task.Spec.RunRequest {
			events = append(events, e)
		}
	}
	if len(msgs)+len(events) == 0 {
		return out, nil
	}
	st := r.loadState()
	s := r.step(ctx, "read milestone state", "messages", len(msgs))
	state, stateOf, err := r.pilotState(ctx, gl, task, project, spec)
	if err != nil {
		// The run's request is done either way: say so instead of dropping
		// the messages.
		for _, m := range msgs {
			r.pilotReply(ctx, gl, spec, m, "The pilot could not read the milestone's state, so it cannot answer; ask again in a moment.")
		}
		// The events' own data is still worth an assessment.
		aerr := r.assessEvents(ctx, gl, task, spec, fmt.Sprintf("(the milestone's state could not be read: %v)", err), nil, out, events, &st)
		if _, limited := AsUsageLimit(aerr); limited {
			return out, aerr
		}
		return out, s.fail(err)
	}
	s.done(fmt.Sprintf("%d bytes", len(state)))
	role := spec.Agents.Pilot
	// A turn that failed does not hold up the others; the run reports it.
	var failed error
	for _, m := range msgs {
		// Answered before the run was cut off or deferred.
		if slices.Contains(st.PilotAnswered, m.NoteID) {
			continue
		}
		left := pilotLeft(spec, task, out)
		reply := fmt.Sprintf("The pilot's budget for this milestone ($%s, `pilot.maxBudgetUSD`) is used up, so it does not answer any more.", spec.Pilot.MaxBudgetUSD)
		if left >= 0.01 {
			inv := roleInvocation(role, pilotSystem(task), readOnlyTools, pilotSchema, true)
			inv.Role = "pilot"
			inv.Resume = st.session("pilot", role.Engine)
			inv.MaxBudgetUSD = strconv.FormatFloat(left, 'f', 2, 64)
			prompt := buildPilotPrompt(task, m, state)
			inv.Prompt = prompt
			s := r.step(ctx, "run pilot", "engine", role.Engine, "model", role.Model, "note", m.NoteID, "resume", inv.Resume != "")
			res, err := r.invoke(ctx, role, inv, prompt)
			if res != nil {
				out.CostUSD += res.CostUSD
			}
			var a PilotAnswer
			if err == nil {
				err = res.Decode(&a)
			}
			switch _, limited := AsUsageLimit(err); {
			case limited:
				return out, s.fail(err) // the run is deferred and answers the rest
			case err != nil:
				failed = s.fail(err)
				reply = "The pilot could not answer: the run failed (the task's status has the details)."
			default:
				st.setSession("pilot", ranOn(role, res), res.SessionID)
				s.done(agentResult(role, res, "answered"), "session", res.SessionID, "costUSD", res.CostUSD)
				reply = a.Reply + rejectActions(a.Proposal, true)
				trigger := m.IssueIID
				if task.Spec.Issue != nil && trigger == task.Spec.Issue.IID {
					trigger = 0
				}
				why := fmt.Sprintf("@%s asked: %s", m.Author, truncate(m.Body, 300))
				if err := r.propose(ctx, task, a.Proposal, stateOf, fmt.Sprintf("note/%d", m.NoteID), trigger, why, false); err != nil {
					failed = err
					reply += "\n\n_The pilot's proposal could not be recorded._"
				} else if a.Proposal != nil && len(a.Proposal.Actions) > 0 && task.Spec.Issue != nil {
					reply += "\n\n" + followsOn(spec, task)
				}
			}
		}
		r.pilotReply(ctx, gl, spec, m, reply)
		st.PilotAnswered = append(st.PilotAnswered, m.NoteID)
		if n := len(st.PilotAnswered); n > maxPilotAnswered {
			st.PilotAnswered = st.PilotAnswered[n-maxPilotAnswered:]
		}
		if err := r.saveState(st); err != nil {
			r.log().Error(err, "saving the pilot's session")
		}
	}
	err = r.assessEvents(ctx, gl, task, spec, state, stateOf, out, events, &st)
	out.Summary = fmt.Sprintf("answered %d messages, assessed %d events", len(msgs), len(events))
	if _, limited := AsUsageLimit(err); limited || failed == nil {
		return out, err
	}
	return out, failed
}

// pilotLeft is what one turn of the pilot may spend: the rest of its
// budget, at most the role's own cap.
func pilotLeft(spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome) float64 {
	budget, _ := strconv.ParseFloat(spec.Pilot.MaxBudgetUSD, 64)
	spent, _ := strconv.ParseFloat(task.Status.Run.CostUSD, 64)
	left := budget - spent - out.CostUSD
	if turnCap, _ := strconv.ParseFloat(spec.Agents.Pilot.MaxBudgetUSD, 64); turnCap > 0 {
		left = min(left, turnCap)
	}
	return left
}

// maxPilotAnswered is how many answered messages the runner remembers, so
// a message the operator hands over again is not answered twice.
const maxPilotAnswered = 100

// pilotReply posts a reply where the message was written. Agent text is made
// inert: no mentions, quick actions or markers.
func (r *Runner) pilotReply(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, m aiv1.PilotMessage, reply string) {
	body := "🧭 " + prose(reply)
	var err error
	if m.MergeRequestIID != 0 {
		_, err = gl.CreateMergeRequestNote(ctx, spec.GitLab.Project, m.MergeRequestIID, body)
	} else {
		_, err = gl.CreateIssueNote(ctx, spec.GitLab.Project, m.IssueIID, body)
	}
	if err != nil {
		r.log().Error(err, "posting the pilot's reply", "note", m.NoteID)
	}
}

// pilotIssue is what the pilot sees of an issue of its milestone.
type pilotIssue struct {
	IID        int64               `json:"iid"`
	Title      string              `json:"title"`
	State      string              `json:"state"`
	Labels     []string            `json:"labels,omitempty"`
	LastResult aiv1.RunResult      `json:"lastRunResult,omitempty"`
	Summary    string              `json:"lastRunSummary,omitempty"`
	CI         *aiv1.CIStatus      `json:"ci,omitempty"`
	Blocked    *aiv1.BlockedStatus `json:"blocked,omitempty"`
}

// pilotState renders the milestone's state as JSON: the spec, autopilot,
// the issues with their tasks, the default branch's health and the last
// comments of the tracking issue.
// It also returns the state proposals are made on (aiv1.PilotState).
func (r *Runner) pilotState(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (string, stateFunc, error) {
	glProject := spec.GitLab.Project
	m, err := gl.GetMilestone(ctx, glProject, task.Spec.Milestone.ID)
	if err != nil {
		return "", nil, fmt.Errorf("getting milestone: %w", err)
	}
	issues, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	if err != nil {
		return "", nil, fmt.Errorf("listing milestone issues: %w", err)
	}
	var tasks aiv1.AgentTaskList
	if err := r.K8s.List(ctx, &tasks, client.InNamespace(r.Namespace), client.MatchingLabels{labelProject: project.Name}); err != nil {
		return "", nil, fmt.Errorf("listing tasks: %w", err)
	}
	var specTask *aiv1.AgentTask
	var v struct {
		Milestone  string                 `json:"milestone"`
		Spec       string                 `json:"spec"`
		Plan       []aiv1.PlannedIssue    `json:"plan,omitempty"`
		Autopilot  *aiv1.AutopilotStatus  `json:"autopilot,omitempty"`
		Issues     []pilotIssue           `json:"issues"`
		MainHealth *aiv1.MainHealthStatus `json:"mainHealth,omitempty"`
		Comments   []string               `json:"trackingIssueComments,omitempty"`
		Proposals  []aiv1.PilotProposal   `json:"openProposals,omitempty"`
		Rules      []rules.Rule           `json:"rules,omitempty"`
		Engines    pilotEngines           `json:"engines"`
	}
	v.Milestone, v.Spec, v.MainHealth = m.Title, rules.Spec(m.Description, spec.Workflow.CommandPrefix), project.Status.MainHealth
	v.Rules = rules.Parse(rules.Lines(m.Description, spec.Workflow.CommandPrefix))
	v.Engines = enginesOf(project, spec, r.now())
	byIID := map[int64]*aiv1.AgentTask{}
	for i := range tasks.Items {
		t := &tasks.Items[i]
		switch {
		case t.Spec.Milestone == nil || t.Spec.Milestone.IID != m.IID:
		case t.Spec.Type == aiv1.TaskTypeSpec:
			v.Plan, v.Autopilot, specTask = t.Status.Plan, t.Status.Autopilot, t
		case t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Issue != nil:
			byIID[t.Spec.Issue.IID] = t
		}
	}
	for _, is := range issues {
		pi := pilotIssue{IID: is.IID, Title: is.Title, State: is.State, Labels: is.Labels}
		if t := byIID[is.IID]; t != nil {
			pi.LastResult, pi.Summary, pi.CI, pi.Blocked = t.Status.Run.LastResult, truncate(t.Status.Run.Summary, 500), t.Status.CI, t.Status.Blocked
		}
		v.Issues = append(v.Issues, pi)
	}
	if task.Spec.Issue != nil {
		notes, err := gl.ListIssueNotes(ctx, glProject, task.Spec.Issue.IID)
		if err != nil {
			return "", nil, fmt.Errorf("listing notes of the tracking issue #%d: %w", task.Spec.Issue.IID, err)
		}
		for _, n := range notes[max(0, len(notes)-pilotComments):] {
			if !n.System {
				v.Comments = append(v.Comments, "@"+n.Author.Username+": "+truncate(n.Body, 1000))
			}
		}
	}
	if ps := task.Status.Pilot; ps != nil {
		v.Proposals = ps.Proposals
	}
	b, err := json.MarshalIndent(v, "", "  ")
	stateOf := func(a []aiv1.PilotAction) string {
		if specTask == nil {
			return ""
		}
		return aiv1.PilotState(specTask, byIID, a)
	}
	return string(b), stateOf, err
}

// pilotEngines is what the pilot knows about the engines: the usage limits
// in force and, per role and profile, where it runs and its fallback, so a
// pause over a limited engine is not called hopeless when a fallback runs.
type pilotEngines struct {
	UsageLimits []pilotLimit         `json:"usageLimits,omitempty"`
	Roles       map[string]pilotRole `json:"roles"`
}

type pilotLimit struct {
	Engine aiv1.Engine `json:"engine"`
	Until  metav1.Time `json:"until"`
}

type pilotRole struct {
	Engine   aiv1.Engine        `json:"engine"`
	Model    string             `json:"model,omitempty"`
	Fallback *aiv1.RoleFallback `json:"fallback,omitempty"`
}

func enginesOf(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, now time.Time) pilotEngines {
	e := pilotEngines{Roles: map[string]pilotRole{}}
	for _, l := range project.Status.UsageLimits {
		if l.Until.After(now) {
			e.UsageLimits = append(e.UsageLimits, pilotLimit{Engine: l.Engine, Until: l.Until})
		}
	}
	role := func(r aiv1.RoleSpec) pilotRole {
		return pilotRole{Engine: r.Engine, Model: r.Model, Fallback: r.Fallback}
	}
	e.Roles["spec"], e.Roles["implement"], e.Roles["review"] = role(spec.Agents.Spec), role(spec.Agents.Implement), role(spec.Agents.Review.RoleSpec)
	for _, p := range spec.Agents.Profiles {
		e.Roles["profile "+p.Name] = role(p.RoleSpec)
	}
	return e
}

// pilotComments is how many of the tracking issue's last comments the pilot
// sees.
const pilotComments = 20

// pilotAddressed reports whether a human's comment is addressed to the
// pilot, or a comment of the bot is the pilot's or the operator's answer
// (they start with 🧭).
func pilotAddressed(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) func(gitlab.Note) bool {
	bot := ""
	if *spec.Pilot.Mention {
		bot = project.Status.BotUsername
	}
	return func(n gitlab.Note) bool {
		if n.Author.ID == project.Status.BotUserID {
			return strings.HasPrefix(n.Body, "🧭 ")
		}
		_, ok := command.Pilot(n.Body, spec.Workflow.PilotPrefix, bot)
		return ok
	}
}

// withoutPilot drops the conversation with the pilot from an issue's
// comments: it is no input for the implementer.
func (r *Runner) withoutPilot(notes []gitlab.Note) []gitlab.Note {
	if r.pilot == nil {
		return notes
	}
	return slices.DeleteFunc(notes, r.pilot)
}

// stateFunc is aiv1.PilotState of the state the pilot read; nil when it
// could not read it.
type stateFunc func([]aiv1.PilotAction) string

// maxProposed bounds the proposals that wait for the operator.
const maxProposed = 10

// propose records the pilot's proposal in status.pilot.proposed with the
// state it was made on; the operator checks it, posts it on the tracking
// issue and runs it once an owner accepts it. The runner executes nothing.
// A milestone without owners, whom nobody could accept it from, or a state
// it could not read get none.
// source names the message or event it answers: a run started again does
// not propose it twice.
// trigger is the issue it answers (0: the milestone), why the
// classification and evidence or the message, for the audit comment;
// quiet says the run did not mention the owners.
func (r *Runner) propose(ctx context.Context, task *aiv1.AgentTask, p *ProposalAnswer, stateOf stateFunc, source string, trigger int64, why string, quiet bool) error {
	if p == nil || len(p.Actions) == 0 || len(task.Spec.Owners) == 0 || stateOf == nil {
		return nil
	}
	prop := aiv1.PilotProposal{Summary: truncate(p.Summary, 500), Source: source, Trigger: trigger, Why: truncate(why, 1000), Quiet: quiet}
	for _, a := range p.Actions[:min(len(p.Actions), 5)] {
		prop.Actions = append(prop.Actions, aiv1.PilotAction{Tool: truncate(a.Tool, 40), IssueIID: a.Issue, Kind: truncate(a.Kind, 40),
			Note: truncate(a.Note, 2000), Title: truncate(a.Title, 200), Decision: truncate(a.Decision, 40), Rule: truncate(a.Rule, 10), Spec: a.Spec})
	}
	if prop.State = stateOf(prop.Actions); prop.State == "" {
		return nil
	}
	// Optimistic: the operator takes proposals out concurrently.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var t aiv1.AgentTask
		if err := r.K8s.Get(ctx, client.ObjectKeyFromObject(task), &t); err != nil {
			return err
		}
		orig := t.DeepCopy()
		if t.Status.Pilot == nil {
			t.Status.Pilot = &aiv1.PilotStatus{}
		}
		ps := t.Status.Pilot
		if slices.ContainsFunc(slices.Concat(ps.Proposed, ps.Proposals, ps.Accepted), func(q aiv1.PilotProposal) bool { return q.Source == source }) {
			return nil
		}
		if len(ps.Proposed) >= maxProposed {
			return fmt.Errorf("%d proposals wait for the operator already", maxProposed)
		}
		t.Status.Pilot.Proposed = append(t.Status.Pilot.Proposed, prop)
		return r.K8s.Status().Patch(ctx, &t, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	})
}

// followsOn says what becomes of the pilot's proposal: a pilot that
// manages runs its counted tools itself, within its interventions.
func followsOn(spec aiv1.AgentProjectSpec, task *aiv1.AgentTask) string {
	if spec.Pilot.AutonomyOf(task) == aiv1.PilotManaged {
		return fmt.Sprintf("_The pilot runs it within its interventions, with an audit comment on #%d, or proposes it there._", task.Spec.Issue.IID)
	}
	return fmt.Sprintf("_The proposal follows on #%d, where an owner accepts it._", task.Spec.Issue.IID)
}
