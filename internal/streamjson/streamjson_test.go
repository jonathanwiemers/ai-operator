package streamjson

import (
	"testing"
	"time"
)

func TestUsageReadingWithoutWindows(t *testing.T) {
	u := UsageReading([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`))
	if u == nil || u.Status != "rejected" || len(u.Windows) != 0 {
		t.Fatalf("expected the status alone, got %+v", u)
	}
	if u := UsageReading([]byte(`{"type":"rate_limit_event"}`)); u != nil {
		t.Fatalf("an event without info gives no reading, got %+v", u)
	}
}

// Windows come sorted by name, as whole percents, with a reset time only
// when claude sent one.
func TestUsageReadingWindows(t *testing.T) {
	u := UsageReading([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","unifiedWindows":{
		"seven_day":{"utilization":0.915,"resetsAt":1767225600},
		"five_hour":{"utilization":0.004}}}}`))
	if u == nil || u.Status != "allowed_warning" || len(u.Windows) != 2 {
		t.Fatalf("reading %+v", u)
	}
	five, week := u.Windows[0], u.Windows[1]
	if five.Name != "five_hour" || five.Percent != 0 || five.ResetsAt != nil {
		t.Errorf("five_hour %+v", five)
	}
	if week.Name != "seven_day" || week.Percent != 92 || week.ResetsAt == nil || !week.ResetsAt.Time.Equal(time.Unix(1767225600, 0)) {
		t.Errorf("seven_day %+v", week)
	}
	if u := UsageReading([]byte(`not json {"rate_limit_info":{"status":"rejected"}}`)); u != nil {
		t.Fatalf("a malformed line gives no reading, got %+v", u)
	}
}

func TestCallsTool(t *testing.T) {
	for line, want := range map[string]bool{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"x"},{"type":"tool_use","name":"Bash"}]}}`: true,
		// The answer itself is no tool call.
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"StructuredOutput"}]}}`: false,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"tool_use"}]}}`:             false,
		`{"type":"result"}`: false,
		`garbage`:           false,
	} {
		if got := CallsTool([]byte(line)); got != want {
			t.Errorf("CallsTool(%s) = %v", line, got)
		}
	}
}

func TestParseEvent(t *testing.T) {
	ev := ParseEvent([]byte(`{"type":"result","subtype":"error_max_turns","is_error":true,"result":"x"}`))
	if ev != (Event{Type: "result", Subtype: "error_max_turns", IsError: true}) {
		t.Errorf("event %+v", ev)
	}
	if ev := ParseEvent([]byte("Error: not logged in")); ev != (Event{}) {
		t.Errorf("other output has no type, got %+v", ev)
	}
}
