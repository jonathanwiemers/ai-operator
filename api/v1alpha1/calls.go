package v1alpha1

// MaxCalls is how many calls status.run.calls keeps; CallsWithSeries of the
// newest keep their context series, so the status stays small.
const (
	MaxCalls        = 50
	CallsWithSeries = 10
)

// AppendCalls numbers calls after st.LastCallSeq and appends them to
// st.Calls, keeping the newest MaxCalls. All but the newest CallsWithSeries
// lose their context series and subagent turns; totals and models stay.
func AppendCalls(st *RunStatus, calls ...AgentCall) {
	if len(calls) == 0 {
		return
	}
	out := make([]AgentCall, 0, len(st.Calls)+len(calls))
	out = append(out, st.Calls...)
	for _, c := range calls {
		st.LastCallSeq++
		c.Seq = st.LastCallSeq
		out = append(out, c)
	}
	if n := len(out) - MaxCalls; n > 0 {
		out = out[n:]
	}
	for i := range len(out) - CallsWithSeries {
		out[i].ContextSeries, out[i].SubagentTurns = nil, nil
	}
	st.Calls = out
}

// MaxRunCheckpoints is how many checkpoints status.run.runCheckpoints keeps.
const MaxRunCheckpoints = 20

// AppendRunCheckpoint appends cp to st.RunCheckpoints, keeping the newest
// MaxRunCheckpoints.
func AppendRunCheckpoint(st *RunStatus, cp RunCheckpoint) {
	out := append(st.RunCheckpoints[:len(st.RunCheckpoints):len(st.RunCheckpoints)], cp)
	if n := len(out) - MaxRunCheckpoints; n > 0 {
		out = out[n:]
	}
	st.RunCheckpoints = out
}
