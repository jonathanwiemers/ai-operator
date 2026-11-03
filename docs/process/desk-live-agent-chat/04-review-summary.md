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
