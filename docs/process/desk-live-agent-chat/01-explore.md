# Explore — desk-live-agent-chat

**Date:** 2026-11-03 | Done in the spec session (orchestrator), see the spec's Problem and Approach.

- Engines: `internal/runner/engine.go` (`Engine`, `Invocation`, `Result`, `ClaudeCLI`, `CursorCLI`, `runCLI`); stream parsing and logging in `transcript.go`.
- Runner ⇄ agent server: HTTP over a unix socket on `/exchange` (`internal/runner/agent.go`, `AgentServer`, `AgentClient`).
- Pilot messages as the model for chat hand-over: `spec.source.pilotMessages` (`api/v1alpha1/agenttask_types.go`), `internal/controller/pilot.go`.
- Desk history: `internal/research/store` (migrations `0001`, `0002_history.sql`), `internal/history` (recorder, differ, notes wrapper).
- Live transcript and steering: `docs/specs/ui-live-transcript-and-steering.spec.md` (injection was deferred there).
- Lifecycle: `lifecycle.idleTimeout` (1m default) in `api/v1alpha1/agentproject_types.go`.
- External: no A2A in either CLI (2026-11); cursor-agent `acp` (ACP, JSON-RPC over stdio); claude `--input-format stream-json`.
