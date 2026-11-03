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

## Slice 4

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | `reply_to … ON DELETE CASCADE`: retention of an expired user row deleted its younger reply | real | `ON DELETE SET NULL`; the reply stays until its own `expires_at`, test added |
| 2 | (orchestrator) a late `delivered` could overwrite `answered` | real | `SetMessageState` skips answered rows, test added |
| 3 | (orchestrator) `ListenChat` released a listening connection to the pool | real | the connection is hijacked and closed |

## Slice 5

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | (orchestrator) chat rows in the default stream: the web dedupes by `id`, chat ids collide with history ids | real | chat kinds only with `?kinds=`; slice 8 changes the web keys |
| 2 | (orchestrator) `after` paging kept the newest of the merged rows, skipping the rest | real | keep the oldest `limit` when paging newer; unit test |
| 3 | Milestone with no tasks (or a failed list) streamed the whole project's chat | real | list error is a 500, no task names means no chat |
| 4 | Role filter after `LIMIT` under-filled `kinds=chat.agent` pages | real | roles filtered in SQL |
| 5 | Optional `clientKey` broke retry idempotency (3a) | real | required, 400 without |
| — | equal timestamps across the two tables at a page boundary | accepted | rare; documented in code |
