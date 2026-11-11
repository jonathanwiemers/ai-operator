package ui

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestResponsesKeepTheirShape checks that the named responses encode like
// the maps they replaced: the same keys, null where the map had null.
func TestResponsesKeepTheirShape(t *testing.T) {
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	run := ResearchRun{ID: "r1", Status: "succeeded", StartedAt: at}
	result := &ResearchResult{Title: "t"}
	detail := ResearchDetail{}
	features := []Feature{{Name: FeatureNameResearch, Write: true, Namespace: "ns"}}
	msgs := []ChatMessageView{{ID: 1, Role: "user", Body: "hi"}}
	parts := []ThreadPart{{Target: "issue", IID: 3, WebURL: "u", Notes: []NoteView{}}}
	hits := []SearchHit{{RunID: "r1", CreatedAt: at}}
	items := []InboxItem{{Namespace: "ns", Project: "p", Kind: "task", Title: "t", Reason: "r"}}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"me", Me{Username: "jo", Roles: []string{"admin"}, AuthMode: AuthOIDC},
			map[string]any{"username": "jo", "roles": []string{"admin"}, "authMode": AuthOIDC}},
		{"me without roles", Me{Username: "anonymous", AuthMode: AuthNone},
			map[string]any{"username": "anonymous", "roles": []string(nil), "authMode": AuthNone}},
		{"features", FeaturesResponse{Features: features}, map[string]any{"features": features}},
		{"features empty", FeaturesResponse{Features: []Feature{}}, map[string]any{"features": []Feature{}}},
		{"chat post", ChatPostResponse{ID: 42}, map[string]any{"id": int64(42)}},
		{"chat page", ChatPage{Messages: msgs}, map[string]any{"messages": msgs}},
		{"thread", ThreadResponse{Parts: parts}, map[string]any{"parts": parts}},
		{"thread without parts", ThreadResponse{}, map[string]any{"parts": []ThreadPart(nil)}},
		{"research", ResearchResponse{Research: detail, Run: &run, Result: result},
			map[string]any{"research": detail, "run": &run, "result": result}},
		{"research before a run", ResearchResponse{Research: detail},
			map[string]any{"research": detail, "run": (*ResearchRun)(nil), "result": (*ResearchResult)(nil)}},
		{"research runs", ResearchRunsPage{Runs: []ResearchRun{run}}, map[string]any{"runs": []ResearchRun{run}}},
		{"research search", ResearchSearchPage{Hits: hits}, map[string]any{"hits": hits}},
		{"inbox", InboxResponse{Items: items}, map[string]any{"items": items}},
		{"research created", ResearchCreated{Namespace: "ns", Name: "n"}, map[string]string{"namespace": "ns", "name": "n"}},
		{"logout redirect", LoginRedirect{Redirect: "/"}, map[string]string{"redirect": "/"}},
		{"gitlab disconnected", GitLabDisconnected{Status: "disconnected"}, map[string]string{"status": "disconnected"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, want := decoded(t, c.got), decoded(t, c.want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// decoded is v as JSON decoded into a map, so null and absent differ.
func decoded(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
