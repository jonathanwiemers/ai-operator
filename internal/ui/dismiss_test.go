package ui

import (
	"context"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

const shopDismiss = shopMilestone + "/followups/dismiss"

// dismissEnv is an owner (ada@example.com, GitLab user 7) with steer on
// milestone 3 under autopilot, its tracking issue #77 in an in-memory
// GitLab with an unrelated thread and the retrospective's, whose
// follow-up 1 is open and 2 opened as #60. It returns the retrospective's
// note.
func dismissEnv(t *testing.T) (*testEnv, *gitlabfake.Fake, gitlab.Note) {
	t.Helper()
	env, gl := steerer(t)
	withAutopilot(t, env, gl)
	fake := followUpGitLab(env)
	bot := gitlab.User{ID: 99, Username: "ai-bot"}
	fake.Issues[77] = &gitlab.Issue{IID: 77, Description: "Tracking\n" + marker.TrackingIssue, Author: bot, Milestone: &gitlab.Milestone{ID: 30, IID: 3}}
	other := fake.AddNote(77, bot, "📌 The milestone stays open.")
	retro := fake.AddNote(77, bot, "🧭 Retrospective")
	fake.AddReply(77, other.ID, gitlab.User{ID: 7}, "thanks")
	withRetrospective(t, env, &aiv1.PilotRetrospective{NoteID: retro.ID, PostedAt: &metav1.Time{Time: time.Now()}, OwnerIDs: []int64{7},
		FollowUps: []aiv1.PilotFollowUp{{Title: "Refunds"}, {Title: "Receipts"}}, Created: map[string]int64{"2": 60}})
	setOwners(t, env, "ada@example.com")
	return env, fake, retro
}

func TestDismissFollowUps(t *testing.T) {
	env, fake, retro := dismissEnv(t)
	var m MilestoneView
	if env.get(t, shopMilestone, &m); m.Permissions == nil || !m.Permissions.Dismiss {
		t.Fatalf("an owner may dismiss: %+v", m.Permissions)
	}
	code, out := env.postJSON(t, shopDismiss, nil)
	if code != http.StatusAccepted || out["body"] != "create none" {
		t.Fatalf("dismiss: %d %v", code, out)
	}
	notes := fake.IssueNotes[77]
	last := notes[len(notes)-1]
	if !command.RetroNone(last.Body) || fake.Replies[last.ID] != retro.ID || fake.DiscussionLists != 1 {
		t.Errorf("the reply %+v in thread %d (lists %d), want the retrospective's %d", last, fake.Replies[last.ID], fake.DiscussionLists, retro.ID)
	}

	// Once the operator settled them, there is nothing to dismiss.
	var st aiv1.AgentTask
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Status.Pilot.Retrospective.DismissedAt, st.Status.Pilot.Retrospective.DismissedBy = &metav1.Time{Time: time.Now()}, "ada-gl"
	if err := env.reader.Update(context.Background(), &st); err != nil {
		t.Fatal(err)
	}
	m = MilestoneView{}
	if env.get(t, shopMilestone, &m); m.Permissions.Dismiss {
		t.Errorf("dismissed already: %+v", m.Permissions)
	}
	if code, _ := env.postJSON(t, shopDismiss, nil); code != http.StatusConflict || len(fake.IssueNotes[77]) != len(notes) {
		t.Errorf("dismissed again: %d", code)
	}
}

func TestDismissFollowUpsOnlyForOwners(t *testing.T) {
	t.Run("not an owner subject", func(t *testing.T) {
		env, fake, _ := dismissEnv(t)
		setOwners(t, env, "bea")
		var m MilestoneView
		if env.get(t, shopMilestone, &m); m.Permissions.Dismiss {
			t.Errorf("a non-owner may not: %+v", m.Permissions)
		}
		if code, _ := env.postJSON(t, shopDismiss, nil); code != http.StatusForbidden || len(fake.IssueNotes[77]) != 3 {
			t.Errorf("a non-owner: %d, notes %d", code, len(fake.IssueNotes[77]))
		}
	})
	t.Run("a GitLab account the operator did not pin", func(t *testing.T) {
		env, fake, retro := dismissEnv(t)
		withRetrospective(t, env, &aiv1.PilotRetrospective{NoteID: retro.ID, OwnerIDs: []int64{8}, FollowUps: []aiv1.PilotFollowUp{{Title: "Refunds"}}})
		if code, _ := env.postJSON(t, shopDismiss, nil); code != http.StatusForbidden || len(fake.IssueNotes[77]) != 3 {
			t.Errorf("another GitLab account: %d, notes %d", code, len(fake.IssueNotes[77]))
		}
	})
	t.Run("view only", func(t *testing.T) {
		env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
		withAutopilot(t, env, gl)
		env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
		env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
		env.login(t, "/")
		setOwners(t, env, "ada@example.com")
		if code, _ := env.postJSON(t, shopDismiss, nil); code != http.StatusForbidden {
			t.Errorf("without steer: %d", code)
		}
	})
}

func TestDismissFollowUpsNeedsOpenOnes(t *testing.T) {
	for name, retro := range map[string]*aiv1.PilotRetrospective{
		"no retrospective": nil,
		"not posted":       {Failed: "no budget"},
		"all opened":       {NoteID: 1, OwnerIDs: []int64{7}, FollowUps: []aiv1.PilotFollowUp{{Title: "Refunds"}}, Created: map[string]int64{"1": 60}},
		"no follow-ups":    {NoteID: 1, OwnerIDs: []int64{7}},
	} {
		t.Run(name, func(t *testing.T) {
			env, fake, _ := dismissEnv(t)
			withRetrospective(t, env, retro)
			var m MilestoneView
			if env.get(t, shopMilestone, &m); m.Permissions.Dismiss {
				t.Errorf("nothing to dismiss: %+v", m.Permissions)
			}
			if code, _ := env.postJSON(t, shopDismiss, nil); code != http.StatusConflict || len(fake.IssueNotes[77]) != 3 {
				t.Errorf("%d, notes %d", code, len(fake.IssueNotes[77]))
			}
		})
	}
	t.Run("autopilot off", func(t *testing.T) {
		env, _, _ := dismissEnv(t)
		var st aiv1.AgentTask
		if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
			t.Fatal(err)
		}
		st.Spec.Autopilot = false
		if err := env.reader.Update(context.Background(), &st); err != nil {
			t.Fatal(err)
		}
		if code, _ := env.postJSON(t, shopDismiss, nil); code != http.StatusConflict {
			t.Errorf("autopilot off: %d", code)
		}
	})
	t.Run("closed", func(t *testing.T) {
		env, _, _ := dismissEnv(t)
		if code, _ := env.postJSON(t, "/api/v1/projects/team-a/shop/milestones/4/followups/dismiss", nil); code != http.StatusConflict {
			t.Errorf("a milestone without tasks: %d", code)
		}
	})
	t.Run("the comment is gone", func(t *testing.T) {
		env, fake, _ := dismissEnv(t)
		fake.IssueNotes[77] = fake.IssueNotes[77][:1]
		if code, _ := env.postJSON(t, shopDismiss, nil); code != http.StatusConflict || len(fake.IssueNotes[77]) != 1 {
			t.Errorf("no thread: %d", code)
		}
	})
}

func TestDismissFollowUpsGitLabRefuses(t *testing.T) {
	t.Run("not the tracking issue", func(t *testing.T) {
		env, fake, _ := dismissEnv(t)
		fake.Issues[77].Author = gitlab.User{ID: 5}
		if code, _ := env.postJSON(t, shopDismiss, nil); code != http.StatusConflict || len(fake.IssueNotes[77]) != 3 {
			t.Errorf("a foreign issue: %d", code)
		}
	})
	for _, c := range []struct{ gitlab, want int }{
		{http.StatusForbidden, http.StatusForbidden},
		{http.StatusInternalServerError, http.StatusBadGateway},
		{http.StatusUnauthorized, http.StatusConflict},
	} {
		env, fake, _ := dismissEnv(t)
		fake.NoteErrors = map[int64]error{77: &gitlab.Error{StatusCode: c.gitlab, Method: "POST", Path: "notes"}}
		code, out := env.postJSON(t, shopDismiss, nil)
		if code != c.want {
			t.Errorf("GitLab %d: %d %v, want %d", c.gitlab, code, out, c.want)
		}
		if c.gitlab == http.StatusUnauthorized && out["login"] != pathGitLabLogin {
			t.Errorf("a rejected token asks to connect again: %v", out)
		}
	}
}
