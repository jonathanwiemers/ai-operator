# Cross-repo changes: companion repositories

Status: approved by the maintainer (2026-11-05) as the milestone spec of
"Cross-repo changes: companion repositories", with the decisions below
locked.

## Goal

An issue of an AgentProject can change one **companion repository** (first
case: `jo.wiemers/infra/cluster`) in the same run and the same agent
session as its home change. The runner opens a linked MR in the companion
project. The operator lands that MR by rule once the home change is merged
and live. Autopilot merges it when the milestone allows it for that
repository, otherwise the owners are asked to merge. Every layer is gated:
the cluster allow-list, AgentProject targets with path allow-lists and an
auto-merge ceiling, and a milestone line with the full project path.

## Background

* One AgentProject is one GitLab project: one mirror
  (`/runner/mirror.git`), one checkout (`/workspace/repo`), and push, MR, CI
  loop and merge gate all work on `spec.gitlab.project`.
* Typical cross-repo work: an ai-operator feature plus the infra config
  that turns it on (`clusters/netcup01/applications/ai-operator/…`), or
  Bloop plus `clusters/netcup01/applications/bloop/…`. The infra half must
  land after the release that knows the new fields is running. The CRDs are
  strict, so an earlier merge fails at Flux apply (infra !288 waited for
  this by hand). Infra CI is only `kustomize build`, so it does not catch
  the wrong order.
* Infra has no AgentProject and should not get one: Flux applies its `main`
  to production. Two AgentProjects linked `is_blocked_by` keep working for
  big two-sided work, and autopilot already waits for blocking links in
  other projects.
* All AgentProjects share the bot `operator-ai`'s token. The bot is
  **Maintainer on the `jo.wiemers` group** and stays so. That means the
  token could merge into `infra/cluster` `main` and read
  `jo.wiemers/personal/kubernetes` (its tfvars hold live secrets). The gates
  therefore live in the operator.
* Infra holds SealedSecrets (encrypted) and the public sealing cert, with no
  plaintext secrets, so it is acceptable as a checkout the agent can read.
* Full design, options and trade-offs: `docs/specs/cross-repo-changes.spec.md`.

## Decisions (locked)

1. **Companion checkout in the home task (option A).** The issue's pod gets
   a second mirror and checkout. The same implementer session edits both
   trees, and the reviewers see both diffs. No AgentProject on the companion
   project. Linked issues across AgentProjects stay as they are today. No
   cross-project coordinator.
2. **One companion per issue** in this milestone.
3. **Three gating layers, all required.**
   * Cluster: Helm value `crossRepo.allowedProjects` →
     `--cross-repo-allowed-projects`. Empty by default, which turns the
     feature off.
   * AgentProject: `spec.crossRepo.targets[]` with `project` (full path),
     `paths` (gitignore-style allow-list), `autoMerge` (default `false`) and
     an optional `tokenSecretRef` (default `spec.gitlab.tokenSecretRef`).
     Validation: the project is in the cluster allow-list and is not the
     home project. `paths` is non-empty and matches neither
     `.gitlab-ci.yml`, `.gitlab/**` nor the whole tree. An invalid target is
     dropped with condition `CrossRepoReady=False` naming it.
   * Milestone: one header line per companion, full path or GitLab URL:
     `/agent repo jo.wiemers/infra/cluster [merge]`. The line is part of the
     description hash. An unknown or invalid target is named once on the
     tracking issue and ignored.
4. **Per-issue opt-in.** The planner sees the milestone's targets (project,
   paths, a short tree of the allowed paths) and sets `repo: <project>` on
   plan items that need one. `applyPlan` writes label
   `agent-repo::<project>`. Outside such a milestone, an owner's
   `/agent repo <project>` or `/agent repo none` (a routing command like
   `/agent use`, through `commandGate`) sets or clears it. Those companions
   are never auto-merged. A hand-set label counts only under a milestone line
   for that project, or when an owner set it (label events). Otherwise the
   run ignores it with a notice. The operator writes
   `AgentTask.spec.companion` (`project`, `paths`, `branch`, `autoMerge`),
   and the runner only reads it.
5. **Runner: two trees, one trust model.**
   * Mirror: `/runner/mirrors/<project-slug>.git`.
   * Checkout: `/workspace/repos/<project-slug>`, moved by git bundle like
     the home tree.
   * Branch: `agent/<home-slug>-<iid>-<slug>`.
   * The agent server accepts only `/workspace/repo` and
     `/workspace/repos/<valid slug>`.
   * The user prompt gets a "Companion repository" section. System prompts
     are unchanged.
   * claude gets `--add-dir`. A cursor implementer routes to the profile's
     claude engine (its fallback, else `agents.implement`) with a notice.
   * The companion change is checked in the runner's mirror: names within
     `paths`, no symlinks or submodules added, a size cap. A violation fails
     the run before any push of either tree.
6. **Companion MR.**
   * Opened as a draft at the first run that has a companion change.
   * Title `<home title> (companion of <home>#<iid>)`, label
     `agent::companion`, describer text with a "Lands after" line.
   * The home MR description gets a `Companion: <project>!<iid>` line inside
     the description markers.
   * Recorded in `status.run.companion` (`project`, `mrIID`, `pushedSHA`,
     `reviewedSHA`).
   * A run that removes the companion change closes the MR and deletes its
     branch.
7. **CI and the home merge gate.** The CI loop follows the companion
   pipeline too. Fix rounds are shared, and the fix run is told which tree
   failed. The home merge gate also needs the companion green on its
   `pushedSHA`, reviewed at that head, open and mergeable (one rebase, else
   pause kind `companion`). `merge #N <sha>` skips the review checks of both
   trees. CI still applies to both.
8. **Landing by rule** (no ordering knob). After the home merge, the
   AgentProject's `status.companions[]` tracks the companion: home issue and
   milestone, project, MR, merge SHA, state, `readyAt`, `notifiedAt`.
   Waiting entries are checked once per poll. A companion is ready when:
   * the home MR is merged, and
   * the home change is live:
     * the home project is `--self-project`: the running operator's version
       is at least the first tag that contains the merge commit;
     * else, the home project tags its default branch (a tag on a merge
       commit from the last 30 days): a tag contains the merge commit
       (`commits/:sha/refs?type=tag`);
     * else: merged is enough;
     * a home merge that touches only `docs/**` and `*.md` is live at merge;
   * the companion is rebased if needed (once, a conflict pauses with
     `companion`), green on that head, and `detailed_merge_status` is
     mergeable.
9. **Who merges.**
   * Autopilot merges when the milestone line has `merge`, the milestone has
     `/agent autopilot` and the target has `autoMerge: true`. It marks the
     MR ready (starting a pipeline if the head has none), merges with the
     checked `sha`, then follows the companion's default-branch pipeline of
     the merge commit. A red pipeline pauses the milestone (`companion`) and
     mentions the owners.
   * Otherwise the operator marks the MR ready, comments once with the
     owners mentioned and the release name, raises Inbox item
     `companionReady`, and sends one reminder after 7 days.
   * A merge or close by hand is recorded. Entries leave the status 7 days
     after they end.
10. **Queue.** The next issue does not wait for a companion to land. The
    exception: an issue with a companion on project P does not start while
    another companion on P from the same milestone is open. The milestone is
    done only when all its companions have landed. `closeWaitsFor` names
    open companions, and the retrospective lists them.
11. **Merge/push guard.** The bot stays group Maintainer. The GitLab
    wrapper and the mirror refuse a merge outside the home project or a
    target with `autoMerge`. They also refuse a push to anything but `agent/*`
    branches of the home project or a valid target, and any push to a
    default branch. Every refusal is logged and unit-tested.
    `jo.wiemers/personal/kubernetes` is never a valid target.
12. **Desk and Spend.**
    * Companion chip on issue and MR cards (`draft`, `waiting for release`,
      `ready`, `merging`, `merged`) linking to the MR.
    * Companions in the milestone context panel.
    * Inbox kind `companionReady`, and the `companion` pause card with its
      way out.
    * Companions in the pilot state (no new pilot tools).
    * Spend: companion calls already carry the home issue. No schema change,
      only a companion marker on Spend issue rows.

## Requirements

API and config
- R1 Decision 3: `CrossRepoSpec` on AgentProject, the cluster flag and
  chart value, validation and the `CrossRepoReady` condition, then
  `make generate manifests`. Tests: allow-list, home project, CI-config and
  whole-tree `paths`, `autoMerge` default false.
- R2 Decisions 3 and 4: the `/agent repo` milestone line (path and URL, with
  or without `merge`, unknown target named once), the plan item `repo` and
  the label from `applyPlan`, the owner command `/agent repo`, the hand-set
  label rule, and `AgentTask.spec.companion`. Tests for each source,
  including the `commandsFrom: owners` gate.

Runner
- R3 Decision 5: the second mirror and checkout, agent-server path rules,
  the prompt section, `--add-dir`, and cursor routing. Tests: path rules,
  rerouting with a notice.
- R4 Decision 5: the path check in the mirror. Tests: allowed, outside,
  symlink, submodule, rename out of the allowed tree, size cap. On a
  violation neither tree is pushed.
- R5 Decision 6: push and companion MR, the links both ways, the combined
  reviewer diff with tree headers and the security reviewer note, and
  closing on removal. Tests: MR only on the first companion change,
  branch naming, close and delete when the change goes away.

Operator
- R6 Decision 7: the companion pipeline in the CI loop, shared fix rounds,
  the home gate checks, the `companion` pause kind with a `PauseExits`
  entry. Tests: companion red, unreviewed or conflicting keeps the home MR
  unmerged.
- R7 Decision 11: the merge/push guard. Tests: merge in a non-target
  project refused, merge in a target without `autoMerge` refused, push to a
  default branch refused.
- R8 Decisions 8 and 9: `status.companions` and the landing state machine
  (new `internal/controller/companions.go`), `--self-project` and the
  operator's build version, tag lookup, the docs-only rule, rebase once,
  auto-merge only with all three switches, the owner path with one comment
  and one reminder, the post-merge pipeline check. A table test over the
  states.
- R9 Decision 10: the queue rule for two companions on one project,
  `closeWaitsFor` with open companions, the retrospective list.

Desk
- R10 Decision 12: companion on the task and MR views (`make ts-types`),
  the chip, the milestone panel list, Inbox kind `companionReady`, the
  `companion` pause card, the pilot state, and the Spend marker.

Docs
- R11 `docs/design.md` (a Cross-repo section, Security: the guard and what
  the agent can read), `configuration.md` (`crossRepo`, chart values),
  `usage.md` (the milestone line, `/agent repo`, landing), `ui.md`. The
  spec's PR list.

End to end
- R12 A fake-GitLab e2e run: autopilot milestone with
  `/agent repo … merge` → two pushes and two linked MRs → both green → home
  merged → tag → self version bumped → companion rebased, ready, merged,
  default-branch pipeline green → milestone done. The same without `merge`
  stops at ready + one owner comment + Inbox item.

## Constraints

- The runner stays the only git that talks to GitLab. The agent container
  gets no GitLab token. Companion trees move by git bundle only.
- No model decides readiness or a merge. Both are operator rules.
- No new GitLab seat and no project access tokens (Free plan).
- System prompts stay unchanged (prompt cache). New text goes into the user
  prompt.
- `make generate manifests` and `make ts-types` after type changes. CI
  drift jobs must stay green.
- Infra changes (enabling the feature) are not part of this milestone: the
  owner makes them after the release.
- Keep each MR reviewable. Split a requirement if its MR grows large.

## Non-goals

- More than one companion per issue.
- A coordinator planning across several AgentProjects.
- Agent runs on a companion after the home issue has closed.
- Checking Flux reconciliation after a companion merge.
- "Running" detection for projects other than the operator's own.
- Changing the bot's GitLab memberships.

## Suggested order

1. Spec copy + R1 `CrossRepoSpec`, cluster flag, validation
2. R2 milestone line, plan item, label, `/agent repo`, `spec.companion`
3. R7 merge/push guard
4. R3 second mirror and checkout, prompt, engines
5. R4 path check in the mirror
6. R5 companion push, MR, links, reviewer diff
7. R6 CI loop and home gate
8. R8 landing state machine and auto-merge
9. R9 queue rule and `closeWaitsFor`
10. R10 Desk
11. R12 end-to-end test
12. R11 docs pass

## Acceptance

- With an infra target that has `autoMerge: true` and the milestone line
  `/agent repo jo.wiemers/infra/cluster merge`, one issue produces a home
  MR and a linked infra MR in one run. Autopilot merges the home MR. Once
  the operator runs the release that contains it, autopilot merges the
  infra MR, and the milestone closes. No human step.
- Without `merge`, the infra MR is marked ready at the same point, the
  owners get one comment and an Inbox item, and nothing is merged.
- A companion change outside `paths`, or in `.gitlab-ci.yml`, fails the run,
  and neither tree is pushed.
- No code path merges or pushes a default branch in a project that is not
  the home project or a valid target (guard tests).
- A red companion pipeline keeps the home MR unmerged. A red default-branch
  pipeline after a companion merge pauses the milestone with `companion`.
- The Desk shows each companion's state on its issue, and Spend attributes
  its cost to the home issue.

## Merge requests

The merge requests of this milestone, in merge order (from the git log; the
squash commits carry no MR number, so each line names its commit on `main`).

| Req | Title | Commit | Status |
|---|---|---|---|
| R1 | Spec copy + R1: `CrossRepoSpec` on AgentProject, `--cross-repo-allowed-projects`, validation and `CrossRepoReady` | `0fd705b` | merged |
| R2 (1/2) | `/agent repo` milestone line, owner command, `agent-repo` label rule and `AgentTask.spec.companion` | `18b85ba` | merged |
| R2 (2/2) | planner sees the milestone's companion targets and sets `repo` on plan items; `applyPlan` writes `agent-repo` | `6b07431` | merged |
| R7 | merge/push guard in the GitLab wrapper and the mirror | `360d5a1` | merged |
| R3 (1/2) | companion mirror and checkout, agent-server path rules, companion token | `fbfb0bd` | merged |
| R3 (2/2) | Companion repository prompt section, claude `--add-dir` and cursor rerouting | `68b0339` | merged |
| R4 | companion path check in the runner's mirror; a violation pushes neither tree | `f3b6608` | merged |
| R5 (1/2) | companion push and draft MR, links both ways, `status.run.companion`, close on removal | `a452933` | merged |
| R5 (2/2) | combined reviewer diff with tree headers, security reviewer note, companion `reviewedSHA` | `7e595dd` | merged |
| R6 | companion pipeline in the CI loop, shared fix rounds, home merge gate checks, pause kind `companion` | `82f4e39` | merged |
| R8 (1/2) | `status.companions` and the landing readiness rules (live detection, docs-only, rebase once) | `7a6093d` | merged |
| R8 (2/2) | who merges a ready companion: autopilot with three switches, else owners once plus a reminder | `83be201` | merged |
| R9 | queue rule for companions on one project, `closeWaitsFor` with open companions, retrospective list | `b375e2e` | merged |
| R10 (1/2) | companion in the Desk API views, Inbox kind `companionReady`, pilot state, Spend marker | `887b08a` | merged |
| R10 (2/2) | Desk companion chip, milestone panel list, Inbox `companionReady` and the companion pause card | `b950db7` | merged |
| R12 | fake-GitLab end-to-end test of a companion milestone, with and without merge | `6345ae0` | merged |
| R11 | docs pass: Cross-repo in design.md, `spec.crossRepo` in configuration.md, this list | [!238](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/238) | open |
