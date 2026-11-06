package controller

import (
	"slices"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const infraLabel = "agent-repo::group/infra"

// withInfraTarget allows group/infra in the cluster and makes it the
// project's companion target, with autoMerge as given.
func withInfraTarget(e *projectEnv, autoMerge bool) {
	e.r.CrossRepoAllowed = []string{"group/infra", "group/other"}
	e.patchProject(func(p *aiv1.AgentProject) {
		p.Spec.CrossRepo = &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{
			{Project: "group/infra", Paths: []string{"clusters/app/"}, AutoMerge: autoMerge},
		}}
	})
}

// newRepoEnv is a milestone with description and its issue #42 in review,
// labelled with labels, under the group/infra target.
func newRepoEnv(t *testing.T, description string, autoMerge bool, labels ...string) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	withInfraTarget(e, autoMerge)
	e.gl.Users = []gitlab.User{alice, mallory}
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: description}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.gl.AddIssue(42, "Add PayPal button", append([]string{"agent::review"}, labels...)...).Milestone = &m
	e.reconcile()
	return e
}

func (e *projectEnv) companion() *aiv1.CompanionSpec {
	e.t.Helper()
	task := e.tasks()[issueTaskName]
	return task.Spec.Companion
}

func TestCompanionFromMilestoneLine(t *testing.T) {
	for _, c := range []struct {
		name      string
		autopilot bool
		merge     bool
		autoMerge bool
		want      bool
	}{
		{"all three", true, true, true, true},
		{"no merge on the line", true, false, true, false},
		{"no autopilot", false, true, true, false},
		{"target without autoMerge", true, true, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			desc := "/agent spec\n/agent owners @alice\n/agent repo https://gitlab.example/group/infra/-/tree/main"
			if c.autopilot {
				desc = strings.Replace(desc, "/agent spec", "/agent autopilot", 1)
			}
			if c.merge {
				desc += " merge"
			}
			e := newRepoEnv(t, desc+"\nBuild it.", c.autoMerge, infraLabel)
			want := aiv1.CompanionSpec{Project: "group/infra", Paths: []string{"clusters/app/"}, Branch: "agent/app-42-add-paypal-button", AutoMerge: c.want}
			if got := e.companion(); got == nil || got.Project != want.Project || !slices.Equal(got.Paths, want.Paths) || got.Branch != want.Branch || got.AutoMerge != want.AutoMerge {
				t.Fatalf("companion %+v, want %+v", got, want)
			}
			if e.gl.LabelEventLists != 0 {
				t.Fatalf("under a milestone line the label events are not read: %d calls", e.gl.LabelEventLists)
			}
			if task := e.tasks()[issueTaskName]; task.Spec.RunRequest != 0 {
				t.Fatalf("the companion requests no run: %d", task.Spec.RunRequest)
			}

			// The label goes: so does the companion.
			if _, err := e.gl.UpdateIssue(e.ctx, "group/app", 42, gitlab.UpdateIssueOptions{RemoveLabels: []string{infraLabel}}); err != nil {
				t.Fatal(err)
			}
			e.reconcile()
			if got := e.companion(); got != nil {
				t.Fatalf("without the label there is no companion: %+v", got)
			}
		})
	}
}

func TestCompanionFromOwnerCommand(t *testing.T) {
	// An autopilot milestone with "merge" for another project: the owner's
	// companion is never merged by autopilot.
	e := newRepoEnv(t, "/agent autopilot\n/agent owners @alice\n/agent repo group/other merge\nBuild it.", true)
	e.patchProject(commandsFromOwners)
	e.gl.AddNote(42, alice, "/agent repo group/infra")
	e.reconcile()
	if !slices.Contains(e.gl.Labels(42), infraLabel) || !strings.Contains(lastNote(e, 42), "companion repository `group/infra`") {
		t.Fatalf("the command sets the label and answers: %v %q", e.gl.Labels(42), lastNote(e, 42))
	}
	got := e.companion()
	if got == nil || got.Project != "group/infra" || got.AutoMerge {
		t.Fatalf("the owner's companion counts, without autoMerge: %+v", got)
	}
	if task := e.tasks()[issueTaskName]; task.Spec.RunRequest != 0 {
		t.Fatalf("a routing command requests no run: %d", task.Spec.RunRequest)
	}

	// commandsFrom: owners refuses a non-owner's "/agent repo".
	e.gl.AddNote(42, mallory, "/agent repo none")
	e.reconcile()
	if !slices.Contains(e.gl.Labels(42), infraLabel) || !strings.Contains(lastNote(e, 42), ownersOnly) || e.companion() == nil {
		t.Fatalf("a non-owner's command changes nothing: %v %q", e.gl.Labels(42), lastNote(e, 42))
	}

	// An unknown target: the answer lists the allowed ones.
	e.gl.AddNote(42, alice, "/agent repo group/personal")
	e.reconcile()
	if !strings.Contains(lastNote(e, 42), "names no companion target") || !strings.Contains(lastNote(e, 42), "`group/infra`") || e.companion() == nil {
		t.Fatalf("an unknown target is refused with the allowed ones: %q", lastNote(e, 42))
	}

	e.gl.AddNote(42, alice, "/agent repo none")
	e.reconcile()
	if slices.Contains(e.gl.Labels(42), infraLabel) || e.companion() != nil {
		t.Fatalf("none clears the label and the companion: %v %+v", e.gl.Labels(42), e.companion())
	}
}

func TestCompanionFromHandSetLabel(t *testing.T) {
	e := newRepoEnv(t, "/agent spec\n/agent owners @alice\nBuild it.", true)

	// By a non-owner: ignored, with one notice.
	e.gl.LabelBy(42, mallory, infraLabel)
	e.reconcile()
	e.reconcile()
	if e.companion() != nil {
		t.Fatalf("a non-owner's label does not count: %+v", e.companion())
	}
	if got := countIn(e.gl.IssueNotes[42], "is ignored"); got != 1 || !strings.Contains(lastNote(e, 42), "`/agent repo group/infra`") {
		t.Fatalf("want one notice, got %d: %q", got, lastNote(e, 42))
	}
	if task := e.tasks()[issueTaskName]; task.Spec.Source.CompanionNotice != infraLabel {
		t.Fatalf("the notice is recorded: %+v", task.Spec.Source)
	}

	// The owner sets it again by hand: it counts, no further notice.
	e.gl.LabelBy(42, alice, infraLabel)
	e.reconcile()
	if got := e.companion(); got == nil || got.Project != "group/infra" || got.AutoMerge {
		t.Fatalf("an owner's label counts, without autoMerge: %+v", got)
	}
	if got := countIn(e.gl.IssueNotes[42], "is ignored"); got != 1 {
		t.Fatalf("no further notice: %d", got)
	}
	if task := e.tasks()[issueTaskName]; task.Spec.Source.CompanionNotice != "" {
		t.Fatalf("the notice is cleared once the label counts: %+v", task.Spec.Source)
	}
}

func TestCompanionLabelOfNoTarget(t *testing.T) {
	e := newRepoEnv(t, "/agent spec\n/agent owners @alice\n/agent repo group/personal\nBuild it.", true, "agent-repo::group/personal")
	e.reconcile()
	if e.companion() != nil {
		t.Fatalf("a label of no target does not count: %+v", e.companion())
	}
	if got := countIn(e.gl.IssueNotes[42], "is ignored"); got != 1 || !strings.Contains(lastNote(e, 42), "`group/infra`") {
		t.Fatalf("want one notice naming the targets, got %d: %q", got, lastNote(e, 42))
	}
	if e.gl.LabelEventLists != 0 {
		t.Fatalf("no label events are read for it: %d", e.gl.LabelEventLists)
	}
}

func TestRepoLinesOnTheSpecTask(t *testing.T) {
	e := newRepoEnv(t, "/agent spec\n/agent repo group/infra merge\n/agent repo group/personal\n/agent repo\nBuild it.", false)
	st := e.tasks()[specTaskName]
	want := []aiv1.MilestoneRepo{{Project: "group/infra", Merge: true}}
	if len(st.Spec.Repos) != 3 || st.Spec.Repos[0] != want[0] || st.Spec.Repos[1].Project != "group/personal" || st.Spec.Repos[1].Problem == "" ||
		st.Spec.Repos[2].Line != "/agent repo" || st.Spec.Repos[2].Problem == "" {
		t.Fatalf("repos %+v", st.Spec.Repos)
	}
	// Without a tracking issue no comment names them.
	for iid, notes := range e.gl.IssueNotes {
		if countIn(notes, "ignored") > 0 {
			t.Fatalf("no comment without autopilot, got one on #%d", iid)
		}
	}

	// The line is part of the spec: changing it plans again.
	run := st.Spec.RunRequest
	e.gl.Milestones[0].Description = strings.Replace(e.gl.Milestones[0].Description, "group/infra merge", "group/infra", 1)
	e.reconcile()
	if st := e.tasks()[specTaskName]; st.Spec.RunRequest != run+1 || st.Spec.Repos[0].Merge {
		t.Fatalf("a changed repo line re-plans: %d %+v", st.Spec.RunRequest, st.Spec.Repos)
	}

	// A target that becomes invalid shows on the spec task without a new
	// plan.
	e.r.CrossRepoAllowed = nil
	e.reconcile()
	if st := e.tasks()[specTaskName]; st.Spec.RunRequest != run+1 || st.Spec.Repos[0].Problem == "" {
		t.Fatalf("the problem shows, nothing re-plans: %d %+v", st.Spec.RunRequest, st.Spec.Repos)
	}
}

func TestUnknownRepoLineNamedOnceOnTheTrackingIssue(t *testing.T) {
	e := newRepoEnv(t, "/agent autopilot\n/agent repo group/infra\n/agent repo group/personal\n/agent repo a/b c\nBuild it.", false)
	e.reconcile()
	ap := e.autopilot()
	if ap == nil || ap.TrackingIssueIID == 0 {
		t.Fatalf("no tracking issue: %+v", ap)
	}
	notes := e.gl.IssueNotes[ap.TrackingIssueIID]
	if got := countIn(notes, "are ignored"); got != 1 {
		t.Fatalf("want one comment, got %d", got)
	}
	body := lastNoteWith(notes, "are ignored")
	if !strings.Contains(body, "`/agent repo group/personal`") || !strings.Contains(body, "`/agent repo a/b c`") || strings.Contains(body, "`/agent repo group/infra`") {
		t.Fatalf("the comment names the ignored lines only: %q", body)
	}
	if !slices.Equal(ap.RepoNotices, []string{"/agent repo group/personal", "/agent repo a/b c"}) {
		t.Fatalf("repo notices %v", ap.RepoNotices)
	}

	// Fixed lines leave the list; a new broken one is named on its own.
	e.gl.Milestones[0].Description = "/agent autopilot\n/agent repo group/infra\n/agent repo group/personal\n/agent repo x\nBuild it."
	e.reconcile()
	e.reconcile()
	notes = e.gl.IssueNotes[ap.TrackingIssueIID]
	if got := countIn(notes, "are ignored"); got != 2 {
		t.Fatalf("want a second comment for the new line only, got %d", got)
	}
	if body := lastNoteWith(notes, "are ignored"); !strings.Contains(body, "`/agent repo x`") || strings.Contains(body, "group/personal") {
		t.Fatalf("only the new line is named: %q", body)
	}
	if ap := e.autopilot(); !slices.Equal(ap.RepoNotices, []string{"/agent repo group/personal", "/agent repo x"}) {
		t.Fatalf("repo notices %v", ap.RepoNotices)
	}
}

func lastNoteWith(notes []gitlab.Note, s string) string {
	for i := len(notes) - 1; i >= 0; i-- {
		if strings.Contains(notes[i].Body, s) {
			return notes[i].Body
		}
	}
	return ""
}
