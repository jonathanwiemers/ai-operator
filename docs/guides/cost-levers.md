# Cost levers

This page shows how you find out which settings make a line of code
cheaper. You try them side by side in an
[experiment](../concepts/glossary.md#experiment): each issue runs on one
[arm](../concepts/glossary.md#arm), and the Desk's Metrics page compares the
arms on real issues. You read the numbers and decide; nothing switches on
its own.

## Why cost grows with session length

Most of an [implementer](../concepts/glossary.md#implementer)'s tokens are
cache reads: on every turn the agent reads its whole context again. A long
session pays for its early turns over and over, so the same step costs more
late in a run than early. In one run a step went from $17 to $37 as its
context grew.

Three levers work against that:

- **Fresh step sessions.** With `stepSession: fresh`, each step after the
  first starts a new session from the issue, the step summaries and the
  agent's notes. A step costs about as much as the first one, but the agent
  forgets what is not in the notes or the code.
- **Lower implement effort.** A lower `effort` makes the model think less
  per turn. It costs less per turn, and may need more turns or review
  rounds.
- **A cheaper [profile](../concepts/glossary.md#profile).** Claude Sonnet
  costs half of Claude Opus per token. A `balanced` profile on Sonnet sits
  between a cheap `fast` one and a `strong` one on Opus.

None of the levers makes merge requests smaller. They change what a line
costs, not how many lines an issue has.

## Start an experiment

You add `agents.implement.experiment` to the AgentProject: a name and two
to four arms. Each arm overrides the implementer only: `stepSession`,
`effort` or `profile`. One arm may set nothing: the baseline, which runs
as the project is configured.

This AgentProject adds a `balanced` profile and tries all three levers
against a baseline:

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProject
metadata:
  name: shop
  namespace: agents
spec:
  gitlab:
    url: https://gitlab.com
    project: my-group/shop
    tokenSecretRef: {name: gitlab, key: token}
  milestones:
    specMarker: /agent spec
    autopilotMarker: /agent autopilot
  agents:
    credentialsSecretName: agent-credentials
    maxEffort: high
    implement:
      model: claude-opus-5-5
      bashMaxOutputChars: 30000
      experiment:
        name: cost-levers
        arms:
          - name: baseline
          - name: fresh
            stepSession: fresh
          - name: medium
            effort: medium
          - name: sonnet
            profile: balanced
    profiles:
      - name: fast
        description: Small, well-defined changes that follow existing patterns.
        engine: cursor
        model: auto
      - name: strong
        description: Design decisions, cross-cutting or risky changes, unclear scope.
        engine: claude
        model: claude-opus-5-5
      - name: balanced
        description: >-
          Medium issues: a clear scope with some design work across a few
          files; neither a mechanical change that follows an existing
          pattern (fast) nor a cross-cutting, risky or unclear one (strong).
        engine: claude
        model: claude-sonnet-5-5
```

Keep these rules in mind:

- **List every profile you want.** A project that lists profiles replaces
  the built-in ones. If you add `balanced`, also list `fast` and `strong`,
  or the planner can no longer pick them.
- **Describe the new profile for the planner.** The planner picks a profile
  per issue from the descriptions. The `balanced` description above tells
  it which issues are "medium".
- **Stay within `maxEffort`.** An arm's `effort` may not be above the
  project's `maxEffort`, and an arm's `profile` must be one of the project's
  profiles.

Apply the change with `kubectl apply -f shop.yaml`. Then check that the
operator accepted the experiment:

```sh
kubectl -n agents get agentproject shop \
  -o jsonpath='{.status.conditions[?(@.type=="ExperimentValid")]}'
```

`ExperimentValid` is `True` for a valid experiment. When it is `False`, its
message names the problems, and no issue gets an arm until you fix them.
The fields and rules are in
[Experiment](../reference/agents-and-models.md#experiment).

## How issues get their arm

- **Deterministic.** An issue gets its arm when its first implement run
  starts, from a stable hash of the project, the experiment's name and the
  issue number. Over many issues the arms come out about even.
- **Kept for the issue.** Every later run of the issue keeps the arm: fix,
  CI, conflict and review calls count for it too. The pilot never changes
  an arm.
- **You win over the arm.** An issue with a model label, or a profile or
  effort label that you or an `/agent` command set, gets no arm and records
  none. The planner's own labels do not count. A label you add after the
  arm was assigned wins for that one setting, and the issue keeps its arm.

So when you want an issue on a particular model or effort, label it as
always (see [Per-issue model and effort](../reference/agents-and-models.md#per-issue-model-and-effort)).
It simply stays out of the comparison.

To see an issue's arm, read its [task](../concepts/glossary.md#task)'s
status:

```sh
kubectl -n agents get agenttask <task> -o jsonpath='{.status.experiment}'
```

It shows the experiment's name and the arm. An empty arm means the issue
had a label of yours and runs outside the experiment.

## Read the arm table

In the Desk, open *Metrics* in the rail and pick a range that covers the
experiment, for example 90 days. The section *Cost per 100 written lines* has
one table per project with an experiment, titled *Arms of cost-levers:
my-group/shop*:

| Arm | Merged issues | Written lines | Known cost | Tokens | Per 100 written lines |
|-----|---------------|---------------|------------|--------|-----------------------|
| baseline | 7 | 2,940 | $412.80 | 1.9B | $14.04 · 7 issues |
| fresh | 6 | 2,510 | $251.30 | 1.1B | $10.01 · 6 issues |
| medium | 8 | 3,320 | $398.60 | 1.8B | $12.01 · 8 issues |
| sonnet | 3 | 1,120 | $71.40 | 640M | $6.38 · 3 issues |

- **Per 100 written lines** is the figure to compare: the known cost of the
  arm's merged issues for every 100 lines their implement calls wrote. It
  counts all calls of an issue, reviews and CI rounds included. The Code
  section of the same page has the cost per 100 merged production lines,
  see [Lines of code](budgets-and-spend.md#lines-of-code).
- **Merged issues** is how many issues of the arm were merged. The issue
  count next to the figure says how much it rests on.
- Below the arm tables, *By model*, *By effort* and *By step session*
  compare the same figures across all runs, with or without an experiment.

In this example the `sonnet` row is greyed out: it has only 3 merged issues.

## When the numbers mean something

- **At least 5 merged issues per arm.** An arm with fewer is greyed out
  with "too few to compare". Wait until every arm has 5 or more.
- **Similar issue mixes.** The hash spreads issues evenly, but a few big
  issues in one arm still move its figure. Compare over a range with many
  issues, and look at the written lines per issue too.
- **One change at a time.** Change the profiles, the effort or the
  implementer's model during an experiment, and its arms no longer compare.
  Start a new experiment with a new name instead.
- **Quality counts too.** A cheap arm whose issues need more review rounds
  or human fixes may not be cheaper for you. Read a few of its merge
  requests before you switch.

## Read the Context panel

The *Context* section of the Metrics page shows, per project, what fills an
agent's context:

- **Base context by role:** the median context of a fresh session's first
  turn, before the agent does anything. It holds the system prompt, the
  tool definitions and the `CLAUDE.md` files of the repository. When you
  trim a `CLAUDE.md`, the next fresh sessions show it as a drop.
- **Top tools by result tokens:** the five tools whose results put the most
  into the context, with their result tokens (an estimate: bytes / 4), size
  and number of results. A subagent's
  tools count under the subagent. If `Read` leads, a
  [read gate](../reference/agents-and-models.md#read-gate) may help. If
  `Bash` leads, cut its output (next section).

## Cut noisy Bash output

Test runners and builds can print thousands of lines, and all of them go
into the context. `bashMaxOutputChars` on a role caps how much Bash output
the agent sees per command:

```yaml
agents:
  implement:
    bashMaxOutputChars: 30000
```

It takes 1,000 to 200,000 characters and applies to claude roles only.
Pick a cap that still shows a failing test's message. Watch *Bash* in the
Context panel to see the effect.

## End an experiment

No arm wins on its own. When the arms have enough merged issues and you
have read the table:

1. Make the winner the project's setting. For example, set
   `stepSession: fresh` on `implement`, lower its `effort`, or change the
   profile descriptions so the planner picks `balanced` more often.
2. Remove `experiment` from the AgentProject, or start a new one under a
   new name to try the next lever.

Issues that already have an arm keep recording it until the experiment is
removed or renamed. After that they run without arm overrides and record
no arm.

## Next

- [Budgets and spend](budgets-and-spend.md#the-metrics-page): the rest of
  the Metrics page, lines of code, and budgets.
- [Experiment](../reference/agents-and-models.md#experiment): every field
  and rule of an experiment.
- [Profiles](../reference/agents-and-models.md#profiles): how the planner
  picks a profile.
