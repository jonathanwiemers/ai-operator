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

## Slice 3 — Agent server session RPC (`feat/agent-session-rpc`, stacked on slice 2)

- `engine.go`: `SessionEngine` (`StartSession`), implemented by `ClaudeCLI` and `CursorCLI`.
- `agent.go`: `Agent` gains `OpenSession`, `PromptSession`, `CancelSession`, `CloseSession`. `LocalAgent` holds at most one session (same Dir, env and per-run toolchains as `Run`). `AgentServer` routes `session/open|cancel|close` like the other calls; `session/prompt` streams NDJSON (`{"event":…}` per event, flushed, then `{"result":…,"error":…}`), and the request context bounds the turn, so a runner that goes away cancels the turn but keeps the session. `AgentClient.PromptSession` reads the stream; a stream without a final line is an `agentConnError` (lost container), the caller's own cancel is `ctx.Err()`.
- Orchestrator fixes after review: own cancel no longer reported as a lost container, no size limit on the whole stream, a failed open keeps the old session (start new, then close old). Added `TestAgentSessionFailedOpenKeepsPrevious`, the cancel test checks `context.Canceled`.
- Checks: gofmt clean, `go vet ./...` ok, `TMPDIR=/tmp/aio go test -race ./internal/runner/` ok (252 s).

## Slice 4 — Chat table and login (`feat/chat-store`)

- `migrations/0003_chat.sql`: `chat_messages` as in spec §2 plus `reason` (why `failed`/`deferred`); `UNIQUE (author, client_key)`, `UNIQUE (reply_to)`; indexes for task paging, the queued poll and retention; `AFTER INSERT` trigger `pg_notify('chat', id)` on user rows; RLS: everyone may select, inserts only `role = 'user'`, `state = 'queued'`, no `reply_to`, body ≤ 16 KiB (the owner bypasses RLS).
- `migrate.go`: `Grants.ChatWriter` gets `SELECT, INSERT` on the table and the sequence; research writers get `REVOKE ALL` on it. Flag `--research-chat-writer-role`, chart `research.database.chatWriterRole`.
- `chat.go`: `InsertUserMessage` (idempotent by client key, returns the existing ID), `QueuedMessages`, `SetMessageState`, `InsertReply` (`ON CONFLICT (reply_to) DO NOTHING`, marks the message answered in the same transaction), `ChatMessages` (keyset `before`/`after`, newest first), `ListenChat`, `DeleteExpiredChat` (in the retention loop with `--history-retention`).
- Orchestrator fixes after the builder: `SetMessageState` never leaves `answered` (a re-dispatch after a crash can mark a message delivered after its reply landed); `ListenChat` closes its connection instead of returning a listening one to the pool.
- Deviation: the chart's chat writer *secret* (`ui.chat.writerSecret`) moves to slice 5, where the UI reads it; slice 4 only grants the role.

### Tests

- `chat_test.go` against Postgres: grants and RLS (chat writer may insert user rows only, no update/delete, no agent rows, no oversized body), idempotent insert, queue and states (answered stays answered), reply idempotency, paging both ways, `LISTEN` notifications, retention (a reply outlives its expired message).
- `gofmt` clean, `go vet ./...` ok, `go test -race ./internal/research/...` ok on local Postgres 17, `make helm-lint` ok.

## Slice 5 — UI chat endpoint and stream kinds (`feat/ui-chat-endpoint`)

- `internal/ui/chat.go`: `POST …/tasks/{task}/chat` (chat grant, chat writer login, required `clientKey` ≤ 128 characters, 16 KiB, rate limit like comments, audit line without the text; 201 new, 200 known key) and `GET …/tasks/{task}/chat` (reader login, keyset paging, bodies only with `conversation`).
- `config.go`/`authz.go`: rule grant `chat` (not in research rules), `ChatEnabled`/`ChatLogin` from `AI_OPERATOR_UI_CHAT_DB_USER`/`_PASSWORD`; `features.go`: `chat`; `api.go`: `permissions.chat` on the task.
- `stream.go`: kinds `chat.user`/`chat.agent` from `store.ChatStream`, merged with history by `(at, id)`; only when `?kinds=` asks for them, because the current web dedupes events by `id` and chat ids collide with history ids (slice 8 keys by kind and id, then may make them default). With `&milestone=` only that milestone's tasks. Chat is not in `?q=` search.
- `cmd/ui`: `--history-retention` (chart passes `research.historyRetention`) sets `expires_at`. Chart: `ui.chat.writerSecret`, validation that it needs `research.database.host`.
- Deviation: `author_name` is the user name (the session keeps no display name).

### Tests

- `chat_test.go`: config validation, create/retry/no key/oversize/no grant/feature off, GET redaction, stream kinds (opt-in, merge order, redaction, role filter fills a page, empty milestone), merge paging without a gap, features.
- `gofmt` clean, `go vet ./...` ok, `go test -race ./internal/ui/... ./internal/research/...` ok on Postgres 17, `make helm-lint` ok.

## Slice 6 — Runner chat loop (`feat/runner-chat-loop`)

- API: `spec.source.chatMessages` (≤ 20, text ≤ 16 KiB), `status.chat` (`replies` ≤ 20, `lastTurnTime`); generated CRDs.
- `internal/runner/chat.go`: `tick` takes the oldest unanswered non-command message before a run; `chatTurn` opens or reuses the agent-server session (key and engine as spec §3b), saves `chatInFlight` before sending, wraps the text as untrusted data with the author, patches the reply (inert, ≤ 16 KiB) into status, then marks it answered; writable turns commit and push through `publish`; replies whose message left the spec are pruned.
- `runner.go`: `Loop` wakes on a watch of the task (field selector on its name; poll stays the fallback), `Step` closes the chat session before a run; state file written atomically (write and rename).
- Chart: agent Role gets `watch` on agenttasks. `cmd/runner`: client with watch.
- Orchestrator fixes after the builder: open failure answered with its error (was an empty reply); a failed turn backs off one poll interval (was an immediate retry loop on a lost agent); session ID saved once, not on every stream line; watch scoped to the task's name; chat follows the last implementer engine; atomic state writes.

### Tests

- `chat_test.go` (fake session agent, fake client): answer into status, command skipped, order, restart resend with prefix and stored session, agent lost keeps `chatInFlight` and does not spin, reopen after a lost agent, open failure answered with error, usage limit, interrupt cancels and is the next turn, late interrupt does not cancel a finished turn, run closes the session first, pruning, writable turn pushes, engine choice, prompt shape.
- `gofmt` clean, `go vet ./...` ok, `make generate manifests` stable, `go test -race ./internal/runner/ ./api/... ./internal/controller/...` ok, `make helm-lint` ok.

## Slice 7 — Operator dispatch (`feat/operator-chat-dispatch`)

- API: AgentProject `agents.chat` (default off), `lifecycle.chatIdleTimeout` (default 15m).
- `internal/controller/chat.go`: leader-only runnable `Chat` waits for the research database, dispatches queued rows at start, on `LISTEN chat` (retried with backoff) and every 30 s: failed with a reason for commands (until slice 9), missing task, task of another project, project without `agents.chat`; rows behind a task holding 20 messages stay queued; otherwise the message is appended to `spec.source.chatMessages` (ID order, skipped if already there) and only then marked `delivered`. Reconciler `chatreply` copies `status.chat.replies` of messages still in the spec into the table (`InsertReply`; an error reply becomes a `system` row) and then drops them from the spec.
- `agenttask_controller.go`: unanswered chat messages start the pod like a pending run (same suspend, blocked, deferred and capacity gates) without a run request, message "answering chat"; an idle pod stays while `idleTimeout` since the last run or `chatIdleTimeout` since `status.chat.lastTurnTime` has not passed (the longer wins); the fresh check before hibernating includes chat messages.
- Orchestrator fixes after the builder: both writers of `spec.source.chatMessages` patch with an optimistic lock (a merge patch replaces the whole list, so a stale cached task could drop a delivered message or bring back an answered one); dispatch reads tasks from the API server; queued batch raised to 500 so rows behind full tasks do not starve others.
- Deviation from the spec diagram: no "run request kind chat"; the pod starts on the messages themselves.

### Tests

- `internal/controller/chat_test.go` (fake client + Postgres): delivered, idempotent re-dispatch, chat disabled, missing task, command, cap of 20, stale task conflicts and keeps the row queued, reply copied and dropped, error reply as system row, reply insert idempotent, hibernated pod started for chat, idle timeout with chat.
- `gofmt` clean, `go vet ./...` ok, `make generate manifests` stable, `go test -race ./internal/controller/... ./internal/research/... ./api/...` ok, `make helm-lint` ok.

## Slice 8 — Web (`feat/web-desk-chat`)

- `TaskChat.vue`: the task's chat thread (newest page, older pages on demand, agent replies under their message, state chips: waiting, sent to the agent, answered, failed with reason, deferred). Refreshes on live stream events and every 5 s while a message is unanswered and the tab is visible.
- `ChatComposer.vue`: message, and interrupt while the newest message is delivered and unanswered (the agent is on it); the draft and its `clientKey` stay in `sessionStorage` until a 2xx, so a retry or reload reuses the key (3a).
- Task page section "Chat with the agent": shown with the `chat` permission (can post) or the `chat` feature (read).
- Stream: chat kinds opt in per filter (`kindsForFilter`), events keyed by kind and ID (chat and history IDs collide).
- `hack/research-migrate`: also creates `research_chat_writer` (password `chat`) for `make run-ui`; docs/ui.md "Development".

### Tests

- `web/test/stream.test.mjs`: kinds per filter, chat opt-in, event keys.
- `make ui` (type-check, build, 53 node tests) ok; checked on `make run-ui` against the cluster read-only with the local seeded database: thread, reply nesting, state chips, post from the composer.

## Slice 9a — Commands for Implement tasks (`feat/chat-commands`)

- `internal/chatcmd`: strict JSON command bodies (spec §4a), `Routing()` reuses `command.Apply`; model names are one plain token.
- UI: a `command` post must parse (400 otherwise).
- Dispatcher: delivers Implement commands onto the task and wakes the project reconciler (one waker for webhook and chat); fails others ("not supported on this task yet", until 9b). Poll sweep fails delivered rows no longer on their task.
- Project reconciler: `applyChatCommands` (retry, review, use, effort) on every sync, mark `spec.source.lastChatCommandID` and answers in `spec.source.chatAnswers` saved with the run request.
- `chatreply`: copies answers as `system` rows, drops message and answer. Commands do not start or keep a pod (`HasChatTurns`).

### Tests

- chatcmd table; UI 400; dispatch of commands, wake, Spec task refused; apply retry once across syncs, use → labels, review without head; answer copied and dropped; command-only list needs no pod; sweep of a removed task.
- `gofmt` clean, `go vet ./...` ok, `make generate manifests` stable, `go test -race ./...` ok, `make helm-lint` ok.

## Slice 9b — Autopilot verbs, proposals, owner subjects (`feat/chat-commands-pilot`)

- Dispatcher: pause/resume/skip for the Spec task of an autopilot milestone, accept/reject for the Coordinate task.
- Autopilot: `chatCommands` applies the verbs through `autopilot.command`, which now returns its answer and whether it applied (the tracking issue still gets only usage and unknown answers); answers and mark are written to the Spec task after the autopilot status. Verbs that reach a task whose autopilot is off are answered "The autopilot is off for this milestone."
- Pilot: `applyPilotChatCommands` answers proposals through `answerProposal` (new `authorized` flag skips the GitLab owner-ID check); authority by `spec.ownerSubjects`, from the milestone line `<prefix> owner-subjects a b`, which is also cut from the planner's spec text.

### Tests

- Dispatcher per task type; autopilot verbs once across syncs, no GitLab comments; verbs answered when the autopilot is off; accept by owner subject vs. someone else; reject; two proposal answers in one sync keep both answers; owner-subjects parsing.
- `go vet ./...` ok, `go test -race ./...` ok except the pre-existing `internal/webhook` shutdown flake (fails about 1 in 3 runs on main too), `make helm-lint` ok.
## Slice 9c — Web command buttons (`feat/web-chat-commands`)

- `web/src/chatState.ts`: `chatWaiting` (task status message while Queued/Pending/Blocked, "starting the agent…" without a pod, "the agent is answering…" and Interrupt only with a running pod), `commandLabel` (chips like "skip #12" instead of JSON).
- Composer: Retry, Review again, Model… (engine, model, effort, default) on Implement tasks; Pause/Resume and Skip… on autopilot Spec tasks; Accept/Reject per open proposal on Coordinate tasks. Commands use the same clientKey draft as messages.
- Checked with `make run-ui` against netcup01 (read-only) and the local database: waiting chip with the task's capacity message, autopilot buttons.

### Tests

- `web/test/chatState.test.mjs` (7); `make ui` ok, 60 node tests.
