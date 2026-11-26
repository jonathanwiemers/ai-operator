package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// seedState writes the runner's state as an earlier run left it.
func (e *pilotEnv) seedState(t *testing.T, st state) {
	t.Helper()
	if err := e.r.saveState(st); err != nil {
		t.Fatal(err)
	}
}

func (e *pilotEnv) state(t *testing.T) state {
	t.Helper()
	st, err := e.r.loadState()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// nextQueueRequest asks the queue's pilot one more question in a new run
// request, after mutate.
func (e *pilotEnv) nextQueueRequest(t *testing.T, body string, mutate func(*aiv1.AgentTask)) {
	t.Helper()
	task := e.queuePilot(t)
	task.Spec.RunRequest++
	task.Spec.Source.PilotMessages = append(task.Spec.Source.PilotMessages,
		aiv1.PilotMessage{NoteID: 3000 + task.Spec.RunRequest, IssueIID: 7, Author: "alice", Body: body, Request: task.Spec.RunRequest})
	if mutate != nil {
		mutate(&task)
	}
	if err := e.k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
}

func TestQueuePilotNewRoundStartsAFreshSession(t *testing.T) {
	e := newQueuePilotEnv(t, nil)
	e.seedState(t, state{PilotRound: 2, Sessions: map[string]string{
		"pilot/claude": "sess-round-2", "pilot/cursor": "sess-round-2-cursor", "implement/claude": "sess-impl"}})
	e.step(t)
	if len(e.engine.calls) != 1 || e.engine.calls[0].Resume != "" {
		t.Fatalf("the round's first call starts without a resume: %+v", e.engine.calls)
	}
	if p := e.engine.calls[0].Prompt; !strings.Contains(p, "Round 2: #420 merged; #421 red on lint.") || !strings.Contains(p, "## Queue state") {
		t.Fatalf("the round's first prompt has the note and the queue's state:\n%s", p)
	}
	st := e.state(t)
	if st.PilotRound != 3 || st.Sessions["pilot/cursor"] != "" || st.Sessions["implement/claude"] != "sess-impl" || st.Sessions["pilot/claude"] != "sess-pilot" {
		t.Fatalf("round 3 keeps only its own pilot session: %+v", st)
	}

	// The next call of the same round resumes the round's session.
	e.nextQueueRequest(t, "and #424?", nil)
	e.step(t)
	if len(e.engine.calls) != 2 || e.engine.calls[1].Resume != "sess-pilot" {
		t.Fatalf("the round's next call resumes its session: %+v", e.engine.calls)
	}

	// Round 4 starts fresh again.
	e.nextQueueRequest(t, "a new day", func(p *aiv1.AgentTask) {
		p.Spec.Source.PilotRound = &aiv1.PilotRound{Number: 4, StartedAt: p.Spec.Source.PilotRound.StartedAt, Note: "Round 3: #424 merged."}
	})
	e.step(t)
	if c := e.engine.calls[2]; c.Resume != "" || !strings.Contains(c.Prompt, "Round 3: #424 merged.") {
		t.Fatalf("round 4's first call starts fresh with its note: resume %q\n%s", c.Resume, c.Prompt)
	}
	if st := e.state(t); st.PilotRound != 4 {
		t.Fatalf("the state's round: %d", st.PilotRound)
	}
}

func TestMilestonePilotKeepsItsSession(t *testing.T) {
	e := newPilotEnv(t, "")
	// A milestone's pilot has no rounds, even with a stray one.
	task := e.task(t)
	task.Spec.Source.PilotRound = &aiv1.PilotRound{Number: 9}
	if err := e.k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	e.seedState(t, state{PilotRound: 2, Sessions: map[string]string{"pilot/claude": "sess-milestone"}})
	e.step(t)
	if len(e.engine.calls) != 1 || e.engine.calls[0].Resume != "sess-milestone" {
		t.Fatalf("a milestone's pilot resumes its session: %+v", e.engine.calls)
	}
	if st := e.state(t); st.PilotRound != 2 {
		t.Fatalf("a milestone's pilot leaves the round alone: %d", st.PilotRound)
	}
}

// newRoundNoteEnv is the queue's pilot in round 3, asked for the round's
// note and nothing else, with the round's session saved.
func newRoundNoteEnv(t *testing.T, mutate func(*aiv1.AgentTask)) *pilotEnv {
	t.Helper()
	e := newQueuePilotEnv(t, func(p *aiv1.AgentTask) {
		p.Spec.Source.PilotMessages = nil
		p.Spec.Source.RoundNote = 1
		if mutate != nil {
			mutate(p)
		}
	})
	e.seedState(t, state{PilotRound: 3, Sessions: map[string]string{"pilot/claude": "sess-round-3"}})
	return e
}

func roundNoteOf(t *testing.T, e *pilotEnv) *aiv1.PilotRoundNote {
	t.Helper()
	ps := e.queuePilot(t).Status.Pilot
	if ps == nil || ps.RoundNote == nil {
		t.Fatalf("no round note recorded: %+v", ps)
	}
	return ps.RoundNote
}

func TestQueuePilotWritesTheRoundNote(t *testing.T) {
	e := newRoundNoteEnv(t, nil)
	e.engine.raw = `{"note":"#422 merged. #421 held: lint red. ` + strings.Repeat("ä", aiv1.MaxRoundNote) + `"}`
	e.step(t)
	if len(e.engine.calls) != 1 {
		t.Fatalf("one note call: %+v", e.engine.calls)
	}
	c := e.engine.calls[0]
	if c.JSONSchema != roundNoteSchema || c.Resume != "sess-round-3" {
		t.Fatalf("the note resumes the round's session: schema %q, resume %q", c.JSONSchema, c.Resume)
	}
	for _, want := range []string{"At most 1500 characters", "what is held and why", "## Queue state", `"title": "Fix the login redirect"`} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("the note prompt misses %q", want)
		}
	}
	n := roundNoteOf(t, e)
	if n.Request != 1 || n.Failed != "" || !strings.HasPrefix(n.Note, "#422 merged. #421 held") || utf8.RuneCountInString(n.Note) > aiv1.MaxRoundNote {
		t.Fatalf("the note, at most %d characters: %+v (%d)", aiv1.MaxRoundNote, n, utf8.RuneCountInString(n.Note))
	}
	if notes := botNotes(e.gl, 7); len(notes) != 0 {
		t.Fatalf("the runner never posts the note: %q", notes)
	}
	if r := e.queuePilot(t).Status.Run; r.ObservedRunRequest != 1 || r.LastResult != aiv1.RunResultSucceeded || r.Summary != "wrote the round note" {
		t.Fatalf("run status %+v", r)
	}

	// The note is not written twice for its request.
	e.nextQueueRequest(t, "and now?", nil)
	e.step(t)
	if len(e.engine.calls) != 2 || e.engine.calls[1].JSONSchema != pilotSchema {
		t.Fatalf("the next run only answers: %+v", e.engine.calls)
	}
}

func TestQueuePilotRoundNoteFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(e *pilotEnv)
		mutate func(p *aiv1.AgentTask)
		failed string
		call   bool
	}{
		{name: "the call fails", setup: func(e *pilotEnv) { e.engine.err = errors.New("engine crashed") }, failed: "engine crashed", call: true},
		{name: "a usage limit", setup: func(e *pilotEnv) {
			e.engine.err = &UsageLimitError{Engine: aiv1.EngineClaude, Message: "usage limit"}
		}, failed: "the call hit a usage limit", call: true},
		{name: "an empty note", setup: func(e *pilotEnv) { e.engine.raw = `{"note":"  "}` }, failed: "the note is empty", call: true},
		{name: "no budget left in the round", setup: func(e *pilotEnv) {
			e.patchProject(t, func(p *aiv1.AgentProject) { p.Spec.Pilot.MaxBudgetUSD = "2" })
		}, mutate: func(p *aiv1.AgentTask) {
			p.Status.Run.CostUSD = "5.0000"
			p.Spec.Source.PilotRound.StartCostUSD = "3.0000"
		}, failed: "the pilot's budget for this round ($2, `pilot.maxBudgetUSD`) is used up"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRoundNoteEnv(t, tc.mutate)
			tc.setup(e)
			e.step(t)
			if called := len(e.engine.calls) > 0; called != tc.call {
				t.Fatalf("%d calls, want a call: %v", len(e.engine.calls), tc.call)
			}
			if n := roundNoteOf(t, e); n.Request != 1 || n.Note != "" || !strings.Contains(n.Failed, tc.failed) {
				t.Fatalf("the reason, no note: %+v", n)
			}
			if notes := botNotes(e.gl, 7); len(notes) != 0 {
				t.Fatalf("the operator posts the facts, not the runner: %q", notes)
			}
			// Recorded, not deferred: the operator goes on with the facts.
			if r := e.queuePilot(t).Status.Run; r.ObservedRunRequest != 1 {
				t.Fatalf("the run's request is done: %+v", r)
			}
		})
	}
}

func TestQueuePilotBudgetPerRound(t *testing.T) {
	// Round 3 started at $3 and spent $2 of its $2.
	e := newQueuePilotEnv(t, func(p *aiv1.AgentTask) {
		p.Status.Run.CostUSD = "5.0000"
		p.Spec.Source.PilotRound.StartCostUSD = "3.0000"
	})
	e.patchProject(t, func(p *aiv1.AgentProject) { p.Spec.Pilot.MaxBudgetUSD = "2" })
	e.step(t)
	if len(e.engine.calls) != 0 {
		t.Fatalf("no call without budget: %+v", e.engine.calls)
	}
	want := "🧭 The pilot's budget for this round ($2, `pilot.maxBudgetUSD`) is used up, so it does not answer any more until the next round starts."
	if notes := botNotes(e.gl, 7); len(notes) != 1 || notes[0] != want {
		t.Fatalf("the round's budget answer: %q", notes)
	}

	// Round 4 starts at $5: the pilot answers again.
	e.nextQueueRequest(t, "and now?", func(p *aiv1.AgentTask) {
		p.Spec.Source.PilotRound = &aiv1.PilotRound{Number: 4, StartedAt: p.Spec.Source.PilotRound.StartedAt, StartCostUSD: "5.0000"}
	})
	e.step(t)
	if len(e.engine.calls) != 1 || e.engine.calls[0].MaxBudgetUSD != "2.00" {
		t.Fatalf("round 4 answers with its full budget: %+v", e.engine.calls)
	}
}

func TestPilotBudgetSpentTexts(t *testing.T) {
	spec := aiv1.AgentProjectSpec{Pilot: aiv1.PilotSpec{MaxBudgetUSD: "20"}}
	ms := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &aiv1.MilestoneRef{IID: 3}}}
	if got := pilotBudgetSpent(spec, ms, "there is no assessment"); got != "The pilot's budget for this milestone ($20, `pilot.maxBudgetUSD`) is used up, so there is no assessment." {
		t.Errorf("milestone: %q", got)
	}
	q := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{QueueTask: "shop-queue", Source: aiv1.SourceState{PilotRound: &aiv1.PilotRound{Number: 2}}}}
	if got := pilotBudgetSpent(spec, q, "there is no assessment"); got != "The pilot's budget for this round ($20, `pilot.maxBudgetUSD`) is used up, so there is no assessment until the next round starts." {
		t.Errorf("queue: %q", got)
	}
}
