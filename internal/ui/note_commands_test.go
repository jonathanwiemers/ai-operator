package ui

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func TestNoteCommands(t *testing.T) {
	for _, tc := range []struct {
		body           string
		words          []string
		commands, only bool
	}{
		{"please add tests", nil, false, false},
		{"/agents are cool", nil, false, false},
		{"/agent retry", []string{"retry"}, true, true},
		{"/agent RETRY\n\n /agent: review ", []string{"retry", "review"}, true, true},
		{"/agent pause", nil, true, false},
		{"/agent", nil, true, false},
		{"/agent skip 43", nil, true, false},
		{"/agent retry\nwith more tests", []string{"retry"}, true, false},
	} {
		words, commands, only := noteCommands(tc.body, "/agent")
		if !slices.Equal(words, tc.words) || commands != tc.commands || only != tc.only {
			t.Errorf("%q: %q %v %v, want %q %v %v", tc.body, words, commands, only, tc.words, tc.commands, tc.only)
		}
	}
}

// botNoteEnv is a UI whose comment box posts with the fixed token of the
// shop's bot (ada-pat), with chat; reader and chat are the chat stores, nil
// for none.
func botNoteEnv(t *testing.T, reader, chat *store.Store) (*testEnv, *fakeGitLab) {
	t.Helper()
	gl := newFakeGitLab(t)
	cfg := withChat(oidcConfig(Rule{Roles: []string{"*"}, Steer: true, Chat: true}))
	cfg.GitLab = GitLabConfig{URL: gl.srv.URL, TokenUser: "ada@example.com"}
	cfg.GitLabToken = "fine-grained-pat"
	env := newTestEnv(t, cfg)
	if reader != nil {
		env.server.SetResearchStore(reader)
		env.server.SetChatStore(chat)
	}
	withAutopilot(t, env, gl)
	ctx := context.Background()
	var ap aiv1.AgentProject
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	ap.Spec.GitLab.URL, ap.Status.BotUsername = gl.srv.URL, "ada-pat"
	if err := env.reader.Update(ctx, &ap); err != nil {
		t.Fatal(err)
	}
	var impl aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-42-add-paypal"}, &impl); err != nil {
		t.Fatal(err)
	}
	impl.Status.Run.PushedSHA = "5a0e000"
	if err := env.reader.Update(ctx, &impl); err != nil {
		t.Fatal(err)
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.login(t, "/")
	return env, gl
}

func TestNoteCommandsRefused(t *testing.T) {
	env, gl := botNoteEnv(t, nil, nil)
	for _, path := range []string{shopTask + "/notes", shopMilestone + "/notes"} {
		for _, body := range []string{"/agent pause", "/agent retry\nwith more tests", "/agent skip 43"} {
			code, msg := env.postChatErr(t, path, map[string]string{"target": "issue", "body": body})
			if code != http.StatusBadRequest || msg != commentBoxRefusal {
				t.Errorf("%s %q: %d %q, want 400 with the refusal", path, body, code, msg)
			}
		}
		// The merge grant: only owner subjects, as the button.
		if code, _ := env.postChatErr(t, path, map[string]string{"target": "issue", "body": "/agent merge"}); code != http.StatusForbidden {
			t.Errorf("%s merge without the grant: %d, want 403", path, code)
		}
		// Past the checks, without a chat database.
		if code, _ := env.postChatErr(t, path, map[string]string{"target": "issue", "body": "/agent retry"}); code != http.StatusServiceUnavailable {
			t.Errorf("%s retry: %d, want 503", path, code)
		}
	}
	if len(gl.posted) != 0 {
		t.Fatalf("command lines went to GitLab: %q", gl.posted)
	}
	// Prose without command lines posts as before.
	if code, _ := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": "please add tests"}); code != http.StatusOK ||
		len(gl.posted) != 1 || gl.posted[0] != "issues 42 PAT fine-grained-pat: please add tests" {
		t.Fatalf("prose: %d %q", code, gl.posted)
	}
}

func TestNoteCommandsOwnToken(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"*"}, Steer: true})
	var ap aiv1.AgentProject
	_ = env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap)
	ap.Status.BotUsername = "ada-gl"
	_ = env.reader.Update(context.Background(), &ap)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	// An OAuth token is the user's own, even when it shares the bot's name.
	if code, _ := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": "/agent pause"}); code != http.StatusOK ||
		len(gl.posted) != 1 || gl.posted[0] != "issues 42 Bearer at-1: /agent pause" {
		t.Fatalf("own token: %d %q", code, gl.posted)
	}
}

func TestNoteCommandsRouted(t *testing.T) {
	owner, reader, chat := chatStores(t)
	env, gl := botNoteEnv(t, reader, chat)
	commands := func(task string) []string {
		t.Helper()
		msgs, err := owner.ChatMessages(t.Context(), "team-a", "shop", task, 0, 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range msgs {
			if m.Kind != "command" || m.Author != "ada@example.com" {
				t.Fatalf("message %+v", m)
			}
			out = append(out, m.Body)
		}
		return out
	}
	code, out := env.postJSON(t, shopTask+"/notes", map[string]string{"target": "issue", "body": "/agent retry"})
	if code != http.StatusOK || out["routed"] == nil {
		t.Fatalf("task retry: %d %+v", code, out)
	}
	if got := commands("shop-42-add-paypal"); !slices.Equal(got, []string{`{"cmd":"retry"}`}) {
		t.Fatalf("task retry: %q", got)
	}
	// The milestone composer: the pause card's commands for the paused #42.
	if code, out := env.postJSON(t, shopMilestone+"/notes", map[string]string{"body": "/agent resume\n/agent retry"}); code != http.StatusOK {
		t.Fatalf("milestone: %d %+v", code, out)
	}
	if got := commands("shop-spec-m3-checkout"); !slices.Equal(got, []string{`{"cmd":"resume"}`}) {
		t.Fatalf("milestone resume: %q", got)
	}
	if got := commands("shop-42-add-paypal"); len(got) != 2 {
		t.Fatalf("milestone retry: %q", got)
	}
	if len(gl.posted) != 0 {
		t.Fatalf("command lines went to GitLab: %q", gl.posted)
	}
}

func TestAutopilotNoteCommand(t *testing.T) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{OwnerSubjects: []string{"ada"}}}
	impl := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: 42}}}
	tasks := []aiv1.AgentTask{impl}
	if c, _ := autopilotNoteCommand(tasks, st, "resume", 42, true); c.cmd.Issue != 42 || c.task != st {
		t.Errorf("resume of a hold: %+v", c)
	}
	if c, _ := autopilotNoteCommand(tasks, st, "skip", 42, false); c.cmd.Issue != 42 {
		t.Errorf("skip: %+v", c)
	}
	if _, problem := autopilotNoteCommand(tasks, st, "merge", 42, false); problem == "" {
		t.Error("merge without a pushed head")
	}
	tasks[0].Status.Run.PushedSHA = "abc1234"
	if c, _ := autopilotNoteCommand(tasks, st, "merge", 42, false); c.cmd.SHA != "abc1234" || c.cmd.Issue != 42 {
		t.Errorf("merge: %+v", c)
	}
}
