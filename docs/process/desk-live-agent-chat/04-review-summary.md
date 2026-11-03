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

## Slice 6

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | (orchestrator) open failure wrote an empty reply without error | real | reply carries the error |
| 2 | (orchestrator) lost agent → `tick` reported work done → immediate retry loop | real | failed turn reports no work; Loop waits a poll interval; test |
| 3 | (orchestrator) state saved on every claude stream line | real | save only when the session ID changes |
| 4 | (orchestrator) watch on every task of the namespace | real | field selector `metadata.name` |
| 5 | (orchestrator) chat used the profile engine, runs the label engine: separate sessions | real | follow `state.ImplementEngine`; test |
| 6 | (orchestrator) state file written in place, read concurrently by the interrupt watcher | real | write and rename |
| 7 | `chatSessionKey` kept after a lost agent: retries prompt a session the restarted container does not have | real | cleared on any prompt error; reopen test |
| 8 | Interrupt landing after the answer marked a finished turn interrupted | real | `chatTurnState` guards cancel vs finish; test |

## Slice 7

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | (orchestrator) dispatcher and reply reconciler replace `chatMessages` with a merge patch from cached reads: a stale read drops a delivered message (lost) or re-adds an answered one (stuck, pod never hibernates) | real | optimistic lock on both patches, dispatch reads from the API server; stale-read test |
| 2 | (orchestrator) a 100-row queued batch could be filled by rows waiting behind full tasks | real (unlikely) | batch of 500 |
| — | cursor-agent review | no findings | — |

## Slice 8

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | (orchestrator) newer messages fetched with an `after=` cursor missed state changes of shown rows (queued → delivered → answered) | real | refresh the newest page and merge by ID |
| 2 | `loadNewer`/`loadOlder` without the load sequence: a response for the previous task merged into the next task's thread | real | sequence check on every load |
| 3 | task switch kept the old thread on screen while loading | real | cleared on switch |
| 4 | a post finishing after a task switch cleared the new task's draft and kept the old one | real | draft key captured at send |
