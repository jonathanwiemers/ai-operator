# Desk: live chat with the agents

## Problem

Talking to an agent goes through GitLab. A message written in the Desk is
posted as a GitLab comment with the user's token; the operator finds it on
its next poll (or webhook), turns it into a run request, starts or wakes the
pod, and the runner reads the issue and MR threads again to build a one-shot
prompt for `claude -p` or `cursor-agent -p`. The answer comes back as a
comment. That costs:

- **Latency:** a reply takes a poll, a pod start and a full context rebuild,
  even for "why did you pick this approach?".
- **Tokens and code:** every run re-reads and re-prompts the threads;
  routing (`pilotPrefix`, `@bot`, watermarks, owner IDs, quotes and code
  that do not count) exists only to tell commands from conversation.
- **A second identity:** a user needs a connected GitLab account to talk to
  an agent, and the Desk mirrors GitLab instead of owning the chat.
- **No interruption:** steering reaches the agent between runs only (the
  live-transcript spec left injection for "a later phase").

The question was whether the agent CLIs speak A2A so the Desk could talk to
them directly. Neither does (2026-11): Claude Code has a feature request
(anthropics/claude-code#104852) and community wrappers, cursor-agent has
neither. A2A is also the wrong shape: it is agent to agent across vendors,
with tasks and agent cards. A person chatting with a coding agent is what
the **Agent Client Protocol (ACP)** is for, which cursor-agent supports
officially (`agent acp`), and Claude Code covers the same need with its
streaming input mode.

## Goals

1. **Chat in the Desk goes straight to the agent's live session.** A
   message to an issue's agent (implementer) or a milestone's pilot reaches
   its CLI session within seconds when the pod runs, and starts the pod when
   it does not. The answer streams back into the stream as it is written.
2. **One session per task.** Chat turns and operator runs (implement, CI
   fix, review fixes) share the same CLI session, so the agent knows what
   was said and what it did. They never overlap.
3. **Interrupt.** A user can stop the current turn and say something else.
4. **Postgres owns the Desk chat.** Messages and answers are stored in the
   research database and shown in the stream without a GitLab round trip.
5. **GitLab keeps the code record:** branch, MR, pushes, CI, the run report
   on the MR. Work done in a chat turn is committed and pushed like a run's.
6. **GitLab comments can be switched off as input**, per project, so a
   project can run on Desk chat alone.
7. **Nothing is lost on a restart.** A message, an answer or an interrupt
   survives a restart or crash of the operator (also a leader change), the
   UI, the agent pod (runner or agent container, eviction, hibernation) and
   a short database outage: it is delivered once it can be, and shown
   once. Only the live token stream of a turn that a crash cuts off is
   lost; the turn is answered again.

## Non-goals

- A2A, agent cards, or agents of other systems calling ours. Revisit when a
  CLI ships it or we need an external caller.
- Talking to the agent pod from the browser: everything goes through the UI
  server and the operator; agent pods keep no ingress.
- Removing the GitLab comment path from the code. This spec makes it
  optional per project; deleting it is a later spec once Desk chat has run
  on a real project for a few weeks.
- Reviewers, describer, spec agent and research as chat targets: they stay
  one-shot. Chat targets are the implementer of an issue and the pilot.
- A project-level pilot (issue #47).
- Mobile push or e-mail for answers.

## Constraints

- **Subscriptions.** Both engines keep authenticating with the CLI
  credentials they use today (`CLAUDE_CODE_OAUTH_TOKEN`, `CURSOR_API_KEY`);
  no SDK that needs an API key.
- **Security boundaries stay** (design.md, Security): the agent container
  has only model credentials; the runner has the GitLab token; the UI's
  service account stays read-only; the UI's database login stays read-only
  for everything but the new chat table; agent pods get no database login.
- **Agent text is untrusted** like today: answers are made inert before
  they reach GitLab, rendered with the UI's markdown subset in the Desk,
  never parsed as commands.
- **Usage limits and fallback** apply to chat turns as to runs: a limited
  engine defers the turn (with a visible notice) or uses the role's
  fallback, which starts that engine's own session.
- **Budget.** A chat turn gets its role's `maxBudgetUSD` as a per-turn cap
  (claude), like a call today, and does not count as a run for
  `maxRunsPerTask`; a per-task chat cap (`agents.chat.maxTurnsPerDay`,
  default 50) keeps a loop or a stuck UI from draining a subscription.
- Releases: new UI config keys are released before the infra values use
  them (`yaml.UnmarshalStrict`).

## Approach

### 1. Engine sessions (runner, agent server)

A new interface next to `Engine` in `internal/runner/engine.go`:

```go
// Session is a long-lived conversation with one agent CLI process.
type Session interface {
	// Prompt sends one user turn and streams the agent's events to fn
	// until the turn ends. One turn at a time.
	Prompt(ctx context.Context, text string, fn func(Event)) (*Result, error)
	// Cancel stops the turn in progress; Prompt returns with Cancelled.
	Cancel(ctx context.Context) error
	// ID is the engine's session ID, stored like today's for --resume.
	ID() string
	Close() error
}
```

- **claude:** `claude -p --input-format stream-json --output-format
  stream-json --verbose` plus today's flags (model, effort, budget, system
  prompt, tool lists, `--resume`). Each user turn is one JSON user message
  on stdin; a turn ends with the `result` line, which the existing parser
  already reads. Cancel sends an interrupt control request; if the CLI
  version lacks it, SIGINT to the process and resume the session on the
  next turn. First party, same auth and output parser as today.
- **cursor:** `cursor-agent acp` (Cursor's docs call the binary `agent`;
  MR 2 checks which names the image's install ships) — ACP over stdio,
  JSON-RPC 2.0: `initialize`,
  `authenticate` (`cursor_login`, from `CURSOR_API_KEY`), `session/new` with
  the checkout as working directory or `session/load` with the stored ID,
  `session/prompt` per turn, `session/update` notifications as events,
  `session/cancel` to interrupt. Mode `agent` for the implementer,
  `ask` for the pilot (read-only). `session/request_permission` is answered
  by the agent server: `allow-once` for writing roles, `reject-once` for
  read-only ones, the same line `--force` draws today.
- **Not chosen:** the community ACP adapters for Claude Code
  (`@zed-industries/claude-code-acp` and its renamed and forked variants).
  They run the Agent SDK, their names and auth guidance are in flux, and
  they add a third-party package to the agent image for no feature the
  native stream mode lacks. One ACP client for both engines would be nicer;
  revisit when Anthropic ships ACP or an official adapter.
- **Not chosen (revisit): the vendors' agent SDKs** (Claude Agent SDK,
  Cursor's `@cursor/sdk`). They run the same harnesses as the CLIs, so the
  agent does not get better; they would give typed events, a clean
  interrupt, a per-tool permission callback instead of allow-lists plus a
  reset, and in-process custom tools (e.g. the pilot's proposals as a tool
  instead of a schema). Against them today: billing (design.md chose the
  CLIs because subscriptions authenticate them; using
  `CLAUDE_CODE_OAUTH_TOKEN` with the Agent SDK is not clearly allowed, and
  Cursor bills the SDK per token), and both are TypeScript, so the agent
  container would need a Node session host next to the Go agent server.
  Revisit when Anthropic's policy page allows subscription use with the
  SDK, or if the claude stream-json session proves unreliable in MR 1: an
  SDK session would sit behind the same `Session` interface.
- **Agent server RPC** (`internal/runner/agent.go`) gains
  `session/open`, `session/prompt` (streams events back over the socket as
  newline-delimited JSON on the HTTP response), `session/cancel` and
  `session/close`. One open session per agent container. The transcript
  logging (`transcript.go`) consumes the same events, so the live panel
  works unchanged.
- **One-shot calls stay** for reviewers and the other roles; the
  implementer's operator runs move onto the session too, so chat and runs
  share context (goal 2). A run's prompt becomes one turn of that session.

### 2. Chat storage (migration `0003_chat.sql`)

```sql
CREATE TABLE chat_messages (
    id          bigserial PRIMARY KEY,
    namespace   text NOT NULL,
    project     text NOT NULL,
    task        text NOT NULL,              -- AgentTask the chat targets
    author      text NOT NULL,              -- OIDC subject; '' for the agent
    author_name text NOT NULL DEFAULT '',
    role        text NOT NULL,              -- user | agent | system
    kind        text NOT NULL DEFAULT 'message', -- message | interrupt | command
    body        text NOT NULL,
    reply_to    bigint UNIQUE REFERENCES chat_messages(id),
    client_key  text,                       -- composer retry key
    UNIQUE (author, client_key),
    state       text NOT NULL DEFAULT 'queued', -- queued | delivered | answered | failed | deferred
    at          timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
```

- A new login, **chat writer** (`ui.chat.writerSecret`): `INSERT` on
  `chat_messages` with `role = 'user'` only (a row-level policy), `SELECT`
  on it. The UI uses it only for the composer; everything else stays on the
  reader login.
- The operator (owner login) updates `state` and inserts agent and system
  rows. An `AFTER INSERT` trigger does `pg_notify('chat', id)`.
- The stream (`GET /api/v1/stream`) gains the kinds `chat.user` and
  `chat.agent` from this table; bodies only with the `conversation` grant,
  as for comments.
- Retention: `--history-retention`, like `history_events`.

### 3. Delivery path

```
Desk composer ─POST─▶ UI server ─INSERT (chat writer)─▶ chat_messages
                                                          │ pg_notify
operator (LISTEN chat) ◀──────────────────────────────────┘
   ├─ task hibernated/idle → start pod (run request kind "chat")
   └─ spec.source.chatMessages += {id, kind, text, author}  (like pilotMessages)
runner (watches its AgentTask) ─session/prompt─▶ agent server ─▶ CLI session
   └─ status.chat.replies (bounded) ─▶ operator ─INSERT agent row─▶ chat_messages
```

- **UI:** `POST /api/v1/projects/{ns}/{name}/tasks/{task}/chat` with
  `{body, kind}`; needs the new grant `chat` in the access rules (default
  off), plus the existing CSRF protection, size limit (16 KiB), per-user
  rate limit and audit line without the text. No GitLab token involved.
- **Operator** (`internal/controller/chat.go`): listens on `chat` (and
  polls `state = 'queued'` every `pollInterval` as fallback, since a
  `LISTEN` connection drops). It hands each message to its task like a
  pilot message: `spec.source.chatMessages` with the message ID and a run
  request, at most 20 queued per task (the rest wait in the table). It
  marks the row `delivered`. Messages in a project that has no live chat
  enabled are marked `failed` with a reason.
- **Runner:** gains `watch` on its own AgentTask (RBAC verb added, scoped as
  today). A chat loop owns the session: it takes queued chat messages and
  run requests in order, one turn at a time. A run request waits for the
  current chat turn and vice versa; an `interrupt` message cancels the
  current turn (chat or run) and is sent as the next turn.
- **Answers:** the runner streams events to the agent server log as today
  (live panel), and at the end of a turn writes the answer, made inert, to
  `status.chat.replies` (last 20, each cut to 16 KiB, keyed by message ID).
  The operator copies new replies into `chat_messages` (`role = 'agent'`,
  `reply_to`) and drops answered `chatMessages` from the spec. The Desk
  gets them through the existing change events and the stream query.
- **Work in a chat turn:** after a writing turn, the runner commits,
  bundles and pushes like a run, and adds a short entry to the MR's run
  report ("💬 chat turn by `<user>`: `<one line>`, pushed `<sha>`"), so the
  code record stays complete without copying the conversation to GitLab.
  CI and review follow the push as for any run.
- **Pod lifetime:** a pod with an open chat stays up while messages come
  (`lifecycle.chatIdleTimeout`, default 15m since the last turn), then
  hibernates as today (`lifecycle.idleTimeout`, default 1m, still applies
  to pods without a chat). The session ID is stored as today, so the next
  message after hibernation resumes it from the PVC (`--resume`,
  `session/load`). A cold start shows "starting the agent…" in the stream.

### 3a. Durability (steer, 2026-11-03)

Every hop is at-least-once with a durable record before the next hop, and
every consumer dedupes by the message ID, so a restart anywhere repeats a
step instead of losing it:

| Hop | Durable record | After a restart |
|---|---|---|
| Browser → UI | the row, committed before `201` | no `201`: the composer keeps the draft and retries with the same client key (`client_key` unique per author), so a retry never doubles the message |
| table → operator | row `state = 'queued'` | the operator lists `queued` rows on start and every `pollInterval`; `LISTEN` only makes it faster |
| operator → task | `spec.source.chatMessages` (by ID) | the spec is patched **before** the row is marked `delivered`; a crash in between hands it over again, and an ID already in the spec is skipped |
| task → runner → agent | the spec entry, plus `inFlight` (message ID and session ID) in the runner state on the PVC before the turn is sent | a new runner sends the spec's unanswered messages again, resuming the stored session; for the `inFlight` one it prefixes "your answer to this message may not have arrived" (the resumed session may already hold it) |
| agent → runner | `status.chat.replies` by message ID | replies stay in the status until the operator acknowledges them; the runner prunes only replies whose message has left the spec |
| runner → table | agent row, `UNIQUE (reply_to)` | the operator inserts `ON CONFLICT DO NOTHING`, then drops the message from the spec; a crash in between inserts nothing twice |
| table → browser | the rows | the UI is stateless; the stream reconnects and refetches newer than its top (`after` cursor) |

- **Session ID** is stored in the runner state as soon as the CLI reports
  it (claude's `init` line, ACP `session/new`), not only at the end of a
  turn, so a crash in the first turn can still resume.
- **Interrupts** are rows like messages: one that arrives after its turn
  ended (or after a restart) is dropped with a system row "nothing to
  interrupt".
- **Work in a cut-off turn** stays in the checkout on the PVC; the next
  turn's push publishes it, as after a run interrupted today
  (`status.run.interruptions`).
- **Database outage:** the UI answers `503` and the composer keeps the
  draft; replies wait in the task status (bounded by the 20 queued
  messages per task) until the operator can write them.
- **Order:** messages of a task are sent in ID order; a retried hand-over
  never overtakes a later one (the runner sorts the spec's entries).

### 4. Commands and authority

- The composer's buttons (retry, model/effort, pause, resume, skip, accept
  a pilot proposal) send `kind = 'command'` rows with a structured body
  (`{"cmd":"use","model":"…"}`), never free text. The operator runs them
  with the same functions the `/agent` and `/pilot` parsers call today.
- Who may chat or command: the access rules decide (`chat` grant), by OIDC
  roles, instead of GitLab's project permissions. Pilot owners, today
  pinned by GitLab user ID, gain `ownerSubjects` (OIDC subjects) in the
  milestone header for the Desk path.
- The bot's GitLab comments are never input on either path, as today.

### 5. GitLab comments as input, per project

`spec.workflow.inputs: [gitlab, desk]` (default both, today's behaviour plus
the Desk). With only `desk`:

- Issue and MR notes are no longer read as `/agent` or `/pilot` commands
  or pilot messages; run reports and needs-input questions point to the
  Desk link instead of asking for a reply.
- The implementer's context drops the thread re-read: it has the session.
  A fresh session (archived workspace, engine switch) gets the issue
  description, the agent notes, and the last 20 chat messages from the
  table (passed by the operator in the run request, cut to 30 kB) instead
  of the GitLab threads.
- Labels (`agent::ready`) and milestone planning stay on GitLab.
- MR review threads written by humans in GitLab are still read for review
  fixes: they are code review, not chat.

## Decisions (confirmed by the maintainer, 2026-11-03)

1. **Claude transport:** native stream-json input, not a community ACP
   adapter (one ACP client for both engines would be nicer).
2. **Chat in Postgres as the source of truth,** which reverses the Desk
   spec's non-goal ("GitLab stays the place of record"). Without the
   research database there is no live chat (the composer falls back to
   GitLab comments).
3. **UI gets a narrow write login** (`INSERT` on `chat_messages` only).
   Alternative: the UI calls an operator endpoint authenticated with its
   service account token (TokenReview), which keeps the UI database login
   read-only but adds an internal HTTP API to the operator.
4. **Authority moves to the access rules** for the Desk path: an OIDC
   role with `chat` can steer an agent without being a GitLab member.
5. **Implementer runs move onto the session,** not only chat turns.
   Alternative: chat turns resume the run's session between runs only
   (smaller change, but runs and chat would fork the session).

## Affected files

- `internal/runner/engine.go`, new `session_claude.go`, `session_acp.go`;
  `agent.go` (session RPC); `runner.go` (chat loop, watch); `transcript.go`
  (events from sessions).
- `internal/controller/chat.go` (new: listener, dispatch, replies, commands),
  `agentproject_controller.go` (inputs gate), `task` controller (chat idle
  timeout).
- `api/v1alpha1`: `spec.source.chatMessages`, `status.chat`,
  `workflow.inputs`, `lifecycle.chatIdleTimeout`, `agents.chat`,
  milestone `ownerSubjects`; `make generate manifests`.
- `internal/research/store/migrations/0003_chat.sql`, `migrate.go`
  (chat writer login, row policy), `retention.go`.
- `internal/ui` (chat endpoint, `chat` grant, stream kinds, chat writer
  pool), `web/src` (composer target "agent", streaming answer card,
  interrupt button, cold-start state).
- `charts/ai-operator` (chat writer secret, runner `watch` verb, values,
  README); `Dockerfile.agent` unchanged unless MR 2 finds the ACP entry
  point missing from the Cursor install.
- Docs: `docs/design.md` (architecture, pod topology, security, pilot),
  `docs/ui.md`, `docs/commands.md`, `docs/configuration.md`, `docs/usage.md`.

## Risks / trade-offs

- **Long-lived CLI processes:** a session process can hang or leak memory
  where a one-shot call ends. Mitigation: per-turn timeout as today, close
  and resume the session on any protocol error, agent-lost handling
  extended to sessions.
- **Stream-json input is less documented** than print mode; interrupt
  behaviour may change between Claude Code versions. Covered by contract
  tests against the pinned CLI in CI (recorded transcripts) and a
  fallback to SIGINT plus resume.
- **ACP in cursor is young:** `session/cancel` parameters are not
  documented. Same mitigation; a cancel that fails kills the process and
  resumes.
- **Subscription burn:** chat makes it cheap to start turns. The per-task
  daily cap, budget and the rail's usage meter bound it; the fallback
  routing applies.
- **Record split:** conversation in Postgres, code in GitLab. People who
  only read GitLab see what was pushed and a one-line chat entry, not why.
  The run report links the Desk.
- **New write path into the database** from the UI. Bounded by the row
  policy (user rows only), the grant, CSRF, rate limit and audit line.
- **Authority without GitLab membership:** a Desk role can make an agent
  push to a project the person cannot push to themselves. The access rules
  must mirror who should steer each project; documented, default off.
- **Size:** about ten MRs; the session layer and the chat loop are the
  risky ones and may exceed the 50-line budget (noted per MR).

## Validation

- Sessions: unit tests with recorded claude stream-json and cursor ACP
  transcripts (turns, tool calls, result, cancel, limit messages, protocol
  error); a fake CLI binary for the process handling. A CI job runs one
  real turn per engine against the pinned CLIs when credentials are present
  (manual job).
- Agent server: session RPC over the socket, one session at a time,
  streaming, close on container restart.
- Store: migration, chat writer can insert only user rows of its own
  subject, cannot update or touch other tables; notify fires; retention.
- Durability: one test per hop of the table above that kills the step
  between its durable write and the next (operator before marking
  `delivered`, runner after `inFlight`, operator between reply insert and
  spec update, UI retry with the same client key) and checks one message
  and one answer arrive; a database outage test for replies waiting in
  the status.
- Operator: dispatch (queued → delivered, cap of 20, fallback poll),
  replies copied once, commands mapped to the existing functions, `inputs`
  gate (no note commands with only `desk`).
- Runner: chat and run serialized; interrupt cancels and re-prompts;
  writing turn commits and pushes; replies bounded and inert.
- UI: `chat` grant (403 without), rate limit, audit line without text,
  stream kinds with and without `conversation`.
- Web: `make ui`; screenshots of the streaming answer, interrupt and cold
  start (light, dark, narrow).
- Cluster: enable on Bloop with `inputs: [gitlab, desk]`, chat with one
  implementer and one pilot, interrupt a run, hibernate and resume;
  restart the operator, the UI and the agent pod each in the middle of a
  turn and check nothing is lost or doubled; then a week with
  `inputs: [desk]`.

## Delivery

Each MR released and tried before the next where it changes behaviour.
Production-line budget < 50 where possible; 1, 2 and 6 are expected over
it and say so in their description.

1. **Claude session:** `Session` interface, stream-json session, recorded
   transcript tests. No caller yet.
2. **Cursor session:** ACP client, permission answers, tests.
3. **Agent server session RPC:** open, prompt (streamed), cancel, close;
   transcript logging from session events.
4. **Chat table and login:** migration `0003`, chat writer grants and
   policy, store queries, chart secret.
5. **UI chat endpoint and stream kinds:** `chat` grant, POST with client
   key (retry-safe), stream query, features flag `chat`.
6. **Runner chat loop:** watch, serialize turns and runs, interrupt,
   replies in status, commit/push of writing turns, run report entry;
   `inFlight` and early session ID in the runner state, resend after a
   restart (3a).
7. **Operator dispatch:** listener and poll fallback, `chatMessages`,
   replies to the table, pod start, `chatIdleTimeout`; spec before
   `delivered`, idempotent reply insert before dropping from the spec (3a).
8. **Web:** composer target "agent", streaming answer card, interrupt,
   cold-start state.
9. **Commands and owners:** structured command rows, `ownerSubjects`.
10. **`workflow.inputs`:** gate the note readers, fresh-session context
    from the chat table, run report wording; docs.

Then: Bloop on `[gitlab, desk]`, then `[desk]`; a later spec removes the
GitLab comment path if nothing needs it.

## PRs / MRs

| # | Title | MR | Status |
|---|---|---|---|
| — | docs(spec): Desk live chat with the agents | [!70](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/70) | merged |
| — | docs(spec): Desk chat survives restarts | [!72](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/72) | merged |
| 1 | feat(runner): long-lived claude session with stream-json input | [!71](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/71) | merged |
| 2 | feat(runner): cursor-agent ACP session | [!74](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/74) | merged |
| 3 | feat(runner): agent server session RPC | [!75](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/75) | merged |
| 4 | feat(research): chat_messages table and chat writer login | [!76](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/76) | merged |
| 5 | feat(ui): Desk chat endpoint, chat grant and stream kinds | this MR | open |
