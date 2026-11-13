# Read gate and writer subagent: delegation that is enforced, not only suggested

Status: approved by the maintainer (2026-11-05) as the milestone spec of
"ai-operator: read gate and writer subagent", with the decisions below
locked.

## Goal

1. A claude role with subagents cannot read a big file whole into its own
   context. A `PreToolUse` hook (the **read gate**) blocks the read and tells
   the agent to ask a subagent, or to read only the section it edits with
   `offset`/`limit`.
2. Boilerplate (tests, fixtures, configs, stubs that follow an existing file)
   can go to a cheaper **writer** subagent. Its output never enters the main
   model's context; the main model reviews the diff and fixes the part that
   needs judgment.
3. The Spend tab shows how often the gate blocked and what the writer cost, so
   the owner can tune the threshold and the subagent models.

## Background

The idea comes from Spotify's shunt plugin for Claude Code
(github.com/spotify/portal-ai-plugins, `plugins/shunt`; engineering blog
2026-09, "cut my Claude Code token usage by 90%"). Shunt has three layers:
hooks that block big reads (the hard gate), scripts that send reads and
boilerplate generation to a cheap model, and skills that say when to use them
(soft guidance). The hooks enforce the routing when the model ignores the
skills. The reported saving is 82–94 % of the tokens for bulk reads (one
author, one Java monorepo, not verified independently).

What ai-operator has today (milestone 4, all merged):

- `subagents:` per role and profile (claude only), passed as `--agents` JSON
  by `withSubagents` (`internal/runner/runner.go`). The role's system prompt
  gets one line (`subagentsNote`): delegate searches, lookups and log reading,
  read only the files you edit. That is the soft layer only. Nothing stops
  the role from reading a 3 000-line file whole.
- `CallUsage` parses `modelUsage`, `subagent_stats` and per-turn context size;
  `agent_calls` stores them. The Spend tab has *With vs without subagents* per
  role and a context-growth chart with subagent markers.
- my-cluster: a read-only haiku `scout` on Orbit's `implement` and profile
  `strong` since 2026-11-05 (example/infra !295). The `ai-operator` project
  has none.
- First run with the scout (Orbit #421 implement, opus, agent image
  2026.11.105, 2026-11-05 18:01): the system prompt had the subagents line,
  but opus made 0 `Agent` calls in 54 tool calls and dumped whole files with
  `cat a.ts; cat b.ts; …` several times. The prompt line alone is ignored.
- The claude CLI runs in the agent container (`Dockerfile.agent`), whose
  entrypoint is `ai-runner` (`cmd/runner`, subcommands `agent-server`,
  `research`, `init-workspace`, `log`). `ClaudeCLI.Args`
  (`internal/runner/engine.go`) builds the command line and passes no
  `--settings` today.
- Orbit's resumed opus sessions grow in cost per step ($17 → $37 over five
  steps of one run, m4 background). Reads of whole files are a large part of
  that context.

## Decisions (locked)

1. **The hook is a subcommand of `ai-runner`**, `ai-runner read-gate`, not a
   shell script: the binary is already on the agent image, the logic is
   tested in Go, and no `jq` or `wc` is needed. It reads the hook JSON on
   stdin and answers in Claude Code's current hook format
   (`hookSpecificOutput.permissionDecision: "deny"` with
   `permissionDecisionReason`), not shunt's legacy `decision: block`.
2. **Read rule** (shunt's, plus exemptions for non-text reads). Allow when
   `offset` or `limit` is set, when `pages` is set, when the path is not a
   regular file, when the extension is an image, PDF or notebook, or when the
   file has at most `minLines` lines. Otherwise deny with: *File is N lines
   (read gate: M). Ask a subagent (names) for what you need from it, or read
   only the section you edit with offset/limit.* Lines are counted by
   streaming the file and stopping at `minLines + 1`, so big files cost no
   more than small ones.
3. **Bash rule.** Deny a command whose first word is `cat`, `head`, `tail`,
   `less` or `more` (after an optional `cd … &&`) on one or more files that
   are over `minLines` lines, unless `head`/`tail` read at most `minLines`
   lines (`-n K` with K ≤ minLines). Pipes, redirects and anything the
   parser does not understand are allowed. `sed -n 'A,Bp'` is allowed (it is
   the targeted read). The gate does not try to be a shell parser; it catches
   the common whole-file dump.
4. **Subagents are exempt.** The scout must read big files; that is its job.
   The hook allows every tool call that comes from a subagent. The first
   slice checks how the image's claude version marks a subagent's tool call
   in the hook input (`agent_id` / `agent_type` or similar), records the
   finding in the spec and tests with a recorded input. If the CLI gives no
   way to tell, the read gate is refused by validation with a clear message
   and the milestone continues with the writer and the measurement only.
5. **Fail open.** Any error in the hook (bad JSON, unreadable file, panic)
   allows the call and logs one line. A gate bug must never fail a run. The
   hook has a 5 s timeout in the settings.
6. **Configuration:** a new optional `readGate: {minLines: <int>}` per role
   and profile (claude only), next to `subagents`. Unset = no gate
   (today's behaviour). `minLines` defaults to 350 when `readGate: {}` is
   set, minimum 100. A role with `readGate` but without `subagents` is
   allowed: the deny message then names claude's built-in subagents
   ("a subagent such as Explore"). Cursor roles, and claude roles that run on
   a cursor fallback, ignore it with a warning once per run and role, like
   `subagents`.
7. **How it is passed:** when a role has `readGate`, the runner adds
   `--settings` with a JSON object holding only the `PreToolUse` hooks for
   `Read` and `Bash`, each with command
   `ai-runner read-gate --min-lines N --subagents scout,writer`. The prompt
   line from `subagentsNote` gains one sentence: *Reads of files over N lines
   are blocked; ask a subagent or read a section with offset/limit.*
8. **Counting:** the runner counts denials of the gate in the stream (a
   `tool_result` with `is_error` whose text starts with the gate's fixed
   prefix) into `CallUsage.ReadGateBlocks`. `agent_calls` gets a
   `read_gate_blocks` column (new migration). The Spend tab's *With vs
   without subagents* table gets a *gate blocks per call* column, and the Task
   page's call table shows the count.
9. **Writer subagent:** no new mechanism. `subagents` already allows `Write`
   and `Edit`. The milestone ships:
   - `subagentsNote` names what each subagent may do. Read-only subagents
     (no `Write`/`Edit`/`Bash`) get the delegate-reads sentence as today.
     Subagents with `Write` or `Edit` get: *Give boilerplate (tests,
     fixtures, configs, stubs that follow an existing file) to <name> with a
     short spec and one reference file. It writes the files and answers with
     their paths only. Review its changes with git diff and fix what needs
     judgment.*
   - the docs and chart README example adds a `writer` on
     `claude-sonnet-5-5` with tools `Read, Grep, Glob, Write, Edit` and a
     prompt in shunt's code-writer style (follow the reference file's
     patterns; never answer with code; list the paths written).
   - the scout example's prompt adopts shunt's bulk-reader contract: answer
     in bullets with exact names, paths and line numbers, no preamble; the
     caller checks line numbers before editing.
10. **my-cluster rollout is outside this milestone** (example/infra MRs after
    the release, by the owner): `readGate: {}` on Orbit's `implement` and
    `strong`, the new scout prompt, a scout on `spec`, and the writer after
    the gate has data.

## Requirements

Hook
- R1 `ai-runner read-gate` subcommand with decisions 1–5. Table tests in the
  style of shunt's evals: small/big file, offset, limit, pages, image/PDF,
  missing path, directory, every Bash form of decision 3 (allowed and
  denied), subagent call (recorded hook input from the image's claude),
  malformed JSON (allow). A benchmark is not part of the default test suite.
- R2 The first slice records in the spec how the image's claude marks a
  subagent's tool call in the `PreToolUse` input and the hook output format
  it accepts, checked with the image's CLI version.

API and runner
- R3 `readGate` on `RoleSpec` (role and profile), validation per decision 6
  (minimum 100, claude only), `make generate manifests`, chart CRD, tygo
  types if a view embeds it.
- R4 `ClaudeCLI.Args` adds `--settings` only when the invocation carries a
  read gate. Runner test: the settings JSON and the prompt sentence are
  present only when configured, and absent on cursor and on a cursor
  fallback (with the warning).

Measurement
- R5 `CallUsage.ReadGateBlocks` from the stream (fixture with a recorded
  denial), `status.run.calls`, `agent_calls.read_gate_blocks` (migration),
  Spend tab column and Task page call table.

Writer
- R6 `subagentsNote` per decision 9 (read-only vs writing subagents). Runner
  test for both kinds and for a mix.
- R7 Docs: `docs/configuration.md` (Subagents, Read gate), chart README
  example with `scout` (new prompt) and `writer`, `docs/ui.md` for the new
  column, `docs/design.md` for the hook.

## Constraints

- Default behaviour does not change: no `readGate`, no `--settings`, the same
  prompt line for read-only subagents.
- The hook must stay fast: it runs before every `Read` and `Bash` call.
  Count lines with a bounded read; no shelling out.
- No new dependencies in the agent image.
- CRD fields are only added, never removed or renamed.
- Slices stay small and reviewable (< 50 production lines where it fits;
  the hook with its parser may be larger, say so in its MR).

## Non-goals

- Sending reads to a model of another vendor (shunt uses Gemini Flash through
  Spotify Portal). Cursor's budget is too small for it today.
- A gate for writes (shunt has none either).
- Gating cursor roles: cursor-agent has no hooks.
- Changing the my-cluster configuration (decision 10).

## Suggested order

1. Spec copy to `docs/specs/read-gate-and-writer.spec.md` + R2 finding + R1
   hook subcommand with tests.
2. R3 API field and validation.
3. R4 runner passes `--settings` and the prompt sentence.
4. R5 counting, migration, Spend and Task page.
5. R6 writer note.
6. R7 docs (may go with the slices they describe).

## Acceptance

- With `readGate: {}` on a role, a claude call's `Read` of a 2 000-line file
  without `offset`/`limit` is denied with the message, the same read with
  `offset`/`limit` goes through, and a subagent's read of the same file goes
  through.
- `cat` of the same file in Bash is denied; `cat big | grep x` and
  `sed -n '10,40p' big` go through.
- Without `readGate` the command line and the system prompt are byte-for-byte
  what they are on main today.
- A run's Spend shows gate blocks per call; the Task page call table shows
  the count per call.
- A broken hook input lets the call through and logs one line.

## Finding (R2)

Checked on 2026-11-05 against claude `2.1.318`, the version
`npm view @anthropic-ai/claude-code version` reported, i.e. what the
unpinned `npm install -g @anthropic-ai/claude-code` in `Dockerfile.agent`
installs today. **Method:** the hook input and output schemas and the
hook result handling in the CLI bundle (`bin/claude.exe` of the package,
which embeds the JS with its zod schemas and their descriptions). A live
run with a `--settings` hook that dumps stdin was tried but had no
credentials (`Not logged in`), so nothing was billed and nothing was
recorded live.

- **Subagent marker: `agent_id`.** Every hook input has the common fields
  `session_id`, `transcript_path`, `cwd`, `prompt_id` (optional),
  `permission_mode` (optional), `agent_id` (optional), `agent_type`
  (optional) and `effort` (optional); `PreToolUse` adds `hook_event_name`,
  `tool_name`, `tool_input`, `tool_use_id` and `mcp_server` (MCP tools only).
  The bundle describes `agent_id` as *"Present only when the hook fires from
  within a subagent (e.g., a tool called by an AgentTool worker). Absent for
  the main thread, even in --agent sessions. Use this field (not
  agent_type) to distinguish subagent calls from main-thread calls."*
  `agent_type` is *"Present when the hook fires from within a subagent
  (alongside agent_id), or on the main thread of a session started with
  --agent (without agent_id)."* So the gate allows every input with a
  non-empty `agent_id` and ignores `agent_type`. The CLI can tell a
  subagent's call apart: `readGate` needs no refusal by validation.
- **Output format.** `{"hookSpecificOutput":{"hookEventName":"PreToolUse",
  "permissionDecision":"deny","permissionDecisionReason":"…"}}` on stdout
  with exit code 0 is accepted (the schema also has `allow`, `ask`, `defer`,
  `updatedInput` and `additionalContext`). A `hookSpecificOutput` with
  another `hookEventName` counts as malformed. The gate never answers
  `allow`: that would skip claude's own permission checks (D184-P1), so an
  allowed call gets no stdout at all.
- **Denial in the stream (from the bundle, not observed live).** A deny
  becomes `behavior: "deny"` with the message
  `PreToolUse:<tool> hook error: <permissionDecisionReason>`, and the tool
  call is answered with a `tool_result` with `is_error: true` and that
  message as its text. Claude therefore **does** wrap the reason (prefix
  `PreToolUse:Read hook error: `); the UI strips a `[command]: ` part for
  display only. The counting slice should match `readgate.Marker`
  (`read gate: `) anywhere in the text of an `is_error` `tool_result`, not at
  its start, and accept a string or an array of text blocks as `content`.
  Reconstructed line:

  ```json
  {"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01HcV8yQ2mXkR4sT7uW9zA3b","is_error":true,"content":"PreToolUse:Read hook error: File /workspace/repo/src/big.ts is over 350 lines (84.2 KiB; read gate: 350). Ask a subagent (scout) for what you need from it, or read only the section you edit with offset/limit."}]},"parent_tool_use_id":null}
  ```

- **Fixtures.** `internal/readgate/testdata/pretooluse-main.json` and
  `pretooluse-subagent.json` are reconstructed from the 2.1.318 schema
  (marked as such in the `README.md` there); the tests point them at a
  temporary 2 000-line file. Replace them with live recordings when a run
  with credentials can make them.
- **Re-check** when the image's claude is updated: the CLI is unpinned, and
  a rename of `agent_id` would make the gate block the scout's reads.
