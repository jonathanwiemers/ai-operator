# desk-live-agent-chat — index

**Status:** `in progress` | **Spec:** `docs/specs/desk-live-agent-chat.spec.md` | **Spec MR:** [!70](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/70)

Resume here on any new agent session. Orchestrator reads only this file plus the latest step artifact.

## Flow (per slice)

```
spec (approved) → per slice: worktree → implement → review → MR → merge when green → release → [deploy]
```

Merges: maintainer authorized the orchestrator to merge each MR once its pipeline is green (2026-11-03).
Release: every code merge to `main` releases a CalVer automatically (`docs/releasing.md`).
Deploy: cluster bump in `jo.wiemers/infra/cluster` only for slices that change runtime behaviour (from slice 5 on); slices 1–4 add code without a caller or schema only.

## Slices

| # | Scope | Branch | MR | Status |
|---|-------|--------|----|--------|
| 0 | Spec (+ steer !72) | `spec/desk-live-agent-chat` | [!70](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/70) | merged |
| 1 | Claude stream-json `Session` | `feat/claude-session` | [!71](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/71) | merged |
| 2 | Cursor ACP session | `feat/cursor-acp-session` | [!74](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/74) | merged |
| 3 | Agent server session RPC | `feat/agent-session-rpc` | [!75](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/75) | merged |
| 4 | Chat table and login | `feat/chat-store` | [!76](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/76) | merged |
| 5 | UI chat endpoint and stream kinds | `feat/ui-chat-endpoint` | [!77](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/77) | merged, released 2026.11.8, deployed (infra!289) |
| 6 | Runner chat loop | `feat/runner-chat-loop` | [!78](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/78) | merged |
| 7 | Operator dispatch | `feat/operator-chat-dispatch` | [!79](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/79) | merged, released 2026.11.10, chat on for ai-operator (infra!290) |
| 8 | Web | `feat/web-desk-chat` | [!80](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/80) | merged |
| 8+ | Enum fix found by the e2e test | `fix/chat-kind-enum` | [!81](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/81) | merged |
| 9a | Commands: shape, path, Implement commands | `feat/chat-commands` | [!82](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/82) | merged |
| 9b | Commands: autopilot verbs, proposals, `ownerSubjects` | `feat/chat-commands-pilot` | [!83](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/83) | merged |
| 9c | Commands: web buttons; chat shows "waiting for capacity" | `feat/web-chat-commands` | [!84](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/84) | review done |
| 10 | `workflow.inputs` | — | — | on hold: decided after the "ask the pilot" spec |

## Step artifacts

| Step | File | Status |
|------|------|--------|
| explore | [01-explore.md](01-explore.md) | done |
| spec summary | [02-spec-summary.md](02-spec-summary.md) | approved |
| implement | [03-implement.md](03-implement.md) | per slice |
| review summary | [04-review-summary.md](04-review-summary.md) | per slice |
| gitlab review | [05-review-gitlab.md](05-review-gitlab.md) | per slice |
| handoff | [06-handoff.md](06-handoff.md) | pending |
| merge | [07-merge.md](07-merge.md) | per slice |
| release | [08-release.md](08-release.md) | per slice |
| deploy | [09-deploy.md](09-deploy.md) | pending |

## Notes

- Delegation: cursor-agent for implementation and reviews (no quota marker on 2026-11-03); orchestrator verifies, commits signed.
- Steer 2026-11-03: maintainer requires chat to survive operator, UI, pod restarts and DB outages; spec section 3a (at-least-once per hop, dedupe by message ID).
- CI: the `drift` job fails now and then on a TLS timeout fetching controller-gen from proxy.golang.org (!73, !75, !76); a retry fixes it. Follow-up: `GOPROXY=https://proxy.golang.org|direct` or a `retry:` on the job.
- Decision 2026-11-03 (user): chat turns keep waiting for a run slot (`maxConcurrentRuns`), no separate chat budget; the UI must show the wait instead of "answering" (9c).
- Decision 2026-11-03 (user): immediate answers come from a new read-only "ask the pilot" path (own spec), not from task chat; slice 10 on hold until then.
- Steer 2026-11-03 (slice 9): spec §4a — commands go onto the task and the project reconciler applies them; answers in `spec.source.chatAnswers`; slice 9 split into 9a/9b/9c.
- Steer 2026-11-03 (slice 6): spec §3b — chat shares the runs' session, interrupts wait during runs, late interrupts are answered as messages (§3a bullet changed).
