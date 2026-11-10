# Rollout on netcup01: pricing, budgets, stepSession and the scout subagent

Acceptance checklist for the netcup01 follow-up of
[the spend spec](../specs/spend-tokens-desk.spec.md#non-goals) (issue #267).
That milestone shipped `pricing:` (#69), budgets (#80–#82),
`implement.stepSession` (#84) and `subagents:` with a `scout` example
(#94/#95), and it left the netcup01 config to an infra follow-up. The rollout
is an infra change in `jo.wiemers/infra/cluster`. This repository can't merge
or check that change, and it can't read production data. **The owner makes
the infra changes in the order below and, once enough runs have been
recorded, runs the comparison and decides decisions 8 and 9 of the spec.**
Write the results into the [results table](#results).

## 1. Order, and why

The keys used here are checked strictly in two places. The operator parses
the pricing file strictly: an unknown key (or a negative price) stops it at
start. An older CRD prunes or rejects AgentProject fields it doesn't know.
So the cluster must run a release that has every key before the values use
them:

- [ ] **The release.** The cluster runs an operator release that contains
      `pricing` (chart value), `spec.budget.perIssueUSD`,
      `spec.budget.perIssueAlertUSD`, `agents.implement.stepSession` and
      `subagents`. Any release with the ACP `usage_update` parsing (#262)
      has all of them; a release that has this checklist does too. Check
      what runs:

      ```sh
      helm -n ai-operator-system list            # CHART column: ai-operator-<version>
      kubectl -n ai-operator-system get deploy -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.template.spec.containers[0].image}{"\n"}{end}'
      kubectl get crd agentprojects.ai.wiemers.me -o yaml \
        | grep -cE 'perIssueUSD:|perIssueAlertUSD:|stepSession:|subagents:'   # > 0 for each key
      ```

      If the cluster pins an older chart or image, bump it in the same infra
      MR or merge the bump first.
- [ ] **Then the infra MR** with the pricing table (2) and the budgets (3).
      `stepSession` and the scout (5) come later, in their own MRs, after
      the comparison (4).
- [ ] Write down the **rollout date** (when the operator picked the change
      up, in UTC). The comparison starts from it.

## 2. Pricing

Under `clusters/netcup01/applications/ai-operator/`, in the operator's Helm
values. The format is the chart's `pricing` value
([charts/ai-operator/values.yaml](../../charts/ai-operator/values.yaml),
[configuration.md](../configuration.md#pricing)): prices in USD per million
tokens under `models`, keyed `"<engine>/<model>"`, a bare model or
`"<engine>/*"`, looked up in that order; each price has `input`, `output`,
`cacheRead` and `cacheWrite` (a missing one is 0).

First list the cursor models in use: what the AgentProjects configure (roles,
`profiles`, `fallback`s, `agents.models` aliases) and what the calls actually
ran on:

```sh
kubectl get agentprojects -A -o json \
  | jq -r '[.. | objects | select(.engine? == "cursor") | (.model // "") | if . == "" then "auto" else . end] | unique[]'
psql "$DATABASE_URL" -c "SELECT model, count(*) FROM agent_calls WHERE engine = 'cursor' GROUP BY 1 ORDER BY 2 DESC;"
```

One entry per model from these lists, plus the `cursor/*` fallback for
models added later. With the models seen so far:

```yaml
pricing:
  models:
    # USD per million tokens. Replace every <usd> with the provider's price
    # list (cursor's model pricing) before merging. Prices are placeholders
    # on purpose: a wrong figure would show as an authoritative estimate.
    "cursor/auto":             {input: <usd>, output: <usd>, cacheRead: <usd>, cacheWrite: <usd>}
    "cursor/gpt-5.6-sol-high": {input: <usd>, output: <usd>, cacheRead: <usd>, cacheWrite: <usd>}
    # ...one line per further cursor model from the lists above...
    "cursor/*":                {input: <usd>, output: <usd>, cacheRead: <usd>, cacheWrite: <usd>}
```

Claude needs no entry: its calls keep the `total_cost_usd` claude reports
(`cost_source = reported`), even with a price for the model.

Before you merge:

- [ ] No `<usd>` is left (`grep -n '<usd>' <values file>` prints nothing).
      A placeholder left in, like any unknown key or negative price, stops
      the operator at start: it parses `pricing.yaml` strictly.
- [ ] `helm template` with the infra values renders the ConfigMap
      `<release>-pricing` and the operator's `--pricing-file` argument
      (`helm lint --strict` passes).
- [ ] After the rollout the operator pod restarted (a pricing change rolls
      it) and its log shows no pricing error.

What to expect:

- Prices apply to calls written from then on. Rows already in `agent_calls`
  keep their cost: nothing is re-priced (D69-4, D262-P3).
- A cursor call gets an estimate (`cost_source = estimated`) only when it
  reports tokens. The cursor-agent checked so far sends no usage over ACP
  ([the ACP finding](../specs/spend-tokens-desk.spec.md#finding-cursor-acp-usage)),
  so its calls may stay `unknown` ("n/a" on the Desk, never $0) and, with
  model `auto`, count as *not metered*. That is no failure of the pricing
  table; check 2 in the [results](#results) records which case holds.

## 3. Budgets

On each AgentProject (ai-operator and Bloop), merged into the existing
`spec` (see [configuration.md](../configuration.md#specbudget)).
`budget.perIssueAlertUSD: "15"` is already set by the
[checkpoints rollout](rollout-checkpoints-acceptance.md): keep it.

```yaml
spec:
  budget:
    perIssueUSD: "<usd>"       # e.g. "20": the per-issue budget
    perIssueAlertUSD: "15"     # already set; keep it
```

Both are decimal strings (pattern `^[0-9]+(\.[0-9]+)?$`), quoted.
`perIssueUSD` should be above `perIssueAlertUSD`, otherwise the 15-dollar
heads-up comes after the pause.

Each autopilot milestone gets its own budget as a line in the milestone
description ([commands.md](../commands.md), [usage.md](../usage.md#budget)):

```
/agent budget <usd>
```

What happens ([usage.md](../usage.md#per-issue-budget)):

| Spend | Per issue (`perIssueUSD` or the issue's `/agent budget`) | Milestone (`/agent budget` in the description) |
|---|---|---|
| `perIssueAlertUSD` | one heads-up on the tracking issue, no pause | – |
| 80 % | one heads-up and one Desk Inbox item (`budgetWarning`) with a browser notification | one `budget` prompt in the Desk (raise, keep going, pause now), an Inbox item and a notification |
| 100 % | autopilot pauses (`pauseKind: budget`, `pausedFor` the issue); running calls finish, the run stops before the next call | autopilot pauses (`pauseKind: budget`); the `budget` prompt offers raise, end the milestone, stay paused |
| to go on | raise it above the spend: `/agent budget <usd>` on the issue (or `budget <usd>` in the task chat), `/agent budget default`, or skip the issue | raise the description's line above the spend (or a raise option of the prompt), or remove the line |

Calls without a known cost (cursor without tokens, see 2) count $0
towards a budget.

Before you merge:

- [ ] The AgentProject YAML validates against the cluster's CRD:
      `kubectl apply --dry-run=server -f <file>`, or the infra repo's own
      schema check.
- [ ] Each running autopilot milestone that should have a budget has the
      `/agent budget <usd>` line. After the next poll, the spec task's
      `status.autopilot.budget` shows `limitUSD` and `spentUSD`, and
      `status.autopilot.items[].budget` the issues' budgets:

      ```sh
      kubectl -n agents get agenttask <spec task> -o jsonpath='{.status.autopilot.budget}{"\n"}{range .status.autopilot.items[*]}{.iid}{"\t"}{.budget}{"\n"}{end}'
      ```

## 4. The comparison, after enough runs

Wait for enough runs: at least about 20 implement calls on each side of
each split (fresh/resumed, with/without subagent) per project. With the
default `stepSession: resume`, *fresh* is mostly first steps and *resumed*
the later ones; without configured subagents, *with* means claude's
built-in subagents ran.

**Desk:** the project's or a milestone's *Spend* tab
(`/projects/<namespace>/<name>?tab=spend`, see [ui.md](../ui.md#spend)):

- *Implementer steps: resumed vs fresh*: tokens and cost per call.
- *With vs without subagents*: per role, cost per call and the subagent
  models' share of the tokens.
- *By kind and model*: the scout's model as its own row; the cache-read
  share in the totals.

**SQL:** psql on the research database, read-only like
[`hack/spend-report.sql`](../usage.md#spend-report-script) (`project` is
the AgentProject's name as in `agent_calls.project`). Costs average over the
calls with a known cost; `calls_unknown` counts the others. The cache-read
share is cache reads of all input tokens (input + cache read + cache write),
as in the Spend tab.

```sh
export SINCE=2026-11-10   # the rollout date
```

### 4.1 Implement steps: fresh vs resumed

```sh
psql "$DATABASE_URL" -v project=ai-operator -v since="$SINCE" <<'SQL'
SELECT CASE WHEN resumed THEN 'resumed' ELSE 'fresh' END AS session,
	count(*) AS calls,
	count(*) FILTER (WHERE cost_usd IS NULL) AS calls_unknown,
	round(avg(input_tokens)) AS avg_input,
	round(avg(output_tokens)) AS avg_output,
	round(avg(cache_read_tokens)) AS avg_cache_read,
	round(avg(cache_write_tokens)) AS avg_cache_write,
	round(avg(cost_usd), 4) AS avg_cost_usd,
	round(coalesce(sum(cost_usd), 0), 2) AS cost_usd,
	round(100.0 * sum(cache_read_tokens) / nullif(sum(input_tokens + cache_read_tokens + cache_write_tokens), 0), 1) AS cache_read_pct
FROM agent_calls
WHERE project = :'project' AND expires_at > now() AND at >= :'since'::date AND kind = 'implement'
GROUP BY 1
ORDER BY 1;
SQL
```

Per run, so a long run's growing context shows (the cost of its first,
fresh step against the later ones):

```sh
psql "$DATABASE_URL" -v project=ai-operator -v since="$SINCE" <<'SQL'
SELECT issue_iid, task, run,
	count(*) AS steps,
	round(avg(cost_usd) FILTER (WHERE NOT resumed), 4) AS fresh_avg_cost_usd,
	round(avg(cost_usd) FILTER (WHERE resumed), 4) AS resumed_avg_cost_usd,
	round(coalesce(sum(cost_usd), 0), 2) AS run_cost_usd
FROM agent_calls
WHERE project = :'project' AND expires_at > now() AND at >= :'since'::date AND kind = 'implement'
GROUP BY issue_iid, task, run
HAVING count(*) >= 3
ORDER BY run_cost_usd DESC
LIMIT 20;
SQL
```

### 4.2 Calls with vs without a subagent

Per role, the calls in which a subagent ran (`subagent_calls > 0`) against
the others:

```sh
psql "$DATABASE_URL" -v project=ai-operator -v since="$SINCE" <<'SQL'
SELECT role, subagent_calls > 0 AS with_subagent,
	count(*) AS calls,
	count(*) FILTER (WHERE cost_usd IS NULL) AS calls_unknown,
	sum(subagent_calls) AS subagent_calls,
	round(avg(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens)) AS avg_tokens,
	round(avg(cost_usd), 4) AS avg_cost_usd,
	round(coalesce(sum(cost_usd), 0), 2) AS cost_usd,
	round(100.0 * sum(cache_read_tokens) / nullif(sum(input_tokens + cache_read_tokens + cache_write_tokens), 0), 1) AS cache_read_pct
FROM agent_calls
WHERE project = :'project' AND expires_at > now() AND at >= :'since'::date AND engine = 'claude'
GROUP BY 1, 2
ORDER BY 1, 2;
SQL
```

The per-model split of the same calls (`models`; a call without one counts
whole on its own model): the subagent models' share of tokens and cost, and
whether the main model reads fewer tokens per call when a subagent ran:

```sh
psql "$DATABASE_URL" -v project=ai-operator -v since="$SINCE" <<'SQL'
SELECT c.role, c.subagent_calls > 0 AS with_subagent, m.model,
	count(DISTINCT c.id) AS calls,
	sum(m.input_tokens) AS input_tokens, sum(m.output_tokens) AS output_tokens,
	sum(m.cache_read_tokens) AS cache_read_tokens, sum(m.cache_write_tokens) AS cache_write_tokens,
	round(sum(m.input_tokens + m.output_tokens + m.cache_read_tokens + m.cache_write_tokens) / count(DISTINCT c.id)) AS tokens_per_call,
	round(coalesce(sum(m.cost_usd), 0), 2) AS cost_usd
FROM agent_calls c CROSS JOIN LATERAL (
	SELECT e->>'model' AS model,
		coalesce((e->>'inputTokens')::bigint, 0) AS input_tokens,
		coalesce((e->>'outputTokens')::bigint, 0) AS output_tokens,
		coalesce((e->>'cacheReadTokens')::bigint, 0) AS cache_read_tokens,
		coalesce((e->>'cacheWriteTokens')::bigint, 0) AS cache_write_tokens,
		(e->>'costUSD')::numeric AS cost_usd
	FROM jsonb_array_elements(c.models) AS e
	UNION ALL
	SELECT c.model, c.input_tokens, c.output_tokens, c.cache_read_tokens, c.cache_write_tokens, c.cost_usd
	WHERE jsonb_array_length(c.models) = 0
) m
WHERE c.project = :'project' AND c.expires_at > now() AND c.at >= :'since'::date AND c.engine = 'claude'
GROUP BY 1, 2, 3
ORDER BY 1, 2, cost_usd DESC;
SQL
```

The Spend tab's breakdowns are the reference: they also handle older rows
whose models don't add up to the row's tokens (counted whole on the main
model). For calls since the rollout both agree.

### 4.3 Reading it

- **stepSession (decision 8).** Flip to `fresh` when the resumed steps cost
  clearly more per call than the fresh ones (say 1.5× or more) and the
  long runs' cost grows step by step, while the fresh steps' cache-read
  share stays reasonable. Keep `resume` when the difference is small:
  `fresh` loses what the session knew beyond the notes and the code.
- **Scout (decision 9).** Enable it when the implement calls *with* a
  subagent cost less per call than those without, and the main model's
  tokens per call drop. A high subagent share with no saving means the
  scout reads more than the role would have: keep it off.

## 5. The owner's decisions

Each as its own infra MR, merged into the AgentProject's `spec`, checked
with `kubectl apply --dry-run=server -f <file>` before merging. Field names
as in `api/v1alpha1` (`ImplementSpec.StepSession`, `RoleSpec.Subagents`)
and `config/crd/bases/ai.wiemers.me_agentprojects.yaml`.

**Decision 8, `stepSession: fresh`** (also applies to the profiles,
[configuration.md](../configuration.md#roles)):

```yaml
spec:
  agents:
    implement:
      stepSession: fresh       # resume (default) or fresh
```

**Decision 9, the read-only scout** for the implementer, as in the
[chart README](../../charts/ai-operator/README.md#subagents) and
[configuration.md](../configuration.md#subagents) (claude only; a call on
cursor goes without it). A profile has its own `subagents`: set it there
too when a profile's implementer should get the scout.

```yaml
spec:
  agents:
    implement:
      subagents:
        - name: scout
          description: Searches the code, looks things up on the web and reads logs. Use it for every search and lookup.
          model: claude-haiku-5-5
          tools: [Read, Grep, Glob, WebFetch, WebSearch]
          prompt: You search and read for the implementing agent and never edit. Answer in bullets with exact names, paths and line numbers, no preamble. The caller checks line numbers before editing.
```

After either change, run 4 again from its rollout date and compare with
the numbers before.

## Results

| # | Check | Where | Expected | Result | Date / notes |
|---|---|---|---|---|---|
| 1 | Release with every key | `helm list`, image, CRD grep (1) | chart/image version ≥ the release with #262 | | |
| 2 | Pricing live | operator log, Spend tab *Calls* of a cursor call | operator starts; cursor calls with tokens `estimated`, without `unknown`/not metered | | |
| 3 | Per-issue budget | `kubectl apply --dry-run=server`, `status.autopilot.items[].budget` | `perIssueUSD` accepted and shown, `perIssueAlertUSD` kept | | |
| 3 | Milestone budget | `status.autopilot.budget` | `limitUSD` = the description's `/agent budget` | | |
| 3 | 80 % / 100 % | Desk Inbox, tracking issue | one Inbox item at 80 %, a budget pause at 100 %, raising resumes | | |
| 4 | Fresh vs resumed, ai-operator | Spend tab; query 4.1 | cost, tokens, cache-read share per step | | |
| 4 | Fresh vs resumed, Bloop | Spend tab; query 4.1 | cost, tokens, cache-read share per step | | |
| 4 | With vs without subagent, ai-operator | Spend tab; query 4.2 | cost per call, tokens per call, subagent share | | |
| 4 | With vs without subagent, Bloop | Spend tab; query 4.2 | cost per call, tokens per call, subagent share | | |
| 5 | Decision 8 `stepSession` | infra MR | `fresh` or keep `resume`, with the reason | | |
| 5 | Decision 9 scout | infra MR | enabled or not, with the reason | | |

If a check fails, open an issue with the query output and link it here.
