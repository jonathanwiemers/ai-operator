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
