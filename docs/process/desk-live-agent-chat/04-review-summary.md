# Review summary — desk-live-agent-chat

One section per slice. Reviewer: cursor-agent (`--mode ask --model auto`), correctness only; orchestrator triages and fixes.

## Slice 1 (!71)

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | `Close` drained `lines` while a `Prompt` could still read its result | real (narrow) | `Close` waits for the process, then for the running turn (`promptMu`), only then drains |
| 2 | Cancel can write an interrupt after the turn's result is queued | harmless | the probe showed claude answers an idle interrupt with `control_response` and the next turn works; no change |
| 3 | Lines after a result would reach the next turn | real (defensive) | `Prompt` drops buffered lines before sending the next user message |
| 4 | Ignored interrupt went straight to kill, spec says SIGINT first | real | after the cancel wait: SIGINT, then kill after the close wait; the session is dead, the caller resumes it by ID |
| — | `runCLI` refactor | no regression found | — |

Added `TestClaudeSessionIgnoredInterruptEndsSession`. Full `go test -race ./internal/runner/` green after the fixes.

## Slice 2

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | `exited()` could win over a buffered response; pending entry not removed | real | check the response channel first on exit, delete the pending entry |
| 2 | `routeUpdate` could block the only reader on a full turn channel, so responses and permission requests stop | real | unbounded per-turn queue with a 1-slot signal; the reader never blocks |

## Slice 3

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | Client's own cancel returned as `agentConnError` | real (would trigger the 3a lost-container path) | return `ctx.Err()` when the context is done |
| 2 | 64 MiB limit applied to the whole NDJSON stream | real for long turns | no limit on the stream |
| 3 | Open closed the live session before the new one started | real | start the new one, close the old one only on success |
