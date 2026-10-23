package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseLogLine(t *testing.T) {
	for _, tc := range []struct {
		line string
		ok   bool
		want Entry
	}{
		{`{"level":"info","ts":"2026-10-23T13:50:23Z","logger":"runner","msg":"step started","run":"t#1","step":8,"name":"prepare checkout","branch":"agent/1"}`,
			true, Entry{Time: "2026-10-23T13:50:23Z", Run: "t#1", Kind: "step", StepNo: 8, Step: "prepare checkout", Status: "started"}},
		{`{"msg":"step done","step":8,"name":"push","result":"agent/1 at abc","durationMs":140}`,
			true, Entry{Kind: "step", StepNo: 8, Step: "push", Status: "done", Result: "agent/1 at abc"}},
		{`{"level":"error","msg":"step failed","step":3,"name":"run implementer","error":"boom"}`,
			true, Entry{Kind: "step", StepNo: 3, Step: "run implementer", Status: "failed", Result: "boom"}},
		{`{"msg":"agent CLI started","role":"bugbot","engine":"cursor","model":"auto","readOnly":true,"promptBytes":1726}`,
			true, Entry{Kind: "cli", Role: "bugbot", Engine: "cursor", Model: "auto", ReadOnly: true}},
		{`{"msg":"agent says","text":"Adding the test.","run":"t#1"}`, true, Entry{Run: "t#1", Kind: "says", Text: "Adding the test."}},
		{`{"msg":"agent thinks","text":"hmm"}`, true, Entry{Kind: "thinks", Text: "hmm"}},
		{`{"msg":"agent uses a tool","tool":"shell","input":"go test ./..."}`, true, Entry{Kind: "tool", Tool: "shell", Input: "go test ./..."}},
		{`{"msg":"agent answers"}`, true, Entry{Kind: "answers"}},
		{`{"msg":"agent prompt","role":"implement","engine":"claude","model":"m","system":"S","prompt":"P"}`,
			true, Entry{Kind: "prompt", Role: "implement", Engine: "claude", Model: "m", System: "S", Prompt: "P"}},
		// Everything else stays on the server.
		{`{"msg":"call done","rpc":"bundle","path":"/exchange/x"}`, false, Entry{}},
		{`{"msg":"starting run","token":"glpat-secret"}`, false, Entry{}},
		{`plain text from a CLI`, false, Entry{}},
	} {
		got, ok := parseLogLine([]byte(tc.line))
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s:\n got %+v %v\nwant %+v %v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

// fakeLogs serves fixed container logs.
type fakeLogs map[string]string

func (f fakeLogs) Stream(_ context.Context, ns, pod, container string) (io.ReadCloser, error) {
	l, ok := f[ns+"/"+pod+"/"+container]
	if !ok {
		return nil, fmt.Errorf("container %s not found", container)
	}
	return io.NopCloser(strings.NewReader(l)), nil
}

func readTranscript(t *testing.T, env *testEnv, path string) (entries []Entry, end string, status int) {
	t.Helper()
	resp, err := env.client.Get(env.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", resp.StatusCode
	}
	rd := bufio.NewReader(resp.Body)
	event := ""
	for {
		l, err := rd.ReadString('\n')
		if err != nil {
			return entries, end, resp.StatusCode
		}
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "event: "):
			event = strings.TrimPrefix(l, "event: ")
		case strings.HasPrefix(l, "data: ") && event == "entry":
			var e Entry
			if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "data: ")), &e); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, e)
		case strings.HasPrefix(l, "data: ") && event == "end":
			var d struct{ Reason string }
			_ = json.Unmarshal([]byte(strings.TrimPrefix(l, "data: ")), &d)
			return entries, d.Reason, resp.StatusCode
		}
	}
}

const shopTask = "/api/v1/projects/team-a/shop/tasks/shop-42-add-paypal"

func TestTranscript(t *testing.T) {
	env := newTestEnv(t, oidcConfig(
		Rule{Roles: []string{"shop-devs"}, Projects: []string{"team-a/*"}},
		Rule{Roles: []string{"shop-leads"}, Projects: []string{"team-a/*"}, Transcript: true},
	))
	env.server.SetLogSource(fakeLogs{
		"team-a/shop-42-add-paypal/runner": `{"msg":"step started","step":10,"name":"run implementer","run":"t#2"}` + "\n" +
			`{"msg":"starting run","token":"glpat-secret"}` + "\n",
		"team-a/shop-42-add-paypal/agent": `{"msg":"agent says","text":"Adding the test.","run":"t#2"}` + "\n" +
			`not json` + "\n" + `{"msg":"call done","rpc":"run","path":"/exchange/secret"}`,
	})
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"shop-devs": map[string]any{}}}
	env.login(t, "/")

	var task struct{ Permissions map[string]bool }
	env.get(t, shopTask, &task)
	if task.Permissions["transcript"] {
		t.Fatal("without the transcript grant the app must not offer it")
	}
	if _, _, code := readTranscript(t, env, shopTask+"/transcript"); code != http.StatusForbidden {
		t.Fatalf("without the grant: %d", code)
	}
	if _, _, code := readTranscript(t, env, "/api/v1/projects/team-b/billing/tasks/billing-7-invoice/transcript"); code != http.StatusNotFound {
		t.Fatalf("a project the user cannot see: %d", code)
	}

	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"shop-leads": map[string]any{}}}
	env.login(t, "/")
	env.get(t, shopTask, &task)
	if !task.Permissions["transcript"] {
		t.Fatal("the grant must be offered")
	}
	entries, end, code := readTranscript(t, env, shopTask+"/transcript")
	if code != http.StatusOK || end != "pod stopped" || len(entries) != 2 {
		t.Fatalf("status %d, end %q, entries %+v", code, end, entries)
	}
	b, _ := json.Marshal(entries)
	if strings.Contains(string(b), "secret") {
		t.Fatalf("only known lines and fields may leave the server: %s", b)
	}
	kinds := map[string]bool{}
	for _, e := range entries {
		kinds[e.Kind] = true
	}
	if !kinds["step"] || !kinds["says"] {
		t.Fatalf("both containers are read: %+v", entries)
	}

	// A task without a pod ends at once.
	if _, end, _ := readTranscript(t, env, "/api/v1/projects/team-a/shop/tasks/shop-spec-m3-checkout/transcript"); end != "no pod" {
		t.Fatalf("no pod: %q", end)
	}
}

func TestTranscriptWithoutLogSource(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"*"}, Transcript: true}))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.login(t, "/")
	var task struct{ Permissions map[string]bool }
	env.get(t, shopTask, &task)
	if task.Permissions["transcript"] {
		t.Fatal("without a log source the app must not offer transcripts")
	}
	if _, _, code := readTranscript(t, env, shopTask+"/transcript"); code != http.StatusNotImplemented {
		t.Fatalf("status %d", code)
	}
}
