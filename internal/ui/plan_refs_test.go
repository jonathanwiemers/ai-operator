package ui

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// specTaskOf adds shop's Spec task of milestone %iid with the run request.
func specTaskOf(t *testing.T, env *testEnv, iid, runRequest int64) {
	t.Helper()
	if err := env.reader.Create(context.Background(), &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-spec-m1-part-1"},
		Spec:       aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: iid}, RunRequest: runRequest},
	}); err != nil {
		t.Fatal(err)
	}
}

// Draft 1 names draft 2 by its quoted title; creating draft 2 fills in %2
// in draft 1's milestone while it waits, and reports why not otherwise.
func TestPlanMilestoneFillsInRefs(t *testing.T) {
	const text = "Before %draft-2; after %draft-2.\n\nSee \"Draf\" and Draft."
	const stored = `Before "Draft"; after "Draft".` + "\n\nSee \"Draf\" and Draft."
	gitlabErr := &gitlab.Error{StatusCode: http.StatusInternalServerError, Method: "PUT", Path: "projects/acme%2Fshop/milestones/7", Body: "boom"}
	for _, c := range []struct {
		name   string
		setup  func(t *testing.T, env *testEnv, fake *gitlabfake.Fake)
		reason string
	}{
		{"no Spec task", func(*testing.T, *testEnv, *gitlabfake.Fake) {}, ""},
		{"waiting", func(t *testing.T, env *testEnv, _ *gitlabfake.Fake) { specTaskOf(t, env, 1, 0) }, ""},
		{"planning", func(t *testing.T, env *testEnv, _ *gitlabfake.Fake) { specTaskOf(t, env, 1, 1) }, "already planning"},
		{"text changed", func(_ *testing.T, _ *testEnv, fake *gitlabfake.Fake) {
			fake.Milestones[0].Description = strings.ReplaceAll(fake.Milestones[0].Description, `"Draft"`, "draft two")
		}, "text changed in GitLab"},
		{"update fails", func(_ *testing.T, _ *testEnv, fake *gitlabfake.Fake) { fake.UpdateMilestoneError = gitlabErr }, gitlabErr.Error()},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, fake := planSteerer(t)
			logs := captureLog(env)
			withThreeDrafts(t, env, text, "builds on %draft-1")
			if code, out := env.postJSON(t, newPlanMilestone, draftBody(1, text)); code != http.StatusCreated || !reflect.DeepEqual(out["filledIn"], []any{}) {
				t.Fatalf("draft 1: %d %v", code, out)
			}
			before := fake.Milestones[0].Description
			if !strings.HasPrefix(before, stored+"\n") {
				t.Fatalf("draft 1 created as:\n%s", before)
			}
			c.setup(t, env, fake)
			before = fake.Milestones[0].Description
			code, out := env.postJSON(t, newPlanMilestone, draftBody(2, "builds on %draft-1"))
			if code != http.StatusCreated || out["iid"] != float64(2) {
				t.Fatalf("draft 2: %d %v", code, out)
			}
			want := map[string]any{"key": float64(1), "iid": float64(1), "state": "filled"}
			if c.reason != "" {
				want["state"], want["reason"] = "skipped", c.reason
			}
			if !reflect.DeepEqual(out["filledIn"], []any{want}) {
				t.Errorf("filledIn %v, want %v", out["filledIn"], want)
			}
			wantDesc, wantDraft := before, stored
			if c.reason == "" {
				wantDesc, wantDraft = strings.ReplaceAll(before, `"Draft"`, "%2"), `Before %2; after %2.`+"\n\nSee \"Draf\" and Draft."
			}
			if got := fake.Milestones[0].Description; got != wantDesc {
				t.Errorf("milestone 1:\n%s\nwant\n%s", got, wantDesc)
			}
			if d := env.getPlan(t, "shop-plan-new").Status.Drafts[0]; d.PendingRefs != nil || d.Draft != wantDraft || d.Revision != 2 {
				t.Errorf("draft 1: %+v", d)
			}
			wantLog := []string{`"action"="plan.ref.fill"`, `"user"="ada@example.com"`, `"name"="shop-plan-new"`, `"project"="shop"`,
				`"key"=1`, `"milestone"=1`, `"ref"=2`, `"iid"=2`, `"state"="` + want["state"].(string) + `"`}
			if !slices.ContainsFunc(logs.audit(), func(l string) bool {
				return !slices.ContainsFunc(wantLog, func(w string) bool { return !strings.Contains(l, w) })
			}) {
				t.Errorf("no audit line with %v in %q", wantLog, logs.audit())
			}
		})
	}
}

// A long GitLab error is cut, and the owner's token never shows.
func TestPlanMilestoneFillReasonRedacted(t *testing.T) {
	env, fake := planSteerer(t)
	withThreeDrafts(t, env, "see %draft-2", "two")
	if code, out := env.postJSON(t, newPlanMilestone, draftBody(1, "see %draft-2")); code != http.StatusCreated {
		t.Fatalf("draft 1: %d %v", code, out)
	}
	tok := "glpat-abcdefghijklmnop"
	fake.UpdateMilestoneError = &gitlab.Error{StatusCode: http.StatusBadGateway, Method: "PUT", Path: "milestones", Body: tok + strings.Repeat("x", 500)}
	code, out := env.postJSON(t, newPlanMilestone, draftBody(2, "two"))
	fills, _ := out["filledIn"].([]any)
	if code != http.StatusCreated || len(fills) != 1 {
		t.Fatalf("draft 2: %d %v", code, out)
	}
	reason, _ := fills[0].(map[string]any)["reason"].(string)
	if strings.Contains(reason, tok) || !strings.Contains(reason, "[redacted]") || len(reason) > maxRefReason+len("…") {
		t.Errorf("reason %q", reason)
	}
}
