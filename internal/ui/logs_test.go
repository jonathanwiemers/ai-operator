package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// logsEnv logs ada@example.com in with role and makes owners the task's
// owner subjects.
func logsEnv(t *testing.T, cfg *Config, role string, owners ...string) *testEnv {
	t.Helper()
	env := newTestEnv(t, cfg)
	var task aiv1.AgentTask
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop-42-add-paypal"}, &task); err != nil {
		t.Fatal(err)
	}
	task.Spec.OwnerSubjects = owners
	if err := env.reader.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{role: map[string]any{}}}
	env.login(t, "/")
	return env
}

var logsRules = []Rule{
	{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true, Chat: true},
	{Roles: []string{"devs"}, Projects: []string{"team-a/*"}, Chat: true},
}

func (e *testEnv) getText(t *testing.T, path string) (int, http.Header, string) {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

// lines is "line 1\n" … "line n\n".
func lines(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestRawLogs(t *testing.T) {
	env := logsEnv(t, oidcConfig(logsRules...), "leads", "bob@example.com", "ada@example.com")
	if code, _, _ := env.getText(t, shopTask+"/logs?container=runner"); code != http.StatusNotImplemented {
		t.Errorf("without a log source: %d", code)
	}
	env.server.SetLogSource(fakeLogs{"team-a/shop-42-add-paypal/runner": lines(1, 600), "team-a/shop-42-add-paypal/agent": "agent says hi\n"})

	code, h, body := env.getText(t, shopTask+"/logs?container=runner")
	if code != http.StatusOK || body != lines(101, 600) {
		t.Fatalf("default tail: %d, %d bytes", code, len(body))
	}
	if h.Get("Content-Type") != "text/plain; charset=utf-8" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v", h)
	}
	for _, c := range []struct {
		query, want string
	}{
		{"container=runner&tail=3", lines(598, 600)},
		{"container=runner&tail=0", lines(600, 600)},
		{"container=runner&tail=-7", lines(600, 600)},
		{"container=runner&tail=100000", lines(101, 600)},
		{"container=agent", "agent says hi\n"},
	} {
		if code, _, body := env.getText(t, shopTask+"/logs?"+c.query); code != http.StatusOK || body != c.want {
			t.Errorf("%s: %d %q", c.query, code, body)
		}
	}
	for _, c := range []struct {
		path string
		want int
	}{
		{shopTask + "/logs", http.StatusBadRequest},
		{shopTask + "/logs?container=init", http.StatusBadRequest},
		{shopTask + "/logs?container=runner&tail=many", http.StatusBadRequest},
		{"/api/v1/projects/team-a/shop/tasks/nope/logs?container=runner", http.StatusNotFound},
		{"/api/v1/projects/team-b/billing/tasks/billing-7-invoice/logs?container=runner", http.StatusNotFound},
	} {
		if code, _, _ := env.getText(t, c.path); code != c.want {
			t.Errorf("%s: %d, want %d", c.path, code, c.want)
		}
	}
	// No pod: its log is not found.
	env.server.SetLogSource(fakeLogs{})
	if code, _, _ := env.getText(t, shopTask+"/logs?container=runner"); code != http.StatusNotFound {
		t.Errorf("no pod: %d", code)
	}
}

func TestRawLogsForbidden(t *testing.T) {
	for _, c := range []struct {
		name, role string
		owners     []string
	}{
		{"not an owner", "leads", []string{"bob@example.com"}},
		{"no owners", "leads", nil},
		{"no steer grant", "devs", []string{"ada@example.com"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := logsEnv(t, oidcConfig(logsRules...), c.role, c.owners...)
			env.server.SetLogSource(fakeLogs{"team-a/shop-42-add-paypal/runner": "secret\n"})
			if code, _, body := env.getText(t, shopTask+"/logs?container=runner"); code != http.StatusForbidden || strings.Contains(body, "secret") {
				t.Errorf("%d %q", code, body)
			}
			var tr TaskResponse
			if env.get(t, shopTask, &tr); tr.Permissions == nil || tr.Permissions.Logs {
				t.Errorf("permissions %+v", tr.Permissions)
			}
		})
	}
}

// pipeLogs follows a log written by the test through a pipe.
type pipeLogs struct {
	fakeLogs
	r      *io.PipeReader
	follow chan bool
}

func (p pipeLogs) Tail(_ context.Context, _, _, _ string, _ int64, follow bool) (io.ReadCloser, error) {
	p.follow <- follow
	return p.r, nil
}

func TestRawLogsFollow(t *testing.T) {
	env := logsEnv(t, oidcConfig(logsRules...), "leads", "ada@example.com")
	pr, pw := io.Pipe()
	follow := make(chan bool, 1)
	env.server.SetLogSource(pipeLogs{r: pr, follow: follow})
	resp, err := env.client.Get(env.srv.URL + shopTask + "/logs?container=agent&follow=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !<-follow {
		t.Fatalf("status %d, follow not passed on", resp.StatusCode)
	}
	rd := bufio.NewReader(resp.Body)
	// Each chunk reaches the client before the next one is written.
	for _, l := range []string{"first\n", "second\n"} {
		go func() { _, _ = pw.Write([]byte(l)) }()
		if got, err := rd.ReadString('\n'); err != nil || got != l {
			t.Fatalf("got %q %v, want %q", got, err, l)
		}
	}
	// The container stops: the response ends.
	pw.Close()
	if rest, err := io.ReadAll(rd); err != nil || len(rest) != 0 {
		t.Errorf("after the end: %q %v", rest, err)
	}
}

func TestTaskPermissionsLogsRestart(t *testing.T) {
	for _, c := range []struct {
		name          string
		cfg           *Config
		role          string
		owners        []string
		logs, restart bool
	}{
		{"owner with steer and chat", withChat(oidcConfig(logsRules...)), "leads", []string{"ada@example.com"}, true, true},
		{"owner without chat configured", oidcConfig(logsRules...), "leads", []string{"ada@example.com"}, true, false},
		{"owner without steer", withChat(oidcConfig(logsRules...)), "devs", []string{"ada@example.com"}, false, true},
		{"not an owner", withChat(oidcConfig(logsRules...)), "leads", []string{"bob@example.com"}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := logsEnv(t, c.cfg, c.role, c.owners...)
			env.server.SetLogSource(fakeLogs{})
			var tr TaskResponse
			if code := env.get(t, shopTask, &tr); code != http.StatusOK || tr.Permissions == nil {
				t.Fatalf("task: %d", code)
			}
			if tr.Permissions.Logs != c.logs || tr.Permissions.Restart != c.restart {
				t.Errorf("permissions %+v, want logs %v restart %v", tr.Permissions, c.logs, c.restart)
			}
		})
	}
	// Without a log source there are no logs to offer.
	env := logsEnv(t, oidcConfig(logsRules...), "leads", "ada@example.com")
	var tr TaskResponse
	if env.get(t, shopTask, &tr); tr.Permissions == nil || tr.Permissions.Logs {
		t.Errorf("permissions %+v", tr.Permissions)
	}
}
