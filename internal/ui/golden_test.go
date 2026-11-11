package ui

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// sameJSON fails unless got encodes to the JSON of want, a golden literal:
// the same keys and values, absent keys absent.
func sameJSON(t *testing.T, got any, want string) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("golden: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("JSON =\n%s\nwant\n%s", b, want)
	}
}

// TestPauseHistoryJSON pins the milestone's pause history as the web reads
// it: newest first, an open pause without until, resolver and via.
func TestPauseHistoryJSON(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	since := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	until := since.Add(10 * time.Minute)
	env.server.pauses = &fakePauses{h: store.PauseHistory{
		Pauses: []store.Pause{
			{Kind: "ciRed", Reason: "pipeline failed", Item: 42, Since: since, Until: &until, ResolvedBy: "selfHealed", Via: "selfHealed", Seconds: 600},
			{Kind: "unknown", Since: since.Add(time.Hour), Seconds: 120},
		},
		Totals: store.PauseTotals{Count: 2, PausedSec: 720, MeanResumeSec: 600,
			ByKind:     map[string]store.PauseKindTotal{"ciRed": {Count: 1, Sec: 600}, "unknown": {Count: 1, Sec: 120}},
			ByResolver: map[string]int{"selfHealed": 1}, ByVia: map[string]int{"selfHealed": 1}},
	}}
	code, m := env.getJSON(t, shopMilestone)
	if code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	sameJSON(t, m["pauses"], `{
		"pauses": [
			{"kind": "unknown", "since": "2026-11-05T11:00:00Z", "seconds": 120},
			{"kind": "ciRed", "reason": "pipeline failed", "item": 42, "since": "2026-11-05T10:00:00Z",
			 "until": "2026-11-05T10:10:00Z", "resolvedBy": "selfHealed", "via": "selfHealed", "seconds": 600}
		],
		"totals": {
			"count": 2, "pausedSec": 720, "meanResumeSec": 600,
			"byKind": {"ciRed": {"count": 1, "sec": 600}, "unknown": {"count": 1, "sec": 120}},
			"byResolver": {"selfHealed": 1}, "byVia": {"selfHealed": 1}
		}
	}`)
}

// TestChatPageJSON pins a chat page's messages: the store's routing fields
// (namespace, project, task, client key, expiry) stay out, and without the
// conversation grant the body is redacted.
func TestChatPageJSON(t *testing.T) {
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	user := store.ChatMessage{ID: 7, Namespace: "team-a", Project: "shop", Task: "shop-42", Author: "ada@example.com",
		AuthorName: "Ada", Role: "user", Kind: "message", Body: "ship it", ClientKey: "k1", State: "answered",
		At: at, ExpiresAt: at.Add(time.Hour)}
	reply := store.ChatMessage{ID: 8, Namespace: "team-a", Project: "shop", Task: "shop-42", Role: "agent", Kind: "message",
		Body: "on it", ReplyTo: 7, State: "answered", Reason: "delivered late", At: at.Add(time.Minute), ExpiresAt: at.Add(time.Hour)}

	sameJSON(t, ChatPage{Messages: []ChatMessageView{chatView(reply, true), chatView(user, true)}}, `{"messages": [
		{"id": 8, "at": "2026-11-05T10:01:00Z", "role": "agent", "kind": "message", "body": "on it", "replyTo": 7,
		 "state": "answered", "reason": "delivered late"},
		{"id": 7, "at": "2026-11-05T10:00:00Z", "author": "ada@example.com", "authorName": "Ada", "role": "user",
		 "kind": "message", "body": "ship it", "state": "answered"}
	]}`)
	sameJSON(t, ChatPage{Messages: []ChatMessageView{chatView(user, false)}}, `{"messages": [
		{"id": 7, "at": "2026-11-05T10:00:00Z", "author": "ada@example.com", "authorName": "Ada", "role": "user",
		 "kind": "message", "state": "answered", "redacted": true}
	]}`)
}

// TestMilestoneListJSON pins the milestone list as the web reads it: live
// rows with the tasks' cost or the snapshot's spend, stored rows from the
// snapshot alone. A row's spend breakdown stays out.
func TestMilestoneListJSON(t *testing.T) {
	env := milestonesEnv(t)
	shop := store.ProjectKey{Namespace: "team-a", Name: "shop"}
	started := t0.Add(-48 * time.Hour)
	done := func(d int) time.Time { return t0.Add(-time.Duration(d) * 24 * time.Hour) }
	spent := store.SpendTotals{CostUSD: store.USD(4.5), InputTokens: 1000, OutputTokens: 200, Calls: 3}
	env.server.milestoneRows = &fakeMilestones{rows: []store.MilestoneRow{
		{Project: shop, IID: 3, Title: "Old title", State: "open", StartedAt: started, TrackingIID: 70,
			Issues: 7, Merged: 7, FollowUpsOpen: 2, Spend: spent},
		{Project: shop, IID: 7, State: "open", Phase: "done", StartedAt: done(3), DoneAt: done(1)},
		{Project: shop, IID: 1, Title: "First", WebURL: "https://gitlab.example.com/acme/shop/-/milestones/1", TrackingIID: 71,
			State: "closed", Phase: "done", StartedAt: done(30), DoneAt: done(20), Issues: 3, Merged: 2, Open: 1, FollowUpsOpen: 1, Spend: spent},
		{Project: shop, IID: 2, Title: "Second", State: "closed", DoneAt: done(10)},
	}}
	code, l := env.getJSON(t, "/api/v1/milestones?state=all")
	if code != http.StatusOK {
		t.Fatalf("milestones: status %d", code)
	}
	sameJSON(t, l, `{"history": true, "milestones": [
		{"namespace": "team-a", "project": "shop", "iid": 5, "title": "M5", "webURL": "https://gitlab.example.com/acme/shop/-/milestones/5",
		 "state": "paused", "phase": "paused", "progress": {"merged": 0, "total": 0, "open": 0}, "followUpsOpen": 0,
		 "costUSD": "1.00", "costKnown": true, "tokens": 0, "costFrom": "tasks", "startedAt": "2026-11-01T13:00:00Z",
		 "needsYou": true, "stored": false},
		{"namespace": "team-a", "project": "shop", "iid": 4, "title": "M4", "webURL": "https://gitlab.example.com/acme/shop/-/milestones/4",
		 "state": "running", "phase": "running", "progress": {"merged": 2, "total": 4, "open": 2}, "followUpsOpen": 0,
		 "costUSD": "1.00", "costKnown": true, "tokens": 0, "costFrom": "tasks", "startedAt": "2026-11-01T12:00:00Z",
		 "needsYou": false, "stored": false},
		{"namespace": "team-a", "project": "shop", "iid": 3, "title": "Checkout", "webURL": "https://gitlab.example.com/acme/shop/-/milestones/3",
		 "state": "planned", "progress": {"merged": 0, "total": 1, "open": 1}, "followUpsOpen": 2,
		 "costUSD": "4.50", "costKnown": true, "tokens": 1200, "costFrom": "calls", "startedAt": "2026-10-30T08:00:00Z",
		 "needsYou": false, "stored": false},
		{"namespace": "team-a", "project": "shop", "iid": 6, "title": "M6", "webURL": "https://gitlab.example.com/acme/shop/-/milestones/6",
		 "state": "waiting", "phase": "running", "progress": {"merged": 0, "total": 0, "open": 0}, "followUpsOpen": 0,
		 "costUSD": "0.00", "costKnown": true, "tokens": 0, "costFrom": "tasks", "startedAt": "2026-11-01T09:00:00Z",
		 "needsYou": false, "stored": false},
		{"namespace": "team-a", "project": "shop", "iid": 7, "title": "M7", "webURL": "https://gitlab.example.com/acme/shop/-/milestones/7",
		 "state": "done", "phase": "done", "progress": {"merged": 1, "total": 1, "open": 0}, "followUpsOpen": 0,
		 "costUSD": "", "costKnown": false, "tokens": 0, "costFrom": "calls",
		 "startedAt": "2026-10-29T08:00:00Z", "doneAt": "2026-10-31T08:00:00Z", "needsYou": false, "stored": false},
		{"namespace": "team-a", "project": "shop", "iid": 2, "title": "Second", "webURL": "",
		 "state": "done", "progress": {"merged": 0, "total": 0, "open": 0}, "followUpsOpen": 0,
		 "costUSD": "", "costKnown": false, "tokens": 0, "costFrom": "calls", "doneAt": "2026-10-22T08:00:00Z",
		 "needsYou": false, "stored": true},
		{"namespace": "team-a", "project": "shop", "iid": 1, "title": "First", "webURL": "https://gitlab.example.com/acme/shop/-/milestones/1",
		 "state": "done", "phase": "done", "progress": {"merged": 2, "total": 3, "open": 1}, "followUpsOpen": 1,
		 "costUSD": "4.50", "costKnown": true, "tokens": 1200, "costFrom": "calls",
		 "startedAt": "2026-10-02T08:00:00Z", "doneAt": "2026-10-12T08:00:00Z", "needsYou": false, "stored": true}
	]}`)
}

// TestMilestoneIssuesJSON pins the issues of a live and a stored milestone
// with their figures: seconds, the cost chip without the breakdown, an
// unknown cost without calls; the snapshot's issue fields stay out.
func TestMilestoneIssuesJSON(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	figures := paypalFigures()
	first := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	figures.Position, figures.FirstRun = 1, &first
	env.server.milestoneDetails = &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{
		3: {Issues: []store.IssueFigures{figures}}, 4: storedCheckout()}}

	code, m := env.getJSON(t, shopMilestone)
	if code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	sameJSON(t, m["issues"], `[
		{"iid": 42, "title": "Add PayPal", "state": "active", "mergeRequestIID": 5,
		 "mergeRequestURL": "https://gitlab.example.com/acme/shop/-/merge_requests/5", "task": "shop-42-add-paypal",
		 "figures": {"wallSec": 3600, "agentSec": 1500, "pausedSec": 600,
		  "costUSD": "1.50", "costKnown": true, "tokens": 1200, "costFrom": "calls"}},
		{"iid": 43, "title": "Add Klarna", "state": "pending"}
	]`)

	code, m = env.getJSON(t, "/api/v1/projects/team-a/shop/milestones/4")
	if code != http.StatusOK {
		t.Fatalf("stored milestone: status %d", code)
	}
	sameJSON(t, m["issues"], `[
		{"iid": 50, "title": "Refund API", "state": "merged", "mergeRequestIID": 9,
		 "figures": {"wallSec": 7200, "agentSec": 900, "pausedSec": 0,
		  "costUSD": "2.50", "costKnown": true, "tokens": 2300, "costFrom": "calls"}},
		{"iid": 51, "title": "Refund mail", "state": "skipped",
		 "figures": {"wallSec": 0, "agentSec": 0, "pausedSec": 0,
		  "costUSD": "", "costKnown": false, "tokens": 0, "costFrom": "calls"}}
	]`)
}

// TestRetrospectiveFollowUpsJSON pins the follow-ups of a live and a stored
// retrospective: the target's link from the project's web URL, body and
// target absent while empty.
func TestRetrospectiveFollowUpsJSON(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	web := gl.srv.URL + "/acme/shop"
	followUps := func(m map[string]any) any {
		r, _ := m["retrospective"].(map[string]any)
		return r["followUps"]
	}

	posted := metav1.NewTime(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	withRetrospective(t, env, &aiv1.PilotRetrospective{NoteID: 900, PostedAt: &posted, Text: retroText,
		FollowUps: []aiv1.PilotFollowUp{{Title: "Retry CI", Body: "flaky lint"}, {Title: "Docs"}, {Title: "Backfill"}},
		Created:   map[string]int64{"1": 60}, Taken: map[string]int64{"3": 14}})
	code, m := env.getJSON(t, shopMilestone)
	if code != http.StatusOK {
		t.Fatalf("live: status %d", code)
	}
	sameJSON(t, followUps(m), strings.ReplaceAll(`[
		{"number": 1, "title": "Retry CI", "body": "flaky lint", "state": "issue", "targetIID": 60, "targetURL": "WEB/-/issues/60"},
		{"number": 2, "title": "Docs", "state": "open"},
		{"number": 3, "title": "Backfill", "state": "milestone", "targetIID": 14, "targetURL": "WEB/-/milestones/14"}
	]`, "WEB", web))

	d := storedCheckout()
	d.RetroText, d.RetroNoteID, d.RetroPostedAt = retroText, 900, posted.Time
	d.FollowUps = []store.FollowUpRow{{Number: 1, Title: "Retry CI", Body: "flaky lint", State: "issue", TargetIID: 60},
		{Number: 2, Title: "Docs", State: "dismissed"}}
	env.server.milestoneDetails = &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{4: d}}
	code, m = env.getJSON(t, "/api/v1/projects/team-a/shop/milestones/4")
	if code != http.StatusOK {
		t.Fatalf("stored: status %d", code)
	}
	sameJSON(t, followUps(m), strings.ReplaceAll(`[
		{"number": 1, "title": "Retry CI", "body": "flaky lint", "state": "issue", "targetIID": 60, "targetURL": "WEB/-/issues/60"},
		{"number": 2, "title": "Docs", "state": "dismissed"}
	]`, "WEB", web))
}
