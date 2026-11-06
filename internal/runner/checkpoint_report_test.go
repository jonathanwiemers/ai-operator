package runner

import (
	"reflect"
	"testing"
)

func TestCheckpointLines(t *testing.T) {
	saved := func(reason string) checkpointNote {
		return checkpointNote{Reason: reason, CheckpointResult: CheckpointResult{SHA: "abc1234def5678", Files: 3, Added: 120, Deleted: 8}}
	}
	line := func(why string) string {
		return "💾 Saved a checkpoint (`abc1234d`, 3 files, +120/−8) because " + why + "."
	}
	tests := []struct {
		name  string
		notes []checkpointNote
		want  []string
	}{
		{"none", nil, nil},
		{"empty", []checkpointNote{{Reason: cutUsageLimit}}, nil},
		{"usage limit", []checkpointNote{saved(cutUsageLimit)}, []string{line("the usage limit was reached")}},
		{"engine error", []checkpointNote{saved(cutEngineError)}, []string{line("the agent call failed")}},
		{"timeout", []checkpointNote{saved(cutTimeout)}, []string{line("the agent call timed out")}},
		{"budget", []checkpointNote{saved(cutBudgetStop)}, []string{line("the budget is used")}},
		{"restart", []checkpointNote{saved(cutContainerRestart)}, []string{line("the agent container restarted")}},
		{"pod lost", []checkpointNote{saved(cutPodLost)}, []string{line("the pod was lost")}},
		{"oom", []checkpointNote{saved(cutOOMKill)}, []string{line("the agent container ran out of memory")}},
		{"skipped", []checkpointNote{{Reason: cutUsageLimit, CheckpointResult: CheckpointResult{SHA: "abc1234def5678", Files: 3, Added: 120, Deleted: 8,
			Skipped: []CheckpointSkip{{Path: "big.bin", Why: "12.3 MB, over checkpoint.maxFileBytes"}, {Path: ".env", Why: "may hold secrets"}}}}},
			[]string{line("the usage limit was reached"),
				"Left out of the checkpoint (still in the workspace): `big.bin` (12.3 MB, over checkpoint.maxFileBytes), `.env` (may hold secrets)."}},
		{"aborted", []checkpointNote{{Reason: cutEngineError, CheckpointResult: CheckpointResult{Aborted: "merge"}}},
			[]string{"A merge was in progress; it was aborted and nothing was saved, half-resolved conflicts are never committed."}},
		{"refused", []checkpointNote{{Reason: cutEngineError, CheckpointResult: CheckpointResult{Refused: "the workspace is on main"}}},
			[]string{"No checkpoint: the workspace is on main."}},
		{"defused", []checkpointNote{{Reason: cutEngineError, CheckpointResult: CheckpointResult{Refused: "on\n/close"}}},
			[]string{"No checkpoint: on\n​/close."}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkpointLines(tc.notes); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
