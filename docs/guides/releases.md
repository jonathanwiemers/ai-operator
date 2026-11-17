# Releases

A merge alone releases nothing. A release is a decision with a reason: the
planner puts release points into the plan, the pilot or an owner adds or
drops one, and the milestone ends with a final release. The operator carries
each decision out: it creates the tag and the GitLab release, and your
project's pipeline for the tag builds what it builds. This page shows where
releases come from, how you see and steer them, and what the operator checks
before it tags.

## A release on the tracking issue

Say the planner decided that a release follows #2 of the milestone
"Checkout v2". Once #2 is merged and the default branch is green, the
[tracking issue](../concepts/glossary.md#tracking-issue) gets:

```text
🚀 Release started (planner): users see the new API. Contains #1, #2. Release 2026.11.3: <link>
📦 Released 2026.11.3: <link to the GitLab release>
```

The GitLab release carries release notes:

```markdown
## Why

The planner released after #2. Reason given by the planner: users see the new API.

## Contains

- #1 API for checkout (!11)
- #2 Checkout page (!12)

Milestone: [Checkout v2](https://gitlab.example/group/app/-/milestones/3)
```

The list holds the issues and merge requests that no earlier release of the
milestone listed. The reasons and titles come from agents, so the operator
shortens each to one line of 200 characters and escapes them: a mention, a
link or HTML in them shows as plain text. An owner's release says
"@alice released: <why>", the final release "The operator released after #2:
the end of milestone Checkout v2.".

## See releases in the Desk

The milestone's *Issues* tab shows each release between the issues, at the
point it was decided: *Release after #N* while it waits, *Releasing…* with
its pipeline while it runs, and then the version with a link to the GitLab
release. Every issue that shipped shows its version. The *Pilot* tab lists
the release events: decided (who, why, after which issue), started and
ended. A done milestone keeps them, also after its tasks are gone.

## Release points in the plan

The [planner](../concepts/glossary.md#planner) decides per issue whether a
release follows it, and writes a `**Release:**` line into the issue
description (see [Write a milestone](write-a-milestone.md)). That is a
[release point](../concepts/glossary.md#release-point).

- When the issue merges, its release waits for a green default branch, then
  runs.
- A re-plan that no longer releases after an issue drops its release point,
  as long as the issue is not merged. Once it is merged, only the pilot or an
  owner can drop it.
- A release point after a skipped issue stays. If the issue is merged after
  all, its release follows; otherwise the final release covers it.

## Cut or drop a release yourself

Owners comment on the tracking issue (see
[Commands](../reference/commands.md#on-the-tracking-issue)):

```text
/agent release the new checkout is ready for users
/agent no-release #12 nothing users notice yet
```

- `/agent release [reason]` releases the newest commit of the default branch
  that has a pipeline, once that pipeline is green.
- `/agent no-release #N [reason]` drops the releases after #N that did not
  start yet. A release that runs already, and the final release, stay.

The reason is optional, one line, at most 200 characters.

You can also release by hand, in GitLab (*Deploy > Releases > New release*)
or with `glab release create <version> --ref main --notes "<why>"`. The
operator sees your tag like its own: a decision whose commit your tag
contains counts as released, and it creates no second tag.

## The pilot's releases

In an answer or an assessment, the [pilot](pilot.md) may add a release
after an issue of the milestone or drop one, with a one-line reason. It
needs no answer from you. The operator checks each decision: the issue must
be in the milestone's queue, the reason must not be empty, and the final
release is never touched. A refused decision gets a comment on the tracking
issue ("🛑 Refused the pilot's release after #42: #42 is not an issue of this
milestone"), and one with nothing to do an "ℹ️" comment.

## A companion that waits

A [companion repository](../concepts/glossary.md#companion-repository)'s
merge request often lands only once the home change is live. On an
autopilot milestone such a companion asks for a release of its home merge
by itself, unless a release that covers it is already on its way. See
[Cross-repo changes](cross-repo-changes.md#how-a-companion-lands).

## When a release runs

A decision waits until the issue before it is merged. Then, on every poll,
the operator goes through these checks: first for a release that runs, then
for the others in the order they were decided:

1. **Releases switched off.** With
   [`workflow.release.mode: Off`](../reference/agentproject.md#specworkflowrelease)
   nothing is tagged: the decision ends as "not applicable".
2. **Already released.** A tag that contains the commit ends the decision
   without a new tag. If the tag is a release the operator ran for this
   milestone, the decision counts as released with that version. Any other
   tag, for example one you made by hand, makes it "already released".
   While the operator's own release of that tag still runs, the decision
   waits for it.
3. **One at a time.** While one release of the milestone runs, the others
   wait.
4. **Minimum interval.** Two releases start at least `minInterval` apart,
   failed attempts included. The final release does not wait for it.
5. **Green head.** The operator tags the head of the default branch, not
   the merge itself. The newest pipeline there must have passed, and the
   head must contain the decision's commit. Later merges are often in the
   release too, since the queue does not wait for it. A default branch
   without any pipeline is released at the decision's commit.

Then it creates the tag and the GitLab release, and follows the tag's
pipeline. Once that pipeline passed, or after 10 minutes without one, the
release is done. If the pipeline fails, the operator retries it once, never
with a new tag. After the second failure the tracking issue says "❌ The
release failed twice: <reason>. The queue goes on."

The operator needs no release job in your CI: the tag and the GitLab
release are its own. Whatever your CI runs for a tag (images, charts,
packages, a deploy) is what the release builds. A project without tag
pipelines is released by the tag alone. How ai-operator itself is built
from its tags is in [Releasing](../contributing/releasing.md).

## The final release

When every issue of the queue is through, autopilot is not done before a
release contains the milestone's last merge. The tracking issue says
"every issue is through; done once a release contains the last merge". The
order is: final release, then the [companions](cross-repo-changes.md),
then done, then the [retrospective](pilot.md#the-retrospective).

- A milestone that merged nothing has no final release.
- If nothing release-relevant changed since the newest release, there is no
  release: "📦 No release needed: nothing release-relevant since 2026.11.2".
  Docs only (`docs/` and Markdown files) is not release-relevant.
- If an earlier release of the milestone already contains the last merge,
  the tracking issue says "📦 Released 2026.11.3". If a tag from outside the
  milestone contains it, such as one you made by hand, it says "📦 Already
  released in 2026.11.3 (…)".
- If another issue merges after the final release was decided, the final
  release moves to that newer merge, unless it already runs.
- Once it ends, releases still waiting for an issue that never merged are
  dropped.

A final release that failed twice pauses the milestone as
[`release`](pauses.md#release). Release by hand and autopilot is done by
itself, `/agent resume` runs the final release once more, and
`/agent endMilestone` ends the milestone without it.

## Outside milestones

Merges outside milestones, such as Renovate updates or your own merge
requests, are released too. While an autopilot milestone of the project
runs or is paused, its releases cover them, and nothing else is added.
Otherwise:

- **The sweep.** If your project tags its default branch (one of its 20
  newest tags is on a commit from the last 30 days), the operator compares
  the newest release with the default branch. When release-relevant
  commits are unreleased for longer than
  [`maxUnreleasedAge`](../reference/agentproject.md#specworkflowrelease)
  and the [default branch's health](while-the-agent-works.md#main-health)
  is green or flaky, it releases the head: "3 commits since 2026.11.4 are
  unreleased, the oldest since 2026-11-09 06:00 UTC (more than 24h)". So a
  Renovate merge is released within a day.
- **A waiting companion** of a merge outside a milestone asks for a release
  of its home merge, as on a milestone.

There is no tracking issue outside milestones, so these releases are not
commented. Their release notes say why, without a list of issues.

## The version format

Versions are calendar versions `YEAR.MONTH.N`: `2026.11.1`, `2026.11.2`,
then `2026.12.1` in the next month. The month has no leading zero
(`2026.1.1`), so each version is valid SemVer. `N` is one more than the
highest `N` of this month among the project's 100 newest tags, starting at
1. If that tag exists already, because someone tagged meanwhile or this
month's highest tag is older than the newest 100, the decision waits and the
operator tries again at the next poll.

## Switch releases off

Set `spec.workflow.release.mode: Off` in the
[`AgentProject`](../reference/agentproject.md#specworkflowrelease). The
operator then tags nothing, and a milestone's final release ends with "📦 No
release: release.mode is Off". The defaults of `minInterval` and
`maxUnreleasedAge` are in the same table.

## Next

- [Run a milestone on autopilot](autopilot.md): the queue the releases
  follow.
- [Cross-repo changes](cross-repo-changes.md): companions that wait for a
  release.
- [Commands](../reference/commands.md#on-the-tracking-issue): `/agent
  release` and `/agent no-release`.
