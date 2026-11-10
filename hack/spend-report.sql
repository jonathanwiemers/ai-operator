-- Per-issue cost analysis from agent_calls, for the Spend tab's questions
-- ("why did #179 cost $53.88", "what do conflict rounds cost") when the tab is
-- not at hand (docs/usage.md "Spend report script"). It only reads.
--
--   psql "$DATABASE_URL" -v project=ai-operator -v milestone=12 -f hack/spend-report.sql
--   psql "$DATABASE_URL" -v project=ai-operator -v issue=179 -f hack/spend-report.sql
--
-- project is required. milestone and issue narrow the calls (0 or unset:
-- all) and the third report needs an issue. Costs sum the known costs of
-- the calls (an unknown cost is not $0, see calls_unknown). Models are the
-- calls' own models: a subagent's tokens are in its call's tokens, the Spend
-- tab's "By kind and model" splits them. Rounds are the review rounds of
-- "Review rounds" in the Spend tab: within a run, round n is the review
-- calls after its (n-1)th fix call by seq. The fourth report lists the
-- cursor and auto calls one by one ("which call ran on auto"), each with
-- the engine of the call after it in its run.

\if :{?milestone}
\else
\set milestone 0
\endif
\if :{?issue}
\else
\set issue 0
\endif

\echo
\echo '== Per issue: kinds, models, rounds, calls, tokens, cost'

WITH calls AS (
	SELECT *, 1 + count(*) FILTER (WHERE kind = 'fix') OVER (PARTITION BY namespace, task, run ORDER BY seq, id) AS round
	FROM agent_calls
	WHERE project = :'project' AND expires_at > now()
		AND (:'milestone'::bigint = 0 OR milestone_iid = :'milestone'::bigint)
		AND (:'issue'::bigint = 0 OR issue_iid = :'issue'::bigint)
)
SELECT coalesce(issue_iid::text, '(milestone)') AS issue,
	string_agg(DISTINCT kind, ',' ORDER BY kind) AS kinds,
	string_agg(DISTINCT model, ',' ORDER BY model) AS models,
	count(DISTINCT (task, run)) AS runs,
	coalesce(max(round) FILTER (WHERE kind = 'review'), 0) AS review_rounds,
	count(*) AS calls,
	count(*) FILTER (WHERE cost_usd IS NULL) AS calls_unknown,
	sum(input_tokens) AS input_tokens, sum(output_tokens) AS output_tokens,
	sum(cache_read_tokens) AS cache_read_tokens, sum(cache_write_tokens) AS cache_write_tokens,
	round(coalesce(sum(cost_usd), 0), 2) AS cost_usd,
	round(sum(cost_usd) / nullif(sum(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0) * 1e6, 2) AS usd_per_1m_tokens
FROM calls
GROUP BY issue_iid
ORDER BY sum(cost_usd) DESC NULLS LAST, issue_iid;

\echo
\echo '== Conflict calls per issue and model (runs with more than one conflict call repeat)'

SELECT coalesce(issue_iid::text, '(milestone)') AS issue, model,
	count(DISTINCT (task, run)) AS runs, count(*) AS calls,
	sum(input_tokens) AS input_tokens, sum(output_tokens) AS output_tokens,
	sum(cache_read_tokens) AS cache_read_tokens, sum(cache_write_tokens) AS cache_write_tokens,
	round(coalesce(sum(cost_usd), 0), 2) AS cost_usd,
	round(sum(cost_usd) / nullif(sum(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0) * 1e6, 2) AS usd_per_1m_tokens,
	max(calls_in_run) AS max_calls_in_run
FROM (
	SELECT *, count(*) OVER (PARTITION BY namespace, task, run) AS calls_in_run
	FROM agent_calls
	WHERE project = :'project' AND expires_at > now() AND kind = 'conflict'
		AND (:'milestone'::bigint = 0 OR milestone_iid = :'milestone'::bigint)
		AND (:'issue'::bigint = 0 OR issue_iid = :'issue'::bigint)
) c
GROUP BY issue_iid, model
ORDER BY sum(cost_usd) DESC NULLS LAST, issue_iid, model;

\echo
\echo '== Calls of the issue (-v issue=N)'

SELECT task, run, seq, kind, role, model, resumed,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	cost_usd, round(duration_ms / 1000.0, 1) AS duration_s
FROM agent_calls
WHERE project = :'project' AND expires_at > now() AND issue_iid = :'issue'::bigint
	AND (:'milestone'::bigint = 0 OR milestone_iid = :'milestone'::bigint)
ORDER BY task, run, seq, id;

\echo
\echo '== Cursor and auto calls (engine cursor or model auto), with the next call of each run'

-- A cursor call that hit its usage limit without a result is still a row,
-- with neither cost nor tokens (unmetered); when the role has a fallback,
-- the next call of the run is the fallback's (next_fallback_from = cursor).
SELECT at, kind, role, coalesce(issue_iid::text, '(milestone)') AS issue, task, run, seq, engine, model,
	fallback_from, cost_source, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	cost_usd, round(duration_ms / 1000.0, 1) AS duration_s, next_engine, next_fallback_from
FROM (
	SELECT *, lead(engine) OVER w AS next_engine, lead(fallback_from) OVER w AS next_fallback_from
	FROM agent_calls
	WHERE project = :'project' AND expires_at > now()
		AND (:'milestone'::bigint = 0 OR milestone_iid = :'milestone'::bigint)
		AND (:'issue'::bigint = 0 OR issue_iid = :'issue'::bigint)
	WINDOW w AS (PARTITION BY namespace, task, run ORDER BY seq, id)
) c
WHERE engine = 'cursor' OR model = 'auto'
ORDER BY at, id;
