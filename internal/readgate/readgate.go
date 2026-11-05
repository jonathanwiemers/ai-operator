// Package readgate is the read gate: a Claude Code PreToolUse hook
// (`ai-runner read-gate`) that blocks reads of big files into the context
// of a role with subagents. It denies a Read without offset/limit, and a
// Bash cat/head/tail/less/more, of a file over minLines lines, and tells
// the agent to ask a subagent or to read only the section it edits.
// Subagents' calls are always allowed, and every error allows the call
// (fail open): a gate bug must never fail a run.
//
// The package has no Kubernetes imports, so the hook starts fast; it runs
// before every Read and Bash call. See docs/specs/read-gate-and-writer.spec.md.
package readgate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Marker is the fixed text in every deny reason. The runner counts the
// stream's error tool results that contain it.
const Marker = "read gate: "

// Input is the part of Claude Code's PreToolUse hook input the gate reads.
type Input struct {
	Cwd       string          `json:"cwd"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	// AgentID is set only when the call comes from a subagent (claude
	// 2.1.318: "Absent for the main thread, even in --agent sessions").
	AgentID string `json:"agent_id,omitempty"`
	// AgentType is also set on the main thread of an --agent session, so
	// the gate does not use it.
	AgentType string `json:"agent_type,omitempty"`
}

// output is the hook's answer on a deny.
type output struct {
	HookSpecificOutput hookOutput `json:"hookSpecificOutput"`
}

type hookOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// Run reads one hook input from stdin and answers on stdout: nothing to
// allow (claude's own permission flow then decides), the deny JSON to
// deny. Errors and panics allow the call with one line on stderr. The
// caller exits 0 in every case.
func Run(stdin io.Reader, stdout, stderr io.Writer, minLines int, subagents []string) {
	defer func() {
		if r := recover(); r != nil {
			logAllowed(stderr, fmt.Errorf("panic: %v", r))
		}
	}()
	data, err := io.ReadAll(stdin)
	if err != nil {
		logAllowed(stderr, err)
		return
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		logAllowed(stderr, fmt.Errorf("parsing hook input: %w", err))
		return
	}
	deny, reason, err := decide(in, minLines, subagents)
	if err != nil {
		logAllowed(stderr, err)
		return
	}
	if !deny {
		return
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(output{hookOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}}); err != nil {
		logAllowed(stderr, err)
	}
}

// logAllowed writes the single stderr line of a fail-open allow.
func logAllowed(stderr io.Writer, err error) {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	fmt.Fprintf(stderr, "read-gate: allowed after error: %s\n", msg)
}

// Decide reports whether the gate denies the call and why. Errors allow.
func Decide(in Input, minLines int, subagents []string) (deny bool, reason string) {
	deny, reason, err := decide(in, minLines, subagents)
	if err != nil {
		return false, ""
	}
	return deny, reason
}

func decide(in Input, minLines int, subagents []string) (bool, string, error) {
	if in.AgentID != "" {
		return false, "", nil
	}
	switch in.ToolName {
	case "Read":
		return decideRead(in, minLines, subagents)
	case "Bash":
		return decideBash(in, minLines, subagents)
	}
	return false, "", fmt.Errorf("unknown tool %q", in.ToolName)
}

// readInput is the Read tool's input. Fields are raw so that any value
// (number, string) counts as set; only a missing field or null does not.
type readInput struct {
	FilePath string          `json:"file_path"`
	Offset   json.RawMessage `json:"offset"`
	Limit    json.RawMessage `json:"limit"`
	Pages    json.RawMessage `json:"pages"`
}

// exempt are the extensions the Read tool does not read as text lines.
var exempt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".bmp": true, ".ico": true, ".tif": true, ".tiff": true,
	".pdf": true, ".ipynb": true,
}

func decideRead(in Input, minLines int, subagents []string) (bool, string, error) {
	var ri readInput
	if err := json.Unmarshal(in.ToolInput, &ri); err != nil {
		return false, "", fmt.Errorf("parsing Read input: %w", err)
	}
	if isSet(ri.Offset) || isSet(ri.Limit) || isSet(ri.Pages) || ri.FilePath == "" {
		return false, "", nil
	}
	if exempt[strings.ToLower(filepath.Ext(ri.FilePath))] {
		return false, "", nil
	}
	size, over, err := bigFile(resolve(in.Cwd, ri.FilePath), minLines)
	if err != nil || !over {
		return false, "", err
	}
	return true, denyReason(ri.FilePath, size, minLines, subagents,
		"read only the section you edit with offset/limit"), nil
}

func isSet(raw json.RawMessage) bool {
	s := string(bytes.TrimSpace(raw))
	return s != "" && s != "null"
}

// resolve makes path absolute against dir.
func resolve(dir, path string) string {
	if filepath.IsAbs(path) || dir == "" {
		return path
	}
	return filepath.Join(dir, path)
}

// bigFile reports whether path is a regular file with more than minLines
// lines, and its size. A path that is missing or not a regular file is
// not big.
func bigFile(path string, minLines int) (size int64, over bool, err error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !fi.Mode().IsRegular() {
		return 0, false, nil
	}
	over, err = overLines(path, minLines)
	return fi.Size(), over, err
}

// overLines reports whether the file has more than max lines. It reads
// with a fixed buffer and stops at max+1 newlines, so a big file costs no
// more than a small one. A last line without a newline counts.
func overLines(path string, max int) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 32*1024)
	lines, last := 0, byte('\n')
	for {
		n, err := f.Read(buf)
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
			if lines > max {
				return true, nil
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err
		}
	}
	if last != '\n' {
		lines++
	}
	return lines > max, nil
}

// denyReason is the message the agent sees. It always contains Marker.
func denyReason(path string, size int64, minLines int, subagents []string, section string) string {
	ask := "a subagent such as Explore"
	if len(subagents) > 0 {
		ask = "a subagent (" + strings.Join(subagents, ", ") + ")"
	}
	return fmt.Sprintf("File %s is over %d lines (%s; %s%d). Ask %s for what you need from it, or %s.",
		path, minLines, formatSize(size), Marker, minLines, ask, section)
}

func formatSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d bytes", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
}
