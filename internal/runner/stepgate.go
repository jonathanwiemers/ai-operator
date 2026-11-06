package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// The step-size gate (`ai-runner step-gate --max-lines N`) is a claude
// PostToolUse hook on Edit, Write and Bash. Once the uncommitted diff of
// the work tree passes N lines it tells the implementer to close its step,
// at most once every stepGateEvery tool calls. Every error means no answer
// (fail open). See docs/specs/checkpoints-and-conventional-commits.spec.md,
// decision 8.

// stepGateEvery is the number of tool calls between two nudges.
const stepGateEvery = 5

// stepGateGitTimeout bounds the git commands of one hook call.
const stepGateGitTimeout = 10 * time.Second

// stepGateTools are the tools whose calls the gate looks at.
var stepGateTools = map[string]bool{"Edit": true, "Write": true, "Bash": true}

// stepGateSessionID keeps the session ID safe to use in a file name.
var stepGateSessionID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// stepGateInput is the part of claude's PostToolUse hook input the gate reads.
type stepGateInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	// AgentID is set only on a subagent's call. Subagents cannot close the
	// step, so their calls are not judged (their edits count on the main
	// thread's next call).
	AgentID string `json:"agent_id,omitempty"`
}

// stepGateState is the per-session state file.
type stepGateState struct {
	// CallsSinceNudge counts the judged tool calls since the last nudge.
	CallsSinceNudge int `json:"callsSinceNudge"`
	// Nudges is the number of nudges of the session.
	Nudges int `json:"nudges"`
}

// StepGate reads one PostToolUse hook input from in and writes the nudge
// (additionalContext JSON) to out when the uncommitted diff in the hook's
// cwd is over maxLines and the cadence allows it. stateDir holds the
// per-session state file; empty means os.TempDir(). On an error nothing is
// written to out: the caller logs the error and exits 0.
func StepGate(in io.Reader, out io.Writer, maxLines int, stateDir string) error {
	if maxLines <= 0 {
		return fmt.Errorf("max lines %d, want > 0", maxLines)
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	var hook stepGateInput
	if err := json.Unmarshal(data, &hook); err != nil {
		return fmt.Errorf("parsing hook input: %w", err)
	}
	if !stepGateTools[hook.ToolName] || hook.AgentID != "" {
		return nil
	}
	if !stepGateSessionID.MatchString(hook.SessionID) {
		return fmt.Errorf("invalid session id %q", hook.SessionID)
	}
	if hook.Cwd == "" {
		return errors.New("hook input without cwd")
	}
	lines, err := stepGateLines(hook.Cwd)
	if err != nil {
		return err
	}
	path := stepGatePath(stateDir, hook.SessionID)
	state, err := readStepGateState(path)
	if err != nil {
		return err
	}
	state.CallsSinceNudge++
	nudge := lines > int64(maxLines) && (state.Nudges == 0 || state.CallsSinceNudge >= stepGateEvery)
	if nudge {
		state.CallsSinceNudge, state.Nudges = 0, state.Nudges+1
	}
	// The state is saved before the answer: a gate that cannot keep its
	// cadence does not nudge at all.
	if err := writeStepGateState(path, state); err != nil {
		return err
	}
	if !nudge {
		return nil
	}
	return json.NewEncoder(out).Encode(map[string]map[string]string{"hookSpecificOutput": {
		"hookEventName":     "PostToolUse",
		"additionalContext": stepGateNudge(lines, maxLines),
	}})
}

// stepGateNudge is the additional context of a nudge.
func stepGateNudge(lines int64, maxLines int) string {
	return fmt.Sprintf("Your uncommitted diff is %d lines (step limit %d). "+
		"Finish this step: get it to build, then return in_progress with its commitMessage.", lines, maxLines)
}

// stepGateLines counts the uncommitted lines (added plus deleted) of the
// repository that holds dir.
func stepGateLines(dir string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), stepGateGitTimeout)
	defer cancel()
	top, err := (&Workspace{Dir: dir}).git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return 0, err
	}
	added, deleted, err := (&Workspace{Dir: top}).UncommittedLines(ctx)
	return added + deleted, err
}

// StepGateNudges returns how often the step-size gate nudged the session,
// 0 when it never did or the state cannot be read.
func StepGateNudges(stateDir, sessionID string) int {
	if !stepGateSessionID.MatchString(sessionID) {
		return 0
	}
	state, err := readStepGateState(stepGatePath(stateDir, sessionID))
	if err != nil {
		return 0
	}
	return state.Nudges
}

func stepGatePath(stateDir, sessionID string) string {
	if stateDir == "" {
		stateDir = os.TempDir()
	}
	return filepath.Join(stateDir, "step-gate-"+sessionID+".json")
}

// readStepGateState reads the state file; a missing file is a new session.
func readStepGateState(path string) (stepGateState, error) {
	var state stepGateState
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("parsing %s: %w", path, err)
	}
	return state, nil
}

// writeStepGateState replaces the state file atomically.
func writeStepGateState(path string, state stepGateState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}
