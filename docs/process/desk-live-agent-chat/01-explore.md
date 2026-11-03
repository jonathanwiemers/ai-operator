# Explore — desk-live-agent-chat

**Date:** 2026-11-03 | Done in the spec session (orchestrator), see the spec's Problem and Approach.

- Engines: `internal/runner/engine.go` (`Engine`, `Invocation`, `Result`, `ClaudeCLI`, `CursorCLI`, `runCLI`); stream parsing and logging in `transcript.go`.
- Runner ⇄ agent server: HTTP over a unix socket on `/exchange` (`internal/runner/agent.go`, `AgentServer`, `AgentClient`).
- Pilot messages as the model for chat hand-over: `spec.source.pilotMessages` (`api/v1alpha1/agenttask_types.go`), `internal/controller/pilot.go`.
- Desk history: `internal/research/store` (migrations `0001`, `0002_history.sql`), `internal/history` (recorder, differ, notes wrapper).
- Live transcript and steering: `docs/specs/ui-live-transcript-and-steering.spec.md` (injection was deferred there).
- Lifecycle: `lifecycle.idleTimeout` (1m default) in `api/v1alpha1/agentproject_types.go`.
- External: no A2A in either CLI (2026-11); cursor-agent `acp` (ACP, JSON-RPC over stdio); claude `--input-format stream-json`.

## Probes (2026-11-03, local CLIs)

**claude 2.1.292, `-p --input-format stream-json --output-format stream-json --verbose`:**
- Several user turns in one process; each turn starts with a `system`/`init` line and ends with its own `result` line. The result line's keys are not ordered (`{"duration_api_ms":…,"type":"result",…}` seen), so parse it, do not prefix-match.
- `{"type":"control_request","request_id":…,"request":{"subtype":"interrupt"}}` on stdin is answered with a `control_response` (`subtype: success`).
- Real model answers could not be recorded: the local Claude login had expired. The interrupt's effect on a running turn is covered with a fake CLI in MR 1 and must be checked on the cluster.

**cursor-agent 2026.10.22, `cursor-agent acp`** (`agent` and `cursor-agent` are the same binary):
- `initialize` (protocolVersion 1) → `agentCapabilities.loadSession: true`, `authMethods: [cursor_login]`; `authenticate {methodId: cursor_login}` → `{}`.
- `session/new {cwd, mcpServers: []}` → `sessionId`, `modes` (agent/plan/ask), `models` (`currentModelId: "default[]"` = Auto, ids like `claude-opus-5-5[context=300k,effort=medium,fast=false]`), `configOptions` (mode select). Model ids differ from `--model` names: map by name.
- `session/prompt` → notifications `session/update` with `sessionUpdate` = `available_commands_update`, `session_info_update`, `agent_thought_chunk` (thinking is visible here, unlike print mode), `agent_message_chunk`; then the response `{stopReason: "end_turn"}`.
- `session/cancel {sessionId}` (notification, no id) during a turn → that prompt returns `{stopReason: "cancelled"}`; the next prompt in the same session works.
- The process does not exit within 15 s after stdin is closed: Close must kill.
