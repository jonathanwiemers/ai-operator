package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

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
