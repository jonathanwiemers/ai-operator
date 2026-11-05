package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func deskOnly(p *aiv1.AgentProjectSpec) { p.Workflow.Inputs = []aiv1.InputSource{aiv1.InputDesk} }

func TestFailureReportPointsToTheDeskWithDeskInput(t *testing.T) {
	const page = "https://desk.example/projects/agents/shop/tasks/shop-42-add-paypal-button"
	for name, c := range map[string]struct {
		inputs    []aiv1.InputSource
		want, not string
	}{
		"desk":        {[]aiv1.InputSource{aiv1.InputDesk}, "To try again, ask for a new run in the Desk: " + page + " (or move the issue back to `agent::ready`).", "/agent retry"},
		"gitlab desk": {[]aiv1.InputSource{aiv1.InputGitLab, aiv1.InputDesk}, "To try again, comment `/agent retry` (or move the issue back to `agent::ready`).", "in the Desk"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newImplementEnv(t)
			e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Workflow.Inputs = c.inputs }, func(*aiv1.AgentTaskSpec) {})
			r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{runErr: errors.New("engine exploded")}, e.task.Name)
			r.DeskURL = "https://desk.example"
			if _, err := r.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			notes := e.gl.IssueNotes[42]
			if body := notes[len(notes)-1].Body; !strings.Contains(body, c.want) || strings.Contains(body, c.not) {
				t.Fatalf("want %q and no %q in:\n%s", c.want, c.not, body)
			}
		})
	}
}

func TestSteerWithoutDeskURL(t *testing.T) {
	r := &Runner{}
	task := &aiv1.AgentTask{}
	var spec aiv1.AgentProjectSpec
	deskOnly(&spec)
	if got := r.steer(spec, task, "comment `/agent …` with guidance", "give guidance"); got != "give guidance in the Desk" {
		t.Fatalf("got %q", got)
	}
}

func TestImplementerSkipsAgentCommandsWithDeskInput(t *testing.T) {
	project := &aiv1.AgentProject{Status: aiv1.AgentProjectStatus{BotUserID: 1, BotUsername: "ai-bot"}}
	human := gitlab.User{ID: 2}
	notes := []gitlab.Note{
		{ID: 1, Author: human, Body: "/agent please also handle refunds"},
		{ID: 2, Author: human, Body: "/pilot why?"},
		{ID: 3, Author: human, Body: "The button should be blue."},
		{ID: 4, Author: human, Body: "/agents are great"},
	}
	kept := func(spec aiv1.AgentProjectSpec) []int64 {
		var ids []int64
		skip := pilotAddressed(project, spec.WithDefaults())
		for _, n := range notes {
			if !skip(n) {
				ids = append(ids, n.ID)
			}
		}
		return ids
	}
	var both, desk aiv1.AgentProjectSpec
	deskOnly(&desk)
	if got := kept(both); len(got) != 3 || got[0] != 1 {
		t.Fatalf("with gitlab input the implementer reads /agent comments: %v", got)
	}
	if got := kept(desk); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("with desk input only it does not: %v", got)
	}
}

// implementerInvocations are the implementer's invocations an engine got.
func implementerInvocations(e *recordingEngine) []Invocation {
	var out []Invocation
	for _, c := range e.calls {
		if c.Role == "implement" {
			out = append(out, c)
		}
	}
	return out
}

func TestDeskInputContextOfFreshAndResumedSessions(t *testing.T) {
	human := gitlab.User{ID: 1, Username: "alice"}
	at := metav1.NewTime(time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC))
	chat := []aiv1.ChatMessage{
		{ID: 7, Kind: "message", Role: "user", Author: "sub-alice", AuthorName: "Alice", Text: "Use the sandbox account.", At: at},
		{ID: 8, Kind: "message", Role: "agent", Text: "Will do.", At: at},
	}
	for name, c := range map[string]struct {
		inputs     []aiv1.InputSource
		want, not  []string
		resumeWant []string
		resumeNot  []string
	}{
		"desk": {
			inputs: []aiv1.InputSource{aiv1.InputDesk},
			want:   []string{"Implement issue #42", "## Recent Desk chat", "**Alice** (2026-11-05 08:30 UTC):\n<untrusted-data>\nUse the sandbox account.", "**agent (you)** (2026-11-05 08:30 UTC):\n<untrusted-data>\nWill do."},
			not:    []string{"The button should be blue."},
			// Human review threads on the MR are still read; the issue
			// thread and the chat are not sent again.
			resumeWant: []string{"Rename this variable."},
			resumeNot:  []string{"Make it green.", "Recent Desk chat", "Working on this in"},
		},
		"gitlab desk": {
			inputs:     []aiv1.InputSource{aiv1.InputGitLab, aiv1.InputDesk},
			want:       []string{"Implement issue #42", "The button should be blue."},
			not:        []string{"Recent Desk chat", "Use the sandbox account."},
			resumeWant: []string{"Make it green.", "Rename this variable."},
			resumeNot:  []string{"Recent Desk chat"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newImplementEnv(t)
			e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Workflow.Inputs = c.inputs }, func(s *aiv1.AgentTaskSpec) { s.Source.ChatContext = chat })
			e.gl.AddNote(42, human, "The button should be blue.")
			engine := &recordingEngine{Engine: steadyEngine{}}
			r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
			if _, err := r.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			calls := implementerInvocations(engine)
			if len(calls) != 1 || calls[0].Resume != "" {
				t.Fatalf("want one fresh implementer call, got %d", len(calls))
			}
			checkPrompt(t, "fresh", calls[0].Prompt, c.want, c.not)

			e.gl.AddNote(42, human, "Make it green.")
			e.gl.AddMRNote(1, human, "Rename this variable.")
			e.rerun(t, r)
			calls = implementerInvocations(engine)
			if len(calls) != 2 || calls[1].Resume == "" {
				t.Fatalf("want the second run to resume the session, got %d calls", len(calls))
			}
			checkPrompt(t, "resumed", calls[1].Prompt, c.resumeWant, c.resumeNot)
		})
	}
}

func checkPrompt(t *testing.T, what, prompt string, want, not []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(prompt, w) {
			t.Errorf("%s prompt lacks %q:\n%s", what, w, prompt)
		}
	}
	for _, n := range not {
		if strings.Contains(prompt, n) {
			t.Errorf("%s prompt has %q:\n%s", what, n, prompt)
		}
	}
}

func TestDeskInputStepsGetTheReviewThreadsOnly(t *testing.T) {
	for name, c := range map[string]struct {
		inputs []aiv1.InputSource
		issue  bool
	}{
		"desk":        {[]aiv1.InputSource{aiv1.InputDesk}, false},
		"gitlab desk": {[]aiv1.InputSource{aiv1.InputGitLab, aiv1.InputDesk}, true},
	} {
		t.Run(name, func(t *testing.T) {
			e := newImplementEnv(t)
			e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Workflow.Inputs = c.inputs }, func(*aiv1.AgentTaskSpec) {})
			engine := &stepEngine{gl: e.gl, issueComment: true}
			r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
			if _, err := r.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if engine.n != 2 || !strings.Contains(engine.prompts[1], "Call it Payment, not PayPalPayment.") ||
				strings.Contains(engine.prompts[1], "Make it green.") != c.issue {
				t.Fatalf("want the MR comment, and the issue comment %v, in the next step: %q", c.issue, engine.prompts)
			}
		})
	}
}
