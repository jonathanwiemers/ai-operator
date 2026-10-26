package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// labelProject is the controller's LabelProject.
const labelProject = "ai.wiemers.me/project"

// runPilot answers the owners' messages to a milestone's pilot, one turn of
// its session each, and assesses the events the operator woke it for. It
// only answers and assesses: the replies are posted where the messages were
// written, the assessments on the tracking issue, nothing else changes.
func (r *Runner) runPilot(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (*Outcome, error) {
	if task.Spec.Milestone == nil {
		return nil, fmt.Errorf("coordinate task has no milestone")
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
	state, err := r.pilotState(ctx, gl, task, project, spec)
	if err != nil {
		// The run's request is done either way: say so instead of dropping
		// the messages.
		for _, m := range msgs {
			r.pilotReply(ctx, gl, spec, m, "The pilot could not read the milestone's state, so it cannot answer; ask again in a moment.")
		}
		// The events' own data is still worth an assessment.
		_ = r.assessEvents(ctx, gl, task, spec, fmt.Sprintf("(the milestone's state could not be read: %v)", err), out, events, &st)
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
			inv := roleInvocation(role, pilotSystemPrompt, readOnlyTools, pilotSchema, true)
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
				st.setSession("pilot", role.Engine, res.SessionID)
				s.done(agentResult(role, res, "answered"), "session", res.SessionID, "costUSD", res.CostUSD)
				reply = a.Reply
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
	err = r.assessEvents(ctx, gl, task, spec, state, out, events, &st)
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
func (r *Runner) pilotState(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (string, error) {
	glProject := spec.GitLab.Project
	m, err := gl.GetMilestone(ctx, glProject, task.Spec.Milestone.ID)
	if err != nil {
		return "", fmt.Errorf("getting milestone: %w", err)
	}
	issues, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	if err != nil {
		return "", fmt.Errorf("listing milestone issues: %w", err)
	}
	var tasks aiv1.AgentTaskList
	if err := r.K8s.List(ctx, &tasks, client.InNamespace(r.Namespace), client.MatchingLabels{labelProject: project.Name}); err != nil {
		return "", fmt.Errorf("listing tasks: %w", err)
	}
	var v struct {
		Milestone  string                 `json:"milestone"`
		Spec       string                 `json:"spec"`
		Plan       []aiv1.PlannedIssue    `json:"plan,omitempty"`
		Autopilot  *aiv1.AutopilotStatus  `json:"autopilot,omitempty"`
		Issues     []pilotIssue           `json:"issues"`
		MainHealth *aiv1.MainHealthStatus `json:"mainHealth,omitempty"`
		Comments   []string               `json:"trackingIssueComments,omitempty"`
	}
	v.Milestone, v.Spec, v.MainHealth = m.Title, notify.WithoutOwners(m.Description, spec.Workflow.CommandPrefix), project.Status.MainHealth
	byIID := map[int64]*aiv1.AgentTask{}
	for i := range tasks.Items {
		t := &tasks.Items[i]
		switch {
		case t.Spec.Milestone == nil || t.Spec.Milestone.IID != m.IID:
		case t.Spec.Type == aiv1.TaskTypeSpec:
			v.Plan, v.Autopilot = t.Status.Plan, t.Status.Autopilot
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
			return "", fmt.Errorf("listing notes of the tracking issue #%d: %w", task.Spec.Issue.IID, err)
		}
		for _, n := range notes[max(0, len(notes)-pilotComments):] {
			if !n.System {
				v.Comments = append(v.Comments, "@"+n.Author.Username+": "+truncate(n.Body, 1000))
			}
		}
	}
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
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
