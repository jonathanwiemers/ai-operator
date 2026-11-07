package v1alpha1

import "testing"

func seqs(calls []AgentCall) []int64 {
	var out []int64
	for _, c := range calls {
		out = append(out, c.Seq)
	}
	return out
}

func TestAppendCallsNumbersAfterLastSeq(t *testing.T) {
	var st RunStatus
	AppendCalls(&st, AgentCall{Kind: CallKindImplement}, AgentCall{Kind: CallKindReview, Seq: 99})
	AppendCalls(&st)
	AppendCalls(&st, AgentCall{Kind: CallKindFix})
	if got := seqs(st.Calls); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("seqs = %v, want [1 2 3]", got)
	}
	if st.LastCallSeq != 3 || st.Calls[2].Kind != CallKindFix {
		t.Fatalf("LastCallSeq = %d, calls %+v", st.LastCallSeq, st.Calls)
	}
}

func TestAppendCallsKeepsTheNewest(t *testing.T) {
	st := RunStatus{LastCallSeq: 7}
	for range 60 {
		AppendCalls(&st, AgentCall{Kind: CallKindReview, ContextSeries: []int64{1, 2}, SubagentTurns: []int32{0},
			Models: []CallModelUsage{{Model: "opus", OutputTokens: 5}}, OutputTokens: 5})
	}
	if len(st.Calls) != MaxCalls || st.LastCallSeq != 67 {
		t.Fatalf("%d calls, LastCallSeq %d; want %d and 67", len(st.Calls), st.LastCallSeq, MaxCalls)
	}
	if first, last := st.Calls[0].Seq, st.Calls[MaxCalls-1].Seq; first != 18 || last != 67 {
		t.Fatalf("seqs %d…%d, want 18…67", first, last)
	}
	for i, c := range st.Calls {
		withSeries := i >= MaxCalls-CallsWithSeries
		if (len(c.ContextSeries) > 0) != withSeries || (len(c.SubagentTurns) > 0) != withSeries {
			t.Errorf("call %d (seq %d): series %v, turns %v; want kept only on the newest %d", i, c.Seq, c.ContextSeries, c.SubagentTurns, CallsWithSeries)
		}
		if len(c.Models) != 1 || c.OutputTokens != 5 {
			t.Errorf("call %d lost its totals or models: %+v", i, c)
		}
	}
}

func TestAppendCallsDoesNotShareTheOldList(t *testing.T) {
	st := RunStatus{}
	AppendCalls(&st, AgentCall{Kind: CallKindSpec, ContextSeries: []int64{1}})
	old := st.Calls
	for range CallsWithSeries {
		AppendCalls(&st, AgentCall{Kind: CallKindSpec})
	}
	if len(old[0].ContextSeries) != 1 {
		t.Fatal("trimming the series changed the list the status had before")
	}
}

func TestAppendRunCheckpointKeepsTheNewest(t *testing.T) {
	var st RunStatus
	for i := range MaxRunCheckpoints + 3 {
		AppendRunCheckpoint(&st, RunCheckpoint{Reason: "pod-lost", Run: int64(i + 1)})
	}
	if n := len(st.RunCheckpoints); n != MaxRunCheckpoints {
		t.Fatalf("%d checkpoints, want %d", n, MaxRunCheckpoints)
	}
	if first, last := st.RunCheckpoints[0].Run, st.RunCheckpoints[MaxRunCheckpoints-1].Run; first != 4 || last != MaxRunCheckpoints+3 {
		t.Fatalf("runs %d…%d, want 4…%d (the oldest dropped)", first, last, MaxRunCheckpoints+3)
	}
}
