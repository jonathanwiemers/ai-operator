# Implement — desk-live-agent-chat

One section per slice. Builder: cursor-agent (`--model auto`) in the slice's worktree; orchestrator verifies, fixes small issues, commits signed.

## Slice 1 — Claude stream-json `Session` (`feat/claude-session`)

- `internal/runner/engine.go`: `Session` interface and `Event`; the stream loop of `runCLI` moved into `turnReader` (held messages, usage reading, result), shared by both paths, behaviour unchanged.
- `internal/runner/session_claude.go`: `ClaudeCLI.Start` runs `claude` with `--input-format stream-json` in its own process group; `Prompt` writes one JSON user message and reads to the turn's result (parsed, so key order does not matter); `Cancel` sends an `interrupt` control request (SIGINT if stdin is gone); `Close` closes stdin, kills the group after 10 s, drains the reader.
- Orchestrator fixes after the builder: reader goroutine drained on `Close`, unused context parameters, `var _ Session`, doc comments.
- Size: ~270 production lines (over the 50-line budget, as the spec's delivery plan says for slice 1).

### Tests

- `session_claude_test.go` (fake shell CLIs): two turns in one process with text round trip, result with `type` not first, error result keeps the session usable, process exit mid-turn, cancel then next turn, concurrent prompt refused, close kills a CLI that ignores EOF.
- `gofmt -l internal/` clean, `go vet ./internal/runner/` ok, `TMPDIR=/tmp/aio go test -race -count=1 ./internal/runner/` ok (231 s). The default macOS `TMPDIR` makes `TestAgentServerTakesNoEnvFromTheRunner` fail on unix socket path length, unrelated to this change.

## Slice 2 — Cursor ACP `Session` (`feat/cursor-acp-session`, stacked on slice 1)

- `internal/runner/session_acp.go`: `CursorCLI.Start` runs `cursor-agent acp` in its own process group and does the handshake (`initialize`, `authenticate cursor_login`, `session/new` or `session/load` for a resume, `session/set_mode ask` for read-only roles, `session/set_model` by name or `name[` prefix, `auto` keeps `default[]`). One reader routes JSON-RPC responses by ID, answers `session/request_permission` (allow-once, reject-once when read-only; other agent requests get -32601) and queues `session/update` notifications for the running turn; replayed updates during a load are dropped.
- `Prompt` logs with the transcript's message keys (`agent thinks`, `agent uses a tool`, `agent says`, `agent answers`), returns the last message as the answer, maps `stopReason` (cancelled, max tokens, refusal) to errors, keeps JSON-RPC error text verbatim for the usage-limit parser.
- `Close`: cursor-agent ignores stdin EOF, so SIGTERM after 2 s, then kill.
- `internal/runner/session.go`: shared `boundWriter`, `sessionProc` (wait once, group kill) and timeouts, used by both sessions.
- Tests: `session_acp_test.go` with the test binary as a fake ACP agent (`TestMain`, `AIO_TEST_ACP`), shaped on `testdata/cursor-acp-session.jsonl` (the sanitised real recording).
- Orchestrator fixes after review: the reader never blocks (per-turn queue plus signal instead of a 64-slot channel), a response that arrived just before the process exit is still used, and its pending entry is removed. Added `TestCursorSessionSlowConsumerDoesNotBlockReader`.
- Checks: gofmt clean, `go vet ./...` ok, `TMPDIR=/tmp/aio go test -race ./internal/runner/` ok (251 s).
