# Web UI: live agent output, prompts and steering

## Problem

The web UI only shows the state of AgentProjects and AgentTasks. What an agent
is doing right now (its messages and tool calls) is only in the agent
container's log, the prompts it got are not visible anywhere, and to steer a
run you have to switch to GitLab and write a comment there.

## Goals

1. **Live transcript:** the task page streams the running agent's output:
   messages, thinking (claude), tool calls, and the runner's steps.
2. **Prompts:** every agent call shows its role, system prompt (with the
   project's instructions) and prompt, including what autopilot adds.
3. **Steering:** a message box on the task page posts a comment on the issue
   or its MR **as the logged-in user**, so the thread in GitLab stays the
   record and the operator handles it like any other comment: a plain
   comment reaches the agent at its next step, `/agent …` starts a run,
   `/agent use|effort` changes model and effort.
4. **Thread:** the task page shows the issue's and the MR's comments next to
   the transcript.

## Non-goals

- Injecting a message into an agent step while it runs (claude
  `--input-format stream-json`): a later phase. Steering reaches the agent
  between steps, like a GitLab comment does today.
- Keeping transcripts after the pod is gone: the pod stops a minute after
  its run; the run log stays on the MR as today.
- Posting as the bot: bot comments are never commands, and that stays.

## Constraints

- GitLab stays the place of record and the authority on who may comment: the
  UI never posts with the bot token or any shared token.
- The UI's service account stays read-only; it gains reading pod logs in the
  agent namespaces, nothing else.
- Transcripts and prompts can quote code, issue text and CI logs: they need
  their own grant in the access rules, default off.
- Prompts are logged only for projects that opt in.

## Approach

### 1. Live transcript (and prompts)

- **Source:** the agent and runner containers' logs, read by the UI through
  the Kubernetes API (`pods/log`, follow). The pod is named like the task.
- **Filter on the server:** only JSON log lines of known kinds are forwarded,
  with an allow-list of fields: `agent CLI started`, `agent thinks`,
  `agent says`, `agent uses a tool`, `agent answers`, `agent prompt`, and the
  runner's `step started/done/failed`. Never raw lines.
- **API:** `GET /api/v1/projects/{ns}/{name}/tasks/{task}/transcript`
  (server-sent events): the lines of the current run so far, then live, until
  the pod stops. Same access checks as the task, plus the transcript grant.
- **Prompts:** `spec.agents.logPrompts` (default `false`). When on, the agent
  server logs one `agent prompt` line per call: run, role, engine and model,
  system prompt and prompt, each cut at 64 KiB. Autopilot's note, CI job
  excerpts, revisits and conflicts are part of the prompt, so they show.
- **Access:** an access rule gets `transcript: true` to show transcripts and
  prompts to its roles (`ai-operator-admin` on this cluster).
- **RBAC:** the UI's role gets `get` on `pods` and `pods/log` in the
  namespaces of `agent.namespaces` (Roles, not cluster-wide).
- **UI:** a *Live* panel on the task page: a timeline of steps, messages,
  tool calls (with their main argument) and collapsible prompts; it follows
  the stream and says when the pod has stopped.

### 2. GitLab connection and thread

- **GitLab sign-in per user:** a "Connect GitLab" button runs OAuth2 against
  the project's GitLab (authorization code with PKCE) with an application per
  GitLab host from the chart values (`ui.gitlab.apps: [{url, clientID,
  existingSecret}]`). GitLab has no scope narrower than `api` that can post
  notes, so the app asks for `api`.
- **Tokens:** access and refresh token in a separate encrypted, `HttpOnly`
  cookie (same scheme as the session); never sent to the browser's
  JavaScript, never logged. Disconnect clears it.
- **Thread:** with a connection the task page shows the issue's and the MR's
  comments (read with the user's token, so GitLab decides what they may see),
  merged into the timeline. Without one: transcript and links only.

### 3. Steering

- **Message box** on the task page: target issue (default) or MR, and the
  text. Buttons for the common commands: run with instructions (`/agent
  …`), model/effort (`/agent use …`), retry; on a spec task with autopilot,
  pause / resume / skip on the tracking issue.
- **Posting:** `POST /api/v1/projects/{ns}/{name}/tasks/{task}/notes` posts
  the note with the user's GitLab token. The UI does not interpret it: the
  operator picks it up on its next poll, exactly like a comment written in
  GitLab.
- **Access:** an access rule gets `steer: true`; without it the box is
  hidden and the endpoint answers 403. GitLab's own permissions apply on top.
- **Safety:** Go's cross-origin protection and `SameSite` on the POST, a
  size limit, a rate limit per user, and one log line per posted note (user,
  project, issue or MR, note id), without its text.

## Steers (review of part 1)

- Log lines are read with a bound: longer than 1 MiB are skipped without
  holding them; up to that they are read whole (prompts).
- Only the current run's lines (`<task>#<activeRunRequest>`) are sent.
- A container that restarts while the pod runs is followed again from the
  last entry's time.
- The pod read is the one named after the task; `status.podName` only says
  one exists (the agent pods may write the status).

## Steers (part 2)

- One GitLab host (`ui.gitlab.url`), not a list: every AgentProject on this
  cluster is on gitlab.com; a list can follow.
- The GitLab token cookie is bound to the UI user who connected it.
- The thread needs no grant beyond seeing the project (GitLab decides with
  the user's token); posting needs `steer`.

## Affected files

- `internal/ui` (transcript stream, log filter, GitLab OAuth, thread, notes),
  `web/src` (task page: live panel, thread, message box).
- `internal/runner` (agent server: `agent prompt` line), `api/v1alpha1`
  (`agents.logPrompts`).
- Chart: UI Roles for `pods`/`pods/log` in `agent.namespaces`,
  `ui.gitlab.apps`, rule fields `transcript` and `steer`.
- Docs: `docs/ui.md` (roadmap items become features), `docs/design.md`
  (security), `docs/commands.md` (steering from the UI).

## Risks / trade-offs

- **Write path in the UI:** the UI can now post as users. Mitigated by the
  per-user GitLab token (no shared credential), the `steer` grant, CSRF
  protection and the audit line. A stolen session cookie plus GitLab cookie
  could post as that user until they expire.
- **`api` scope:** a user's token could do more than post notes. It stays
  server-side in an encrypted cookie and is only used for reading notes and
  posting them.
- **Pod logs** of the agent container quote code; the transcript grant keeps
  them to the roles that should see them.
- **Prompt logging** makes the agent container's log larger and more
  sensitive; opt-in per project.
- **No history:** the live panel is empty once the pod has stopped (a minute
  after the run).

## Validation

- Log filter: unit tests with real claude and cursor lines; unknown lines
  and fields never pass.
- Transcript stream: test with a fake pod-log source; access checks
  (no task access → 404, no transcript grant → 403).
- GitLab OAuth: state/PKCE, token cookie round trip, refresh, disconnect;
  fake GitLab.
- Notes: posted with the user's token to the right issue or MR; 403 without
  `steer`; rate limit; audit line without the text.
- Prompt logging: on/off, cut at 64 KiB, no prompt line without the setting.
- On the cluster: a GitLab application for gitlab.com created by hand (its
  client secret sealed), then a run on Orbit watched and steered from the UI.

## Delivery

Three PRs, each released and tried on the cluster before the next:

1. **Live transcript and prompts:** log filter, SSE endpoint, RBAC, rule
   field `transcript`, `agents.logPrompts`, live panel, docs.
2. **GitLab connection and thread:** OAuth per host, token cookie, thread in
   the task page, chart values, docs.
3. **Steering:** notes endpoint, message box and command buttons, rule field
   `steer`, audit line, docs.

Later: injecting messages into a running claude step; keeping transcripts
after the pod is gone.
