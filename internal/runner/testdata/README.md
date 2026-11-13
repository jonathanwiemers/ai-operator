# Engine fixtures

Lines of the agent CLIs' output that the runner's parsing tests replay.
Prompts and content are placeholders. Each fixture is either **recorded**
(written by the CLI) or **reconstructed** (written by hand from the CLI
bundle's schema). The R5 fixtures say so again in a first
`{"type":"fixture","source":…}` line.

| Fixture | Source | CLI | Shows |
|---|---|---|---|
| `claude-result-usage.json` | origin not recorded (R1, 2026-11-04) | claude | a result line with `usage` and two models in `modelUsage` |
| `claude-stream-subagent.jsonl` | reconstructed from the bundle's stream-json schema (no billed run) | claude 2.1.314 | an Explore subagent on haiku under an opus main thread |
| `claude-stream-read-gate.jsonl` | reconstructed from the bundle | claude 2.1.318 | a Read denied by the read gate |
| `cursor-acp-session.jsonl` | recorded, sanitised | cursor-agent | an ACP session; `session_acp_test.go`'s fake agent is shaped on it |
| `cursor-acp-usage-update.jsonl` | **reconstructed** from the ACP v1 schema, 2026-11-10 | cursor-agent 2026.11.05-86047df (sends neither) | three ACP turns: `usage_update` with `used` and a cumulative USD `cost`, a `PromptResponse.usage`, a turn without a cost and one in EUR; the fake agent's `fixture` mode replays it |
| `claude-stream-subagent-main-model.jsonl` | **reconstructed**, 2026-11-06 | claude 2.1.318 | a general-purpose subagent on the main model: `usage` is the main thread, `modelUsage` both |
| `claude-chat-two-turns.jsonl` | **reconstructed**, 2026-11-06 | claude 2.1.318 | two turns in one `--input-format stream-json` process: `usage` per turn, `modelUsage` and `total_cost_usd` running totals, `model_usage_since_last_result` per turn |
| `claude-resume-carryover.jsonl` | **recorded**, unbilled, 2026-11-06 | claude 2.1.318 | `-p --resume`: the result starts at the totals the transcript saved |
| `claude-chat-resume-two-turns.jsonl` | **recorded**, unbilled, 2026-11-06 | claude 2.1.318 | a resumed `--input-format stream-json` session with two turns: both results carry the saved totals |

## How the R5 fixtures were made

No billed run was possible: the CLI in the agent image (`Dockerfile.agent`,
claude 2.1.318) answers "Not logged in". Hence:

- **Reconstructed** (`claude-stream-subagent-main-model.jsonl`,
  `claude-chat-two-turns.jsonl`): the token numbers are invented. The
  relations between them follow the result schema in the 2.1.318 bundle:
  `usage` is "MAIN AGENT LOOP ONLY — excludes Task subagent, sidechain, and
  auxiliary model calls, and is per-turn in streaming-input sessions";
  `modelUsage` covers "main loop, Task subagents, sidechains, and internal
  calls such as compaction", "cumulative across turns in streaming-input
  sessions"; `total_cost_usd` shares that lifecycle.
- **Recorded, unbilled** (`claude-resume-carryover.jsonl`,
  `claude-chat-resume-two-turns.jsonl`): a logged-out `claude -p` run wrote
  a transcript with its `cost-state` entry. The totals in that entry were
  set by hand (line 2 of each fixture), then the real CLI resumed the
  session: once with one prompt, once with `--input-format stream-json` and
  two user turns. The CLI made no model call (`authentication_failed`), so
  everything after line 2 is its own output. The only edits are shortened
  `init` lines and placeholder text.
