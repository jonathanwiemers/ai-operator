# Rollout on netcup01: checkpoints, conventional commits, step gate and fallbacks

Acceptance checklist for decision 9 of
[the checkpoints spec](../specs/checkpoints-and-conventional-commits.spec.md#decisions-locked)
(issue #259). The rollout is an infra change in `jo.wiemers/infra/cluster`.
This repository can't merge or check that change, and it can't read
production data. **The owner makes the infra change and, one week after it is
live, runs the checks below.** Write the results into the
[results table](#results).

## 1. The infra change

Under `clusters/netcup01/applications/ai-operator/` (the ai-operator and
Bloop AgentProjects, `projects/defaults.yaml` and the per-project files),
set only these fields and keep every other value:

| Field | ai-operator | Bloop |
|---|---|---|
| `workflow.conventionalCommits` | `true` | `true` |
| `agents.implement.maxStepLines` | – | `400` |
| `agents.fallbackAtPercent` | `90` | `90` |
| `agents.implement.fallback` | `{engine: claude, model: claude-sonnet-5-5}` | same |
| `fallback` of the `strong` profile (`agents.profiles[name=strong]`) | `{engine: claude, model: claude-sonnet-5-5}` | same |
| `agents.implement.conflict` | `{model: claude-sonnet-5-5}` | same |
| `budget.perIssueAlertUSD` | `"15"` | `"15"` |

As YAML (merge it into the existing `spec`, don't replace the blocks):

```yaml
spec:
  workflow:
    conventionalCommits: true
  budget:
    perIssueAlertUSD: "15"
  agents:
    fallbackAtPercent: 90
    implement:
      maxStepLines: 400          # Bloop only
      fallback: {engine: claude, model: claude-sonnet-5-5}
      conflict: {model: claude-sonnet-5-5}
    profiles:
      - name: strong
        # ...its existing fields...
        fallback: {engine: claude, model: claude-sonnet-5-5}
```

Before you merge:

- [ ] The cluster runs a release that has all these fields: an operator
      release that contains `agents.implement.conflict` and
      `budget.perIssueAlertUSD` (any release with this checklist does).
      These fields came last, so a release with them has the others too. If the cluster pins an older chart or image, bump it
      in the same infra MR, or merge the bump first: an older CRD prunes or
      rejects the unknown fields.
- [ ] The YAML validates against that release's CRD, for example
      `kubectl apply --dry-run=server -f <file>` against the cluster, or the
      infra repo's own schema check.
- [ ] Write down the **rollout date** (when the operator picked the change
      up, in UTC). Every check below starts from it.

## 2. The checks, one week later

The Desk pages are the *Spend* tab of a project or milestone
(`/projects/<namespace>/<name>?tab=spend`, see [ui.md](../ui.md#spend)) and
the *Agent calls* of a Task page ([ui.md](../ui.md#agent-calls-on-the-task-page)).
The SQL runs with psql on the research database and only reads. Set the
variables once per shell:

```sh
export SINCE=2026-11-10   # the rollout date
# project is the AgentProject's name as it appears in agent_calls.project
```

The cost overview of [`hack/spend-report.sql`](../usage.md#spend-report-script)
for each project and for a milestone that ran after the rollout:

```sh
psql "$DATABASE_URL" -v project=ai-operator -f hack/spend-report.sql
psql "$DATABASE_URL" -v project=bloop -v milestone=<iid> -f hack/spend-report.sql
psql "$DATABASE_URL" -v project=bloop -v issue=<iid> -f hack/spend-report.sql   # one issue's calls in order
```

The script doesn't break down checkpoints or step sizes. The queries below
do; they read the same tables as the Spend tab (`agent_calls`, and the
`checkpoint` events in `history_events` for checkpoints without a call).

### 2.1 A checkpoint after a usage limit, and its fold

**Desk:** the project's Spend tab, *Checkpoints* in the totals. The tooltip
(`byCheckpoint` of `GET /api/v1/spend`) lists `usage-limit` ≥ 1. Open the
task: in its next run, the run's steps contain **fold checkpoints**, and
the branch has no `chore(wip): checkpoint …` commit at the tip.

```sh
psql "$DATABASE_URL" -v project=bloop -v since="$SINCE" <<'SQL'
SELECT issue_iid, task, run, seq, kind, at, checkpoint_sha
FROM agent_calls
WHERE project = :'project' AND at >= :'since'::date AND checkpoint = 'usage-limit'
ORDER BY at;
SQL
```

For each row, check that the next run of the same `task` folded it: the
run report on the Task page shows the *fold checkpoints* step without
"The checkpoint `<sha>` was kept". On the branch,
`git log --format='%h %s' origin/<branch>` must not show `checkpoint_sha`
any more after the fold.

**Pass:** at least one usage-limit checkpoint, and every one is folded or
kept with a reason in the report (someone else pushed).

### 2.2 A checkpoint made by a new pod after a lost pod

**Desk:** *Checkpoints* in the Spend totals or on the Task page lists
`pod-lost` (or `oom-kill`). These have no agent call, so they add to the
count but not to *calls*.

```sh
psql "$DATABASE_URL" -v project=bloop -v since="$SINCE" <<'SQL'
SELECT issue_iid, task, data->>'run' AS run, data->>'reason' AS reason, data->>'sha' AS sha, at
FROM history_events
WHERE kind = 'checkpoint' AND project = :'project' AND at >= :'since'::date
  AND data->>'reason' IN ('pod-lost', 'oom-kill', 'budget-stop')
ORDER BY at;
SQL
```

**Pass:** if a pod was lost in the week, a `pod-lost` row with a `sha`
exists, and the next run's first step folded it, as in 2.1. If no pod was
lost, write "not observed". That's no failure, but the check stays open
for the next week.

### 2.3 Conventional squash commits on main

In a clone of each repository (ai-operator and Bloop):

```sh
git fetch origin
git log --since="$SINCE" --format=%s origin/main \
  | grep -Ev '^(feat|fix|refactor|perf|test|docs|build|ci|chore|style|revert)(\([a-z0-9._/-]+\))?!?: .{1,72}$'
```

That's the default `workflow.commitTypes`. Use the project's own list if it
sets one. Also count the commits that only passed as the fallback:

```sh
git log --since="$SINCE" --format=%s origin/main | grep -c '^chore(agent): '
```

**Pass:** the first command prints nothing. A commit it prints wasn't
squashed into a conventional MR title, so check who merged it and how. Many
`chore(agent):` subjects mean the describe role rarely gets the format right.

### 2.4 Several step commits for a run of about 1 500 lines (Bloop)

**Desk:** Bloop's Spend tab, *Step commits* below *Implementer steps*:
median, p90 and max lines per step commit, and the step-gate nudges
(`stepLines` of the API). With `maxStepLines: 400`, the median should be
below 400 and the nudges above 0.

Per run, the step commits and their lines:

```sh
psql "$DATABASE_URL" -v project=bloop -v since="$SINCE" <<'SQL'
SELECT issue_iid, task, run,
  count(*) FILTER (WHERE lines_added + lines_deleted > 0) AS step_commits,
  sum(lines_added + lines_deleted) AS lines,
  max(lines_added + lines_deleted) AS max_step_lines,
  sum(step_gate_nudges) AS nudges
FROM agent_calls
WHERE project = :'project' AND at >= :'since'::date AND kind = 'implement'
GROUP BY issue_iid, task, run
HAVING sum(lines_added + lines_deleted) >= 1000
ORDER BY lines DESC;
SQL
```

**Pass:** the runs with about 1 500 lines or more have several step commits
(3 or more for 1 500 lines at 400 per step), and their `max_step_lines` is
seldom far above 400.

### 2.5 Comparison with the baseline

The spec's baseline: the 16 merged Bloop agent MRs !726 to !741, mostly one
commit of 1 500–4 000 changed lines. Only !733, !734 and !740 had 2–4 step
commits. Take the Bloop agent MRs merged since the rollout (at most 16,
the latest first) and count, per MR, the commits and changed lines (GitLab:
the MR's *Commits* tab and diff stats, or
`git log --oneline origin/main..origin/<branch>` before the branch is
deleted).

**Pass:** most MRs of 1 500 lines or more have several commits. The
baseline had 3 of 16 MRs with more than one commit, so the count should be
clearly higher.

## Results

Fill in one week after the rollout date.

| # | Check | Where | Expected | Result | Date / notes |
|---|---|---|---|---|---|
| 1 | Usage-limit checkpoint and its fold | Spend tab *Checkpoints*; query 2.1; run report *fold checkpoints* | `usage-limit` ≥ 1, folded | | |
| 2 | Lost-pod checkpoint by the new pod | Spend/Task page *Checkpoints*; query 2.2 | `pod-lost` ≥ 1 (or not observed) | | |
| 3 | Conventional squash commits on main | `git log` 2.3, ai-operator | no output | | |
| 3 | Conventional squash commits on main | `git log` 2.3, Bloop | no output | | |
| 4 | Step commits of a ~1 500-line run | Bloop Spend tab *Step commits*; query 2.4 | several commits, median < 400 | | |
| 5 | Against the baseline !726–!741 | Bloop MRs since the rollout | clearly more than 3 of 16 with several commits | | |
| – | Fallbacks at 90 % | run reports, Spend *Calls* (fallback engine) | sonnet runs while the main engine is ≥ 90 % used | | |
| – | Per-issue alert | Desk prompts / tracking issue | alerts for issues above $15 | | |
| – | Conflict role | Spend *By kind and model* | `conflict` calls on claude-sonnet-5-5 | | |

If a check fails, open an issue with the query output and link it here.
