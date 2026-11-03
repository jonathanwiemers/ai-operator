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
| 0 | Spec | `spec/desk-live-agent-chat` | [!70](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/70) | merging |
| 1 | Claude stream-json `Session` | `feat/claude-session` | — | pending |
| 2 | Cursor ACP session | — | — | pending |
| 3 | Agent server session RPC | — | — | pending |
| 4 | Chat table and login | — | — | pending |
| 5 | UI chat endpoint and stream kinds | — | — | pending |
| 6 | Runner chat loop | — | — | pending |
| 7 | Operator dispatch | — | — | pending |
| 8 | Web | — | — | pending |
| 9 | Commands and owners | — | — | pending |
| 10 | `workflow.inputs` | — | — | pending |

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
