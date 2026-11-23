package streamjson

import "testing"

func TestUsageReadingWithoutWindows(t *testing.T) {
	u := UsageReading([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`))
	if u == nil || u.Status != "rejected" || len(u.Windows) != 0 {
		t.Fatalf("expected the status alone, got %+v", u)
	}
	if u := UsageReading([]byte(`{"type":"rate_limit_event"}`)); u != nil {
		t.Fatalf("an event without info gives no reading, got %+v", u)
	}
}
