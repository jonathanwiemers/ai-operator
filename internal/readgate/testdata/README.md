# Recorded hook inputs

`pretooluse-main.json` (a main-thread `Read`) and `pretooluse-subagent.json`
(the same `Read` from the `scout` subagent) are **reconstructed**, not
recorded: no claude credentials were available when they were made. They
follow the `PreToolUse` hook input schema bundled in claude 2.1.318 (the
version `Dockerfile.agent` installed on 2026-11-05): the common fields
`session_id`, `transcript_path`, `cwd`, `prompt_id`, `permission_mode`,
`agent_id`, `agent_type`, `effort`, plus `hook_event_name`, `tool_name`,
`tool_input` and `tool_use_id`. `agent_id` is present only on a subagent's
call. See the Finding (R2) in `docs/specs/read-gate-and-writer.spec.md`.

The tests replace `cwd` and `tool_input.file_path` with a temporary file.
Replace both files with real recordings when they are available.
