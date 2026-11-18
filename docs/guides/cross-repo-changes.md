# Cross-repo changes

Some changes span two repositories: a feature in your application, and the
configuration in an infrastructure repository that turns it on. An issue can
change one [companion repository](../concepts/glossary.md#companion-repository)
besides its own project, in the same run and the same agent session. This
page shows how you allow a companion, how an issue gets one, what the agent
does with it, and how the companion's merge request lands after the home
change.

## An example

Your application lives in `my-group/shop`, and Flux applies
`my-group/infra/cluster` to production. Allow the infrastructure repository
in three places.

The cluster allows the project, in the Helm values:

```yaml
crossRepo:
  allowedProjects:
    - my-group/infra/cluster
```

The [`AgentProject`](../concepts/glossary.md#agentproject) of the shop names
it as a target, with the paths a change may touch:

```yaml
spec:
  crossRepo:
    targets:
      - project: my-group/infra/cluster
        paths:
          - clusters/my-cluster/applications/shop/
        autoMerge: true
```

The milestone gives its issues the companion, with a
[marker line](../concepts/glossary.md#marker-line):

```text
/agent spec
/agent autopilot
/agent owners @alice
/agent repo my-group/infra/cluster merge
```

The planner then decides which issues must change the companion. Each of
them gets the label `agent-repo::my-group/infra/cluster`, its own branch in
the companion, and a draft merge request there that lands once the shop's
change is released.

## The three gates

All three must agree, and no model decides any of them:

1. **The cluster** allows the project with the chart value
   `crossRepo.allowedProjects`, by full path or by glob (`my-group/**` is
   every project below `my-group`). It is empty by default, which turns
   cross-repo changes off. See the
   [chart values](../../charts/ai-operator/README.md#values).
2. **The `AgentProject`** names it in `spec.crossRepo.targets`, with the
   allowed `paths`, `autoMerge` and an optional token. See
   [`spec.crossRepo`](../reference/agentproject.md#speccrossrepo).
3. **The issue** gets it from the milestone line, the planner or an owner,
   as below.

## Which issues get a companion

- **The milestone line** `/agent repo <project> [merge]` (a full path such
  as `my-group/infra/cluster`, or its GitLab URL) lets the milestone's
  issues change that project, one line per project. It is part of the spec,
  so changing it plans the milestone again. A line with a project that is
  no valid target is ignored, and the tracking issue says so once.
- **The planner** sees each companion of the milestone, its allowed paths
  and the files under them. It marks the issues that must change it, and
  they get the label `agent-repo::<project>`. On a re-plan, a label the
  planner set follows the new plan.
- **You**, as an owner, give one issue a companion with
  `/agent repo <project>` on the issue, and take it away with
  `/agent repo none`. You can also set the label by hand. A label you set
  stays on a re-plan.

A label counts under a milestone line for that project, or when an owner or
the bot added it. Any other `agent-repo::` label is ignored, and the issue
is told once. An issue has at most one companion. Changing it starts no run.
See [Labels](../reference/labels.md) and
[Commands](../reference/commands.md).

## What you see

**In the Desk**, a milestone with companion merge requests lists them under
*Companions* on its *Issues* tab, also after the issue's task is gone. A
task's header links its companion merge request. A companion that is ready
for you to merge is an Inbox item, with a browser notification. On the
*Spend* tab, an issue with a companion has a *companion* marker: its calls
edit both trees and count to the home issue. See [The Desk](desk.md).

**In GitLab**, the companion's merge request:

- is opened as a **draft** in the companion project, titled
  `<home title> (companion of <home project>#<iid>)`, with the label
  `agent::companion`;
- says `Lands after: <home project>!<home MR> is merged and live.`;
- stays a draft until it can land: the operator marks it ready then.

The home merge request's description gets a line
`Companion: <project>!<iid>`. If a later run ends without a companion change
(the agent reverted it, or the issue lost its label), the companion's merge
request is closed and its branch deleted, and the run report says so.

## What the agent does

The agent gets a checkout of the companion on the companion branch, next to
its own project, and edits both in one session. The companion token stays in
the trusted runner; the agent never sees it. An implementer on cursor runs
on claude for such a run (see
[Fallback engine](../reference/agents-and-models.md#fallback-engine)).

Before every push, the runner checks every commit of the companion branch.
The run fails, and neither repository is pushed, when a commit:

- changes a file outside the allowed `paths` (also a deletion or a rename);
- touches `.gitlab-ci.yml`, a `.gitlab/` folder or `.gitmodules`;
- adds a symlink or a submodule;
- is a merge commit;
- or when the files it adds or changes add up to more than 1 MiB.

A file added and removed again later still fails it, since a push
publishes every commit. The report on the issue names the path and the
reason.

## Review and CI

**The reviewers see both trees** in one input, the home change first, then
the companion's. When the two diffs together are too big to show inline,
both are listed by their changed files, so the companion is never cut off.
The security reviewer is told that the companion goes to production on
merge, and looks for secrets, wider permissions and changes that break when
they apply before the home release.

**CI follows both.** The companion's pipeline gets the same rules as the
home one: failed jobs are retried once, and a red pipeline gets a CI fix
run. Both trees share the fix rounds; if both are red, home is fixed first.
The issue stays in `agent::working` until both are green. A companion still
red after the rounds needs your input, with a comment naming it. See
[CI feedback](while-the-agent-works.md#ci-feedback).

**Before the home merge**, the [merge gate](autopilot.md#the-merge-gate)
also needs the companion green, reviewed at its head, open and mergeable.
Autopilot asks GitLab to rebase it once when it needs one. A red companion
pauses as [`ciRed`](pauses.md#cired), an unreviewed one as
[`notReviewed`](pauses.md#notreviewed), anything else that blocks it as
[`companion`](pauses.md#companion).

## How a companion lands

After the home merge, the operator checks the companion's merge request on
every poll, by fixed rules:

1. **The home change must be live.**
   - A home merge of docs only (`docs/` and Markdown files) is live at
     merge.
   - For ai-operator's own repository (the chart value `selfProject`), the
     running operator must be a release that contains the merge.
   - For a project that tags its default branch (one of its 20 newest tags
     is on a commit from the last 30 days), a tag must contain the merge.
     On autopilot, the companion asks for that release by itself, see
     [Releases](releases.md#a-companion-that-waits).
   - Any other project: merged is enough.
2. **Rebase** once if GitLab says it needs one or has a conflict.
3. **Green and mergeable:** a green pipeline on its head, and GitLab
   reports it mergeable.

Then it is ready, and someone merges it.

**Autopilot merges it** only when, at that moment, the milestone line still
ends in `merge`, the milestone is still on autopilot, and the target still
has `autoMerge: true`. Remove `merge` or turn `autoMerge` off and autopilot
stops. It marks the merge request ready, waits for its pipeline, merges the
checked head, and then waits for the companion's default branch to be
green. Mark the merge request draft again to take it over yourself.

**Otherwise you merge it.** The operator marks it ready and comments once
on it, mentioning the milestone's [owners](owners-and-pings.md), with the
home merge request, the release that made the home change live, and why
autopilot does not merge it. The Desk shows it in the Inbox. After 7 days
still open, it gets one reminder.

**Blocked.** A failed rebase, a conflict after the rebase, a red pipeline,
an approval it needs, unresolved discussions, or a push by someone else
block the companion. An autopilot milestone then pauses as
[`companion`](pauses.md#companion). The pause ends by itself once nothing
blocks it any more, and `/agent resume` checks it again with one more
rebase and merge. Outside autopilot, the companion's merge request gets one
comment per block, mentioning the owners. A merge or close by hand at any
point is recorded and ends the pause about it.

## Companions and the queue

The next issue does not wait for a companion to land, with one exception:
an issue with a companion on project P does not start while another
companion on P from the same milestone is open. So two changes to the same
companion land one after the other. The tracking issue says what it waits
for (`waits for the companion my-group/infra/cluster!12 of #4 to land`).
This is a wait, not a pause.

The milestone is done only once its companions have landed, after its
[final release](releases.md#the-final-release). Until then the tracking
issue names the companions it waits for.

A companion of a skipped issue is different: it never lands by itself, and
it does not hold the milestone's done. It does hold the other issues with a
companion on the same project, and if nothing else can go on, the milestone
pauses as [`stuck`](pauses.md#stuck). Merge or close its merge request to
let them start, or to clean it up.

The [retrospective](pilot.md#the-retrospective) lists each companion with
its merge request, state, release and who merged it.

## Next

- [Releases](releases.md): the release a companion waits for.
- [When autopilot pauses](pauses.md#companion): the `companion` pause and
  its ways out.
- [`spec.crossRepo`](../reference/agentproject.md#speccrossrepo): the
  target fields and their defaults.
