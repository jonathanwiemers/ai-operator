package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// TestRedactLoggerHidesSecretsInStreamLog drives logEvent, the runner's
// stream log, through RedactLogger: an agent that read a key with `env` and
// repeats it in a command, its text and an error leaves it out of the log.
func TestRedactLoggerHidesSecretsInStreamLog(t *testing.T) {
	const key = "cursor-TESTSECRET" // a value only EnvSecrets knows
	var out strings.Builder
	base := funcr.New(func(prefix, args string) { out.WriteString(prefix + " " + args + "\n") }, funcr.Options{})
	log := RedactLogger(base, []string{key}).WithValues("run", "r1 "+key)

	logEvent(log, []byte(`{"type":"assistant","message":{"content":[`+
		`{"type":"text","text":"The key is `+key+` and the token glpat-TESTSECRET"},`+
		`{"type":"thinking","thinking":"export `+key+`"},`+
		`{"type":"tool_use","name":"Bash","input":{"command":"curl -H 'PRIVATE-TOKEN: glpat-TESTSECRET' https://oauth2:`+key+`@gitlab.com"}}]}}`), nil)
	logEvent(log, []byte(`{"type":"tool_call","subtype":"started","tool_call":{"shellToolCall":{"args":{"command":"echo `+key+`"}}}}`), nil)
	log.WithName("x").Error(errors.New("claude failed: "+key), "call failed "+key)

	got := out.String()
	for _, secret := range []string{key, "glpat-TESTSECRET"} {
		if strings.Contains(got, secret) {
			t.Errorf("log holds %q:\n%s", secret, got)
		}
	}
	if n := strings.Count(got, "[redacted]"); n < 7 {
		t.Errorf("want at least 7 redactions, got %d:\n%s", n, got)
	}
}

// leakyEngine answers with a key in its text, its structured answer and its
// error, as a CLI would after the agent ran `env`.
type leakyEngine struct{ key string }

func (e *leakyEngine) Run(context.Context, Invocation) (*Result, error) {
	return &Result{Text: "env says " + e.key, Structured: json.RawMessage(`{"summary":"` + e.key + `"}`), IsError: true},
		fmt.Errorf("claude failed: exit status 1: ANTHROPIC_API_KEY=%s glpat-TESTSECRET", e.key)
}

// TestAgentServerRedactsResponses: errors, answers and session events leave
// the agent container without its credentials or known tokens.
func TestAgentServerRedactsResponses(t *testing.T) {
	const key = "sk-TESTSECRET-agent" // a value only the server's Secrets know
	tmp, err := os.MkdirTemp("", "ai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	sessions := &fakeSessionEngine{start: func(Invocation) (Session, error) {
		return &fakeSession{id: "s1", prompt: func(_ context.Context, _ string, fn func(Event)) (*Result, error) {
			fn(Event{Type: "assistant", Line: json.RawMessage(`{"type":"assistant","text":"` + key + `"}`)})
			return &Result{SessionID: "s1", Text: "done " + key}, errors.New("cursor-agent failed: " + key)
		}}, nil
	}}
	client := serveAgent(t, &AgentServer{Log: logr.Discard(), Secrets: []string{key}, Agent: &LocalAgent{
		Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")},
		Exchange:  filepath.Join(tmp, "exchange"),
		Engines:   map[aiv1.Engine]Engine{aiv1.EngineClaude: &leakyEngine{key}, aiv1.EngineCursor: sessions},
	}})
	ctx := context.Background()

	res, runErr := client.Run(ctx, aiv1.EngineClaude, Invocation{ReadOnly: true})
	if res == nil || runErr == nil {
		t.Fatalf("want the result and the error, got %+v, %v", res, runErr)
	}
	if !json.Valid(res.Structured) {
		t.Errorf("the redacted answer is no JSON: %s", res.Structured)
	}
	if _, err := client.OpenSession(ctx, aiv1.EngineCursor, Invocation{}); err != nil {
		t.Fatal(err)
	}
	var events []Event
	sres, promptErr := client.PromptSession(ctx, "go", func(ev Event) { events = append(events, ev) })

	got := fmt.Sprintf("%+v %v %s %+v %v %s", res, runErr, res.Structured, sres, promptErr, events)
	for _, secret := range []string{key, "glpat-TESTSECRET"} {
		if strings.Contains(got, secret) {
			t.Errorf("response holds %q: %s", secret, got)
		}
	}
	if !strings.Contains(runErr.Error(), "ANTHROPIC_API_KEY=[redacted]") || len(events) != 1 {
		t.Errorf("want the redacted error and one event, got %v, %d events", runErr, len(events))
	}
}

// TestFailureCommentHidesTokens: the error quoted in the failure comment on
// GitLab carries neither the runner's tokens nor a credential of its env.
func TestFailureCommentHidesTokens(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "cursor-TESTSECRET")
	e := newImplementEnv(t)
	e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(*aiv1.AgentTaskSpec) {})
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{}, e.task.Name)
	r.GitLabToken, r.CompanionToken = "runner-TESTSECRET", "companion-TESTSECRET"
	r.run = newRunLog(r.Log, e.task)
	runErr := errors.New("git push https://oauth2:x-TESTSECRET@gitlab.example/a.git: runner-TESTSECRET companion-TESTSECRET cursor-TESTSECRET glpat-TESTSECRET")
	r.reportFailure(context.Background(), e.gl, e.k8sProjectSpec(t), e.task.DeepCopy(), &Outcome{}, runErr, "")

	notes := e.gl.IssueNotes[42]
	if len(notes) == 0 {
		t.Fatal("no failure comment")
	}
	body := notes[len(notes)-1].Body
	if strings.Contains(body, "TESTSECRET") || strings.Count(body, "[redacted]") != 5 {
		t.Errorf("the failure comment holds a secret:\n%s", body)
	}
}

// TestJobLogsInPromptHideTokens: a CI job that printed a token does not put
// it into the CI fix prompt.
func TestJobLogsInPromptHideTokens(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "runner-TESTSECRET")
	gl := gitlabfake.New()
	pid := gl.AddPipeline("abc", "agent/42-x", "failed", gitlab.Job{Name: "deploy", Stage: "test", Status: "failed"})
	for _, j := range gl.Jobs[pid] {
		gl.Traces[j.ID] = "$ curl -H 'PRIVATE-TOKEN: glpat-TESTSECRET' https://ci:runner-TESTSECRET@gitlab.example\nFAIL"
	}
	logs, err := jobLogs(context.Background(), gl, "group/app", pid)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs, "TESTSECRET") || !strings.Contains(logs, "PRIVATE-TOKEN: [redacted]") {
		t.Errorf("the job log for the prompt holds a token:\n%s", logs)
	}
}
