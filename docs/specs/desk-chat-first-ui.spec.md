# Web UI: the Desk (chat-first streams with history)

## Problem

The UI is a set of boards and detail pages built from the resources' current
status. Talking to the agents is tucked into the task and milestone pages,
there is no place that shows what happened across a project over time, and
nothing older than the current status survives: a run that finished, a
pipeline that went red and green again, a comment that moved the pilot, are
gone from the UI once the status moves on. The research Postgres exists but
holds research only.

## Goals

1. **Chat first, for every project.** One stream per project that mixes the
   conversation (issue, MR and tracking-issue comments, the pilot's replies
   and proposals, the operator's answers) with what the agents did (runs,
   pushes, CI, reviews, merges, autopilot changes), **newest at the top**,
   with the message box pinned above it.
2. **History from Postgres.** The operator appends these events to the
   database, so streams reach back beyond the current status: load older,
   jump to a day, search, and 30-day charts (runs, spend, merges per day).
3. **A new shell.** A left rail with *Inbox* (what needs you, across
   projects), *Everything* (all streams), the projects with live state, the
   researches and the engine usage; a right panel with what is true now
   (needs you, the active milestone's queue) and the 30-day history.
4. The boards, task pages and research pages stay reachable from the Desk.

## Non-goals

- A **project-level pilot** (a chat target that is not a milestone's pilot
  or an issue). The composer targets what exists today: a milestone's pilot
  and the project's issues. Follow-up.
- Chat stored in Postgres as the source of truth. GitLab stays the place of
  record and the authority on who may command; the database is a mirror.
- Research runs in the project streams (they are not per project). They
  stay in the rail and on their own pages.
- Backfilling history from before this change, and events missed while the
  operator is down (comments are caught up on the next listing; status
  transitions are not).
- Edited or deleted GitLab comments: the first version seen is kept.
- Per-severity review counts (status has only the blocking count).

## Constraints

- The operator writes, as the database owner; the UI reads as the reader.
  The UI's service account and the UI's database login stay read-only.
- Research run pods (the research writer logins) must **not** be able to
  write history: today `grant()` gives writers INSERT/UPDATE on every
  future table by default privileges.
- Recording never blocks or fails a reconcile; a database outage loses
  events, it does not slow the factory.
- History is off without a research database (`research.database.host`);
  the Desk then shows the current state and says that history needs the
  database.
- Comment bodies can quote code and issue text: showing them needs a grant
  of its own in the access rules (`conversation`, default off), like
  `transcript`. Without it a comment shows as *comment by X on #N* with its
  link.
- Agent output and comments are rendered with the UI's markdown subset, as
  today.
- One MR (the maintainer's call, 2026-11-02), a deliberate exception to the
  < 50-line budget.

## Approach

### 1. Storage (migration `0002_history.sql`)

```sql
CREATE TABLE history_events (
    id            bigserial PRIMARY KEY,
    key           text NOT NULL UNIQUE,      -- deterministic, makes writes idempotent
    at            timestamptz NOT NULL,
    namespace     text NOT NULL,             -- the AgentProject's
    project       text NOT NULL,
    milestone_iid bigint,
    issue_iid     bigint,
    mr_iid        bigint,
    task          text NOT NULL DEFAULT '',
    kind          text NOT NULL,
    actor         text NOT NULL DEFAULT '',
    summary       text NOT NULL DEFAULT '',
    data          jsonb NOT NULL DEFAULT '{}',
    expires_at    timestamptz NOT NULL,
    search        tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', summary), 'A') ||
        setweight(to_tsvector('simple', coalesce(data->>'body', '')), 'B')) STORED
);
-- stream pages (keyset on at, id), milestone scope, search, retention
```

Kinds: `note`, `run.started`, `run.finished`, `push`, `ci`, `review`,
`needs`, `merged`, `closed`, `autopilot`, `proposal`.

`grant()` gives writers their rights on the research tables only: their
default privileges on future tables are revoked, and every call revokes
them on `history_events`. The reader keeps SELECT on everything.

Retention: `--history-retention` (default `8760h`, one year), applied by
the existing hourly retention loop.

### 2. Recording (operator, `internal/history`)

- **Recorder:** a leader-only runnable with a bounded queue that writes in
  batches with `INSERT … ON CONFLICT (key) DO NOTHING`. A full queue drops
  and counts (log once per minute). It waits for the database setup to be
  ready and needs nothing else.
- **Status differ:** an informer handler on AgentTasks compares old and new status on every update (the runner's patches from
  the pod arrive the same way as the operator's) and turns transitions into
  events with deterministic keys:

  | Transition | Event | Key |
  |---|---|---|
  | `phase` → Running for a new run request | `run.started` | `task/<uid>/run/<request>/start` |
  | `run.runCount` grows | `run.finished` (result, summary, cost delta, duration since its start when seen) | `task/<uid>/run/<count>` |
  | `run.pushedSHA` changes | `push` | `task/<uid>/push/<sha>` |
  | `ci.state` becomes final, or Fixing | `ci` (state, pipeline, failed jobs, fix round) | `task/<uid>/ci/<pipeline>/<state>` |
  | `run.reviewedSHA` changes | `review` (blocking findings) | `task/<uid>/review/<sha>` |
  | result NeedsInput/Failed, or `blocked` set | `needs` | `task/<uid>/needs/<count or since>` |
  | autopilot item → Merged | `merged` | `task/<uid>/merged/<iid>` |
  | autopilot phase changes | `autopilot` (phase, reason) | `task/<uid>/autopilot/<phase>/<resourceVersion>` |
  | pilot proposal posted or decided | `proposal` | `task/<uid>/proposal/<id>/<state>` |
  | task deleted (issue closed or out of scope) | `closed` | `task/<uid>/closed` |

  Initial adds (start, resync) produce nothing: only real updates do.
- **Comments:** the project reconciler's GitLab client is wrapped once (in
  `gitlabClient`), so every issue and MR note listing, for whatever reason
  it happens, records the non-system notes it returned (`note/<id>`, with
  author, body, issue or MR IID and the note's time). A bounded set of seen
  note IDs keeps repeat listings from reaching the database. The bot's own
  comments (pilot replies, operator answers) are recorded the same way, with
  `data.bot = true`.

### 3. API (UI server, reader login)

- `GET /api/v1/stream?project=ns/name&milestone=<iid>&kinds=…&q=…&before=<cursor>&after=<cursor>&limit=50`:
  events newest first, keyset cursor `(at, id)`. Without `project`: every
  project the user may see (*Everything*). With `milestone`: the
  milestone's events, its issues' and its tracking issue's (the IIDs come
  from the cache). `q` searches. `after` returns what is newer than the top,
  for live updates. Note bodies only with `conversation`.
- `GET /api/v1/projects/{ns}/{name}/history?days=30`: per day runs, merges,
  spend and events, and totals for the period and the one before.
- `GET /api/v1/inbox`: what needs you now across projects, from the cache
  (tasks in *Needs you*, open proposals).
- `/api/v1/features` gains `history` when the database is configured.
- Writing reuses the existing endpoints (`…/tasks/{task}/notes`,
  `…/milestones/{iid}/notes`).

### 4. Web

- **Shell** (`App.vue`): left rail replaces the top bar: Inbox, Everything,
  projects (status dot, one-line state, needs-you count), researches (with
  the research feature), engine usage, user. Collapses on narrow screens.
- **Stream view** (`/`, `/inbox`, `/projects/:ns/:name`,
  `/projects/:ns/:name/milestones/:iid`): header with scope tabs
  (Everything, the project's milestones, Board) and a 30-day activity
  strip that jumps to a day; composer pinned at the top with a target
  picker (the milestone's pilot, an issue) and `/` command suggestions;
  filter chips (Chat, Needs you, Runs, CI & review, Merged); day dividers;
  one card component per kind; comments of one thread on a page grouped at
  their newest, read top-down inside; *Load older*; live refetch of what is
  newer on change events.
- **Context panel:** needs you, the active milestone's queue, 30-day chart
  and totals.
- The board moves to `/projects/:ns/:name/board`; task, research and run
  pages keep their URLs.

## Steers (implementation, 2026-11-02)

- Only AgentTasks are diffed: the AgentProject status has nothing the
  streams need (usage and webhook state are live in the rail).
- The old milestone page (queue, plan, chat) moved to
  `…/milestones/<iid>/detail`; the milestone URL is its stream.
- The history queries live in `internal/research/store` (same pool and
  migrations); `internal/history` holds the recorder, differ and notes
  wrapper.
- Search leaves out comments of projects without `conversation`, so a
  search cannot reveal what a comment says.
- Kept to one `EventCard` component with a branch per kind instead of a
  file per kind.

## Affected files

- `internal/research/store/migrations/0002_history.sql`, `migrate.go` (grants), `retention.go`
- `internal/history/` (new: recorder, differ, notes wrapper, store queries)
- `internal/controller/agentproject_controller.go` (`gitlabClient` wrap)
- `cmd/operator/main.go` (flags, wiring)
- `internal/ui/` (stream, history, inbox handlers; `conversation` rule; features)
- `charts/ai-operator` (values, README: retention, `conversation`)
- `web/src/` (shell, stream view, cards, composer, context panel, router, types, api)
- `docs/ui.md`, `docs/configuration.md`, `docs/design.md`

## Risks / trade-offs

- **Informer diffs, not instrumented call sites:** one place, catches the
  runner's patches too, but transitions during operator downtime are lost
  and two quick patches can merge into one update (the later state wins).
- **Note volume:** every poll lists notes; the seen-set and `ON CONFLICT`
  keep the database writes to new comments.
- **Comment privacy:** mirroring makes comments readable to UI users without
  GitLab access to the project, hence the `conversation` grant, default off.
- **Grant change:** revoking writers' default privileges is a behaviour
  change for future research tables; they get explicit grants instead.
- **Size:** one large MR is harder to review; the parts are separable by
  directory (store, history, ui, web).

## Validation

- Store: migration and grants against Postgres (`storetest`): writers
  cannot insert into `history_events`, the reader can select; keyset
  paging, milestone scope, search, retention.
- Differ: table tests per transition, including no events on resync and
  idempotent keys.
- Notes wrapper: records non-system notes once, passes errors through.
- UI handlers: authorization (projects, `conversation`), cursors, inbox.
- Web: `make ui`; screenshots of the Desk (light, dark, narrow) on a dev
  server with a stubbed API.

## Delivery

One MR on `desk-chat-first-ui` (GitLab), then the infra change, which must
wait for the release: the UI parses its config strictly and an older UI
would refuse the `conversation` key. The project-level pilot is issue #47.

| MR | Status |
|----|--------|
| [!67](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/67) Desk: chat-first streams with history | opened |
| [example/infra!288](https://gitlab.com/example/infra/-/merge_requests/288) `conversation` grant for the admin rule (after the release) | draft |
