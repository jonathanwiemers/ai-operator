package ui

import (
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// maxClosedPrompts bounds the answered, superseded and expired prompts a
// view shows next to the open ones.
const maxClosedPrompts = 10

// PromptView is one question to the owners. Question, context, option
// labels and details and the answer's text are untrusted: the app renders
// them as markdown without HTML.
type PromptView struct {
	ID            string             `json:"id"`
	Kind          aiv1.PromptKind    `json:"kind"`
	Question      string             `json:"question"`
	Context       string             `json:"context,omitempty"`
	Options       []PromptOptionView `json:"options"`
	AllowFreeText bool               `json:"allowFreeText"`
	State         aiv1.PromptState   `json:"state"`
	Answer        *PromptAnswerView  `json:"answer,omitempty"`
	IssueIID      int64              `json:"issueIID,omitempty"`
	MRIID         int64              `json:"mrIID,omitempty"`
	// Task is the AgentTask the prompt is about, if any.
	Task     string       `json:"task,omitempty"`
	PostedAt *metav1.Time `json:"postedAt,omitempty"`
	ClosedAt *metav1.Time `json:"closedAt,omitempty"`
	// CanAnswer: the prompt is open and the user is one of its owners with
	// the Desk chat (the operator checks again).
	CanAnswer bool `json:"canAnswer"`
	// Target is the task whose chat takes the answer command.
	Target PromptTarget `json:"target"`
}

// PromptTarget is the task that receives a prompt's answer command: the
// Coordinate task for the milestone's prompts, the Implement task for its
// agent's questions.
type PromptTarget struct {
	Namespace string `json:"namespace"`
	Task      string `json:"task"`
}

// PromptOptionView is one answer to a prompt. Actions say in plain text
// what choosing it does.
type PromptOptionView struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Detail      string   `json:"detail,omitempty"`
	Actions     []string `json:"actions"`
	Recommended bool     `json:"recommended"`
	FromAnswer  bool     `json:"fromAnswer"`
}

// PromptAnswerView is an owner's answer to a prompt.
type PromptAnswerView struct {
	OptionID string      `json:"optionID,omitempty"`
	Text     string      `json:"text,omitempty"`
	By       string      `json:"by"`
	At       metav1.Time `json:"at"`
	Via      string      `json:"via,omitempty" tstype:"'desk' | 'gitlab'"`
	// Outcome is the operator's markdown of what the chosen actions did.
	Outcome string `json:"outcome,omitempty"`
}

// promptViews returns the prompts of holder, newest first: the open ones
// and the newest maxClosedPrompts closed ones. Answers go to the task
// target (none: nobody can answer); user may answer when it is one of the
// holder's owner subjects and chat allows it the Desk chat.
func promptViews(holder *aiv1.AgentTask, target, user string, chat bool) []PromptView {
	owner := chat && target != "" && slices.Contains(holder.Spec.OwnerSubjects, user)
	views, closed := []PromptView{}, 0
	for _, p := range slices.Backward(holder.Status.Prompts) {
		if p.State.Closed() {
			if closed++; closed > maxClosedPrompts {
				continue
			}
		}
		views = append(views, promptView(p, owner, PromptTarget{Namespace: holder.Namespace, Task: target}))
	}
	return views
}

func promptView(p aiv1.Prompt, owner bool, target PromptTarget) PromptView {
	state := p.State
	if state == "" {
		state = aiv1.PromptOpen
	}
	v := PromptView{ID: p.ID, Kind: p.Source.Kind, Question: p.Question, Context: p.Context, Options: []PromptOptionView{},
		AllowFreeText: p.AllowFreeText, State: state, IssueIID: p.Source.IssueIID, MRIID: p.Source.MRIID, Task: p.Source.Task,
		PostedAt: p.PostedAt, ClosedAt: p.ClosedAt, CanAnswer: owner && !state.Closed(), Target: target}
	for _, o := range p.Options {
		ov := PromptOptionView{ID: o.ID, Label: o.Label, Detail: o.Detail, Actions: []string{}, Recommended: o.Recommended, FromAnswer: o.FromAnswer}
		for _, a := range o.Actions {
			ov.Actions = append(ov.Actions, actionText(a))
		}
		v.Options = append(v.Options, ov)
	}
	// The recommended option first, the others in their order.
	slices.SortStableFunc(v.Options, func(a, b PromptOptionView) int {
		switch {
		case a.Recommended == b.Recommended:
			return 0
		case a.Recommended:
			return -1
		}
		return 1
	})
	if a := p.Answer; a != nil {
		v.Answer = &PromptAnswerView{OptionID: a.OptionID, Text: a.Text, By: a.By, At: a.At, Via: a.Via, Outcome: a.Outcome}
	}
	return v
}

// actionText says in plain text what a pilot action does, like the
// operator's describeAction without the markdown. The pilot's and owners'
// words in it are untrusted: the app shows them as text.
func actionText(a aiv1.PilotAction) string {
	switch a.Tool {
	case "retry":
		return fmt.Sprintf("retry #%d: a new run", a.IssueIID)
	case "reReview":
		return fmt.Sprintf("reReview #%d: its reviewers run again on the pushed head", a.IssueIID)
	case "instruct":
		if a.Text != "" {
			return fmt.Sprintf("instruct #%d with the words of %s: %s", a.IssueIID, a.Owner, a.Text)
		}
		s := fmt.Sprintf("instruct #%d: %s", a.IssueIID, a.Kind)
		if a.Note != "" {
			s += ", with the pilot's hint: " + a.Note
		}
		return s
	case "rebase":
		return fmt.Sprintf("rebase #%d: GitLab rebases its MR onto the default branch; the review holds if the change is unchanged", a.IssueIID)
	case "mergeBase":
		return fmt.Sprintf("mergeBase #%d: the agent merges the default branch into its MR", a.IssueIID)
	case "skip":
		return fmt.Sprintf("skip #%d: autopilot leaves it out", a.IssueIID)
	case "unskip":
		return fmt.Sprintf("unskip #%d: it goes back to pending, and autopilot picks it once it can start", a.IssueIID)
	case "startAnyway":
		return fmt.Sprintf("startAnyway #%d: it may start although an issue it depends on was skipped", a.IssueIID)
	case "markDone":
		s := fmt.Sprintf("markDone #%d: closed as done, not merged by autopilot; the issues that wait for it can start", a.IssueIID)
		if a.Note != "" {
			s += ". The reason: " + a.Note
		}
		return s
	case "pause", "resume":
		return a.Tool + " autopilot"
	case "revisit":
		return fmt.Sprintf("revisit %s, asking for: %s", a.Decision, a.Note)
	case "openIssue":
		return fmt.Sprintf("openIssue %q, not handed to the agent", a.Title)
	case "fixFlaky":
		return fmt.Sprintf("fixFlaky #%d: the flaky issue goes to the agent in this milestone", a.IssueIID)
	case "retryPipeline":
		return fmt.Sprintf("retryPipeline %d: its failed jobs run again", a.PipelineID)
	case "setRule":
		return fmt.Sprintf("setRule (%s): %s", a.Kind, a.Note)
	case "forgetRule":
		return "forgetRule " + a.Rule
	case "editSpec":
		return "editSpec: a new spec replaces the milestone description, its command lines kept"
	}
	return a.Tool
}
