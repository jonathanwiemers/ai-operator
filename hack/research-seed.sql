-- Development data for the research pages: make research-seed applies it as
-- the owner to the research database of make test-db, after migrating it
-- (see docs/ui.md "Development"). It matches hack/research-dev.yaml:
--
--   research-dev/morning-news  two news flash runs that share two URLs, so
--                              the latest one has new and seen items
--   research-dev/heat-pumps    a deep report with every block type
--   research-dev/hostile       a result that bypassed the runner's validation
--                              (raw HTML, a javascript: link): the UI must
--                              withhold it ("Result withheld")
--   research-dev/dev           agent calls and run.finished events for the
--                              Spend tab (see below)
--
-- The data is written as of 2026-10-29 08:00 UTC and moved to the latest
-- such time of day before now, so the pages look fresh. Running it again
-- replaces it. Results are whole block schema v1 answers in dollar quotes
-- tagged json, or hostile for the hostile one; hack/research-migrate's test
-- checks that the json ones pass blocks.Validate and the hostile one fails.

BEGIN;
SET LOCAL TimeZone = 'UTC';

DELETE FROM research_runs WHERE namespace = 'research-dev';

CREATE TEMP TABLE seed (
    id uuid, research text, started timestamptz, finished timestamptz, status text,
    engine text, model text, effort text, cost numeric, reason text, warnings text[], result jsonb
) ON COMMIT DROP;

INSERT INTO seed VALUES
('5eed0000-0000-4000-8000-000000000001', 'morning-news', '2026-10-28 06:00:04Z', '2026-10-28 06:03:12Z', 'succeeded',
 'claude', 'claude-sonnet-5-5', 'medium', 0.1830, 'chosen: claude/claude-sonnet-5-5', '{}', $json$
{"schemaVersion": 1, "title": "Morning news, 28 October", "summary": "A quiet day: one chip launch, the climate talks open.", "blocks": [
  {"type": "items", "items": [
    {"topic": "Tech", "title": "Chipmaker announces 1.4 nm production line", "summary": "Volume production is planned for 2028.", "url": "https://news.example.com/tech/chips-1-4nm", "source": "Example News", "publishedAt": "2026-10-27T18:30:00Z", "importance": 2},
    {"topic": "World", "title": "Climate talks open with a call for faster cuts", "url": "https://world.example.org/climate-talks-open", "source": "World Example", "publishedAt": "2026-10-28T05:10:00Z", "importance": 3},
    {"topic": "Science", "title": "Probe returns samples from a near-Earth asteroid", "url": "https://science.example.net/asteroid-samples", "source": "Science Example", "publishedAt": "2026-10-27T21:00:00Z", "importance": 2},
    {"topic": "World", "title": "Rail strike ends after two days", "url": "https://world.example.org/rail-strike-ends", "source": "World Example", "publishedAt": "2026-10-27T16:45:00Z", "importance": 1}
  ]}
]}
$json$),
('5eed0000-0000-4000-8000-000000000002', 'morning-news', '2026-10-29 06:00:03Z', '2026-10-29 06:03:41Z', 'succeeded',
 'claude', 'claude-sonnet-5-5', 'medium', 0.2140, 'chosen: claude/claude-sonnet-5-5', '{}', $json$
{"schemaVersion": 1, "title": "Morning news, 29 October", "summary": "The climate talks agree on a draft, a new open model tops the benchmarks, and a long-standing bug in a popular compression library is fixed.", "blocks": [
  {"type": "items", "items": [
    {"topic": "Tech", "title": "Open-weights model tops the coding benchmarks", "summary": "The release comes with a permissive licence and a 1M-token context.", "url": "https://news.example.com/tech/open-model-benchmarks", "source": "Example News", "publishedAt": "2026-10-29T04:20:00Z", "importance": 3},
    {"topic": "Tech", "title": "Chipmaker announces 1.4 nm production line", "summary": "Volume production is planned for 2028.", "url": "https://news.example.com/tech/chips-1-4nm", "source": "Example News", "publishedAt": "2026-10-27T18:30:00Z", "importance": 2},
    {"topic": "Tech", "title": "Compression library fixes a 12-year-old overflow", "summary": "All maintained versions get a patch release today.", "url": "https://security.example.com/advisories/compress-overflow", "source": "Security Example", "publishedAt": "2026-10-28T22:05:00Z", "importance": 2},
    {"topic": "World", "title": "Climate talks agree on a draft text", "summary": "Delegates expect the final vote on Friday.", "url": "https://world.example.org/climate-draft", "source": "World Example", "publishedAt": "2026-10-29T03:00:00Z", "importance": 3},
    {"topic": "World", "title": "Climate talks open with a call for faster cuts", "url": "https://world.example.org/climate-talks-open", "source": "World Example", "publishedAt": "2026-10-28T05:10:00Z", "importance": 2},
    {"topic": "Science", "title": "Gravitational waves from a neutron star merger detected again", "url": "https://science.example.net/neutron-star-merger", "source": "Science Example", "publishedAt": "2026-10-28T19:40:00Z", "importance": 2},
    {"topic": "Business", "title": "Lithium prices fall for the fifth month", "url": "https://markets.example.com/lithium-prices", "source": "Markets Example", "publishedAt": "2026-10-28T15:00:00Z", "importance": 1},
    {"title": "City opens its first car-free district", "url": "https://local.example.com/car-free-district", "source": "Local Example", "importance": 1}
  ]},
  {"type": "markdown", "text": "**Also noted:** the *rail strike* follow-up talks were postponed; see [the union's statement](https://world.example.org/union-statement)."},
  {"type": "links", "links": [
    {"title": "Climate talks: live coverage", "url": "https://world.example.org/climate-live"},
    {"title": "Benchmark results table", "url": "https://news.example.com/tech/benchmarks-2026-10"}
  ]}
]}
$json$),
('5eed0000-0000-4000-8000-000000000003', 'heat-pumps', '2026-10-27 14:12:30Z', '2026-10-27 14:58:02Z', 'succeeded',
 'claude', 'claude-opus-5-5', 'high', 2.8410, 'chosen: claude/claude-opus-5-5 (high)',
 '{"blocks[8]: unknown type \"chart\" dropped"}', $json$
{"schemaVersion": 1, "title": "Heat pumps in old buildings: what works", "summary": "Air-to-water heat pumps run efficiently in most unrenovated houses once the flow temperature stays at or below 55 °C. Larger radiators in a few rooms matter more than a full renovation.", "blocks": [
  {"type": "heading", "text": "Key figures", "level": 2},
  {"type": "stats", "stats": [
    {"label": "Median seasonal COP, old buildings", "value": "3.1", "delta": "+0.4 vs. 2020"},
    {"label": "Installations needing new radiators", "value": "38 %", "delta": "-6 pts"},
    {"label": "Typical payback", "value": "11 years"}
  ]},
  {"type": "heading", "text": "Findings", "level": 2},
  {"type": "markdown", "text": "Field studies agree on three points:\n\n1. The **flow temperature** decides the efficiency, not the building's age.\n2. Replacing the radiators of the *two or three coldest rooms* is usually enough.\n3. Hydraulic balancing is cheap and often skipped.\n\n> A heat pump sized for the coldest day runs most of the year at part load.\n\nTo check a house, measure the flow temperature on a cold day:\n\n```\nflow ≤ 55 °C  → suitable as is\nflow > 55 °C  → enlarge radiators first\n```\n\nSee the [field test report](https://research.example.org/heat-pumps/field-test) for the data. `<script>` in a code span stays text."},
  {"type": "items", "items": [
    {"topic": "Studies", "title": "Field test: 56 heat pumps in existing buildings", "summary": "Seasonal COP between 2.6 and 4.1, median 3.1.", "url": "https://research.example.org/heat-pumps/field-test", "source": "Research Example", "publishedAt": "2025-06-12T00:00:00Z", "importance": 3},
    {"topic": "Studies", "title": "Radiator sizing guide for retrofits", "url": "https://guides.example.com/radiator-sizing", "source": "Guides Example", "importance": 2}
  ]},
  {"type": "heading", "text": "Costs compared", "level": 3},
  {"type": "table", "columns": ["System", "Install cost", "Running cost per year", "Notes"], "rows": [
    ["Air-to-water heat pump", "€14,000–€22,000", "€1,250", "Subsidies cover up to 50 %"],
    ["Gas condensing boiler", "€9,000–€12,000", "€1,900", "CO₂ price rises yearly"],
    ["Ground-source heat pump", "€25,000–€35,000", "€980", "Needs a drilling permit"]
  ]},
  {"type": "links", "links": [
    {"title": "Field test report", "url": "https://research.example.org/heat-pumps/field-test"},
    {"title": "Subsidy programme overview", "url": "https://gov.example.org/subsidies/heating"}
  ]}
]}
$json$),
('5eed0000-0000-4000-8000-000000000004', 'hostile', '2026-10-29 07:30:00Z', '2026-10-29 07:41:20Z', 'succeeded',
 'cursor', 'auto', '', 0.0400, 'chosen: cursor/auto (claude: 5-hour window at 85 %)', '{}', $hostile$
{"schemaVersion": 1, "title": "Totally normal result", "summary": "This row was written behind the validator's back.", "blocks": [
  {"type": "markdown", "text": "<script>alert('xss')</script> and [click me](javascript:alert(1))"},
  {"type": "links", "links": [{"title": "Click", "url": "javascript:alert(document.cookie)"}]}
]}
$hostile$);

-- Move the data from 2026-10-29 08:00 UTC to the latest 08:00 UTC before now.
CREATE TEMP TABLE shift ON COMMIT DROP AS
    SELECT date_trunc('day', now() - interval '8 hours') - timestamptz '2026-10-29 00:00Z' AS delta;

-- shifted moves the publishedAt of every item in blocks by delta.
CREATE OR REPLACE FUNCTION pg_temp.shifted(blocks jsonb, delta interval) RETURNS jsonb LANGUAGE sql AS $$
    SELECT coalesce(jsonb_agg(CASE WHEN b ? 'items' THEN jsonb_set(b, '{items}', (
        SELECT jsonb_agg(CASE WHEN it ? 'publishedAt'
            THEN jsonb_set(it, '{publishedAt}', to_jsonb(to_char(
                (it->>'publishedAt')::timestamptz + delta, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
            ELSE it END ORDER BY n)
        FROM jsonb_array_elements(b->'items') WITH ORDINALITY AS x(it, n)))
        ELSE b END ORDER BY i), '[]'::jsonb)
    FROM jsonb_array_elements(blocks) WITH ORDINALITY AS y(b, i)
$$;

INSERT INTO research_runs (id, namespace, research, started_at, finished_at, status, engine, model, effort,
    cost_usd, choice_reason, warnings, expires_at)
SELECT id, 'research-dev', research, started + delta, finished + delta, status, engine, model, effort,
    cost, reason, warnings, started + delta + interval '720 hours'
FROM seed, shift;

INSERT INTO research_results (run_id, created_at, expires_at, title, summary, blocks)
SELECT s.id, r.finished_at, r.expires_at, s.result->>'title', s.result->>'summary',
    pg_temp.shifted(s.result->'blocks', shift.delta)
FROM seed s JOIN research_runs r ON r.id = s.id, shift;

-- The items as the runner stores them: one row per URL hash and run, first
-- seen when the research's earliest result with that URL was saved. The
-- seed's URLs are normalized already (lower-case host, no fragment), so
-- their sha256 is blocks.URLHash.
INSERT INTO research_items (run_id, url_hash, topic, first_seen)
SELECT DISTINCT ON (run_id, hash) run_id, hash, topic, first_seen FROM (
    SELECT res.run_id, sha256(convert_to(it.item->>'url', 'UTF8')) AS hash, coalesce(it.item->>'topic', '') AS topic,
        min(res.created_at) OVER (PARTITION BY r.namespace, r.research, sha256(convert_to(it.item->>'url', 'UTF8'))) AS first_seen
    FROM research_results res JOIN research_runs r ON r.id = res.run_id,
        jsonb_array_elements(res.blocks) AS b(block),
        jsonb_array_elements(b.block->'items') AS it(item)
    WHERE r.namespace = 'research-dev'
) AS items
ORDER BY run_id, hash;

-- Agent calls for the Spend tab of project research-dev/dev (the stream
-- needs an AgentProject dev in research-dev): milestone %4 with its spec,
-- pilot and retro calls; #77 over two runs (an opus step with a haiku
-- subagent and a context series, a resumed step, a cursor review with an
-- estimated cost, a cursor fix after a claude fallback without a cost, a CI
-- round); #78 with a run from before token tracking (only its run.finished
-- event) and one with a call; a Desk chat turn on #90 without a milestone.
-- #77's run 1 also has its run.finished event, which the calls replace.
-- hack/research-migrate's TestSeedSpend checks the sums.
DELETE FROM agent_calls WHERE namespace = 'research-dev';
DELETE FROM history_events WHERE namespace = 'research-dev' AND key LIKE 'seed/%';

CREATE TEMP TABLE seed_calls (
    at timestamptz, milestone bigint, issue bigint, mr bigint, task text, run bigint, seq bigint,
    kind text, role text, engine text, model text, effort text, resumed boolean, fallback_from text,
    input bigint, output bigint, cache_read bigint, cache_write bigint, cost numeric, source text,
    turns int, duration_ms bigint, models jsonb, subagent_calls int, context_series jsonb, subagent_turns jsonb
) ON COMMIT DROP;

INSERT INTO seed_calls VALUES
('2026-10-27 08:10Z', 4, NULL, NULL, 'dev-spec-m4', 1, 1, 'spec', 'spec', 'claude', 'claude-opus-5-5', 'high', false, '',
 42000, 9800, 310000, 52000, 1.2000, 'reported', 18, 412000, '[]', 0, '[]', '[]'),
('2026-10-27 08:30Z', 4, NULL, NULL, 'dev-spec-m4', 1, 2, 'pilot', 'pilot', 'claude', 'claude-sonnet-5-5', 'medium', false, '',
 12000, 2100, 88000, 9000, 0.3100, 'reported', 6, 95000, '[]', 0, '[]', '[]'),
('2026-10-27 09:00Z', 4, 77, 120, 'dev-issue-77', 1, 1, 'implement', 'implement', 'claude', 'claude-opus-5-5', 'high', false, '',
 71000, 24000, 610000, 73000, 2.4500, 'reported', 24, 1260000,
 '[{"model": "claude-opus-5-5", "inputTokens": 30000, "outputTokens": 18000, "cacheReadTokens": 520000, "cacheWriteTokens": 61000, "costUSD": 2.30},
   {"model": "claude-haiku-5-5", "inputTokens": 41000, "outputTokens": 6000, "cacheReadTokens": 90000, "cacheWriteTokens": 12000, "costUSD": 0.15}]',
 2, '[18000, 26500, 31000, 44000, 52500, 61000, 66000, 79000]', '[2, 5]'),
('2026-10-27 09:40Z', 4, 77, 120, 'dev-issue-77', 1, 2, 'implement', 'implement', 'claude', 'claude-opus-5-5', 'high', true, '',
 26000, 21000, 840000, 48000, 3.1000, 'reported', 21, 1080000, '[]', 0,
 '[81000, 88000, 95500, 99000, 104000, 112000, 118500, 126000]', '[]'),
('2026-10-27 10:05Z', 4, 77, 120, 'dev-issue-77', 1, 3, 'review', 'correctness', 'cursor', 'gpt-5', '', false, '',
 64000, 5200, 0, 0, 0.4200, 'estimated', 1, 240000, '[]', 0, '[]', '[]'),
('2026-10-27 13:20Z', 4, 77, 120, 'dev-issue-77', 2, 4, 'fix', 'implement', 'cursor', 'composer-1', '', false, 'claude',
 0, 0, 0, 0, NULL, 'unknown', 0, 330000, '[]', 0, '[]', '[]'),
('2026-10-27 13:45Z', 4, 77, 120, 'dev-issue-77', 2, 5, 'ci', 'implement', 'claude', 'claude-sonnet-5-5', 'medium', true, '',
 9000, 6400, 240000, 21000, 0.6600, 'reported', 9, 300000, '[]', 0, '[]', '[]'),
('2026-10-28 09:15Z', 4, 78, 121, 'dev-issue-78', 2, 1, 'implement', 'implement', 'claude', 'claude-sonnet-5-5', 'medium', false, '',
 18000, 15000, 310000, 33000, 0.9000, 'reported', 14, 720000, '[]', 0, '[]', '[]'),
('2026-10-28 11:00Z', 4, NULL, NULL, 'dev-spec-m4', 1, 3, 'retro', 'retro', 'claude', 'claude-sonnet-5-5', 'medium', true, '',
 7000, 3100, 66000, 4000, 0.2200, 'reported', 4, 80000, '[]', 0, '[]', '[]'),
('2026-10-28 15:30Z', NULL, 90, NULL, 'dev-issue-90', 0, 1, 'chat', 'chat', 'claude', 'claude-sonnet-5-5', 'low', false, '',
 3000, 900, 22000, 2500, 0.0500, 'reported', 2, 25000, '[]', 0, '[]', '[]');

INSERT INTO agent_calls (key, at, namespace, project, milestone_iid, issue_iid, mr_iid, task, run, seq, kind, role,
    engine, model, effort, resumed, fallback_from, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
    cost_usd, cost_source, turns, duration_ms, models, subagent_calls, context_series, subagent_turns, expires_at)
SELECT 'call/seed-' || task || '/' || seq, at + delta, 'research-dev', 'dev', milestone, issue, mr, task, run, seq, kind, role,
    engine, model, effort, resumed, fallback_from, input, output, cache_read, cache_write,
    cost, source, turns, duration_ms, models, subagent_calls, context_series, subagent_turns, at + delta + interval '365 days'
FROM seed_calls, shift;

INSERT INTO history_events (key, at, namespace, project, milestone_iid, issue_iid, task, kind, summary, data, expires_at)
SELECT key, at + delta, 'research-dev', 'dev', 4, issue, task, 'run.finished', summary, data, at + delta + interval '365 days'
FROM (VALUES
    ('seed/dev-issue-78/run/1', timestamptz '2026-10-26 16:00Z', 78, 'dev-issue-78', 'Run 1 finished: succeeded',
     '{"result": "Succeeded", "runCount": 1, "costUSD": "1.75"}'::jsonb),
    ('seed/dev-issue-77/run/1', timestamptz '2026-10-27 10:10Z', 77, 'dev-issue-77', 'Run 1 finished: succeeded',
     '{"result": "Succeeded", "runCount": 1, "costUSD": "5.97"}'::jsonb)
) AS e(key, at, issue, task, summary, data), shift;


-- The Desk's milestones (desk_milestones with their issues and follow-ups),
-- with history events and agent calls, so the dashboard, the milestone pages
-- and the retrospective show in dev. Running the file again replaces them
-- (the cascade removes issues and follow-ups).
--
--   research-dev/dev  %3 done long ago, without agent calls ("no spend");
--                     %4 done with a retrospective and 3 follow-ups (open,
--                     issue, dismissed; its calls are the Spend ones above);
--                     %5 waiting: the autopilot has no "waiting" phase on
--                     main yet, so it is Running with no active item
--   research-dev/web  %2 running (5 issues, 2 merged, #32 active);
--                     %3 paused (on the owner, for #41)
-- The running and paused milestones are in web, so dev's Spend sums above
-- stay as TestSeedSpend checks them.
DELETE FROM desk_milestones WHERE namespace = 'research-dev';

INSERT INTO desk_milestones (namespace, project, iid, milestone_id, title, web_url, state, phase, started_at, done_at,
    tracking_iid, owners, owner_subjects, retro_note_id, retro_text, retro_posted_at, updated_at, expires_at)
SELECT 'research-dev', project, iid, milestone_id, title, web_url, state, phase, started + delta, done + delta,
    tracking, '{alice}'::text[], '{alice-subject}'::text[], retro_note, retro, retro_at + delta,
    coalesce(done, started) + delta, coalesce(done, started) + delta + interval '365 days'
FROM (VALUES
    ('dev', 3, 903, 'Logging cleanup', 'https://gitlab.example.com/research-dev/dev/-/milestones/3', 'closed', 'done',
     timestamptz '2026-09-14 08:00Z', timestamptz '2026-09-18 16:00Z', 60, NULL::bigint, '', NULL::timestamptz),
    ('dev', 4, 904, 'Spend tracking', 'https://gitlab.example.com/research-dev/dev/-/milestones/4', 'closed', 'done',
     timestamptz '2026-10-26 08:00Z', timestamptz '2026-10-28 12:00Z', 70, 5004,
$md$# Retrospective: Spend tracking

## What went well

- Both issues merged on the first review round.
- The cost per call showed up in the Spend tab from the start.

## What went wrong

- #77 needed a second run after a cursor fallback without a cost.
- The context series of the first run grew to 126k tokens.

## Leftovers

1. Show the unmetered calls apart in the Spend tab.
2. Record the cost of a cursor fix after a fallback.
3. Chart the context series per run.
$md$, timestamptz '2026-10-28 12:05Z'),
    ('dev', 5, 905, 'Search index', 'https://gitlab.example.com/research-dev/dev/-/milestones/5', 'open', 'running',
     timestamptz '2026-10-29 07:00Z', NULL, 80, NULL, '', NULL),
    ('web', 2, 912, 'Dark mode', 'https://gitlab.example.com/research-dev/web/-/milestones/2', 'open', 'running',
     timestamptz '2026-10-28 09:00Z', NULL, 90, NULL, '', NULL),
    ('web', 3, 913, 'Checkout redesign', 'https://gitlab.example.com/research-dev/web/-/milestones/3', 'open', 'paused',
     timestamptz '2026-10-27 09:00Z', NULL, 91, NULL, '', NULL)
) AS m(project, iid, milestone_id, title, web_url, state, phase, started, done, tracking, retro_note, retro, retro_at), shift;

INSERT INTO desk_milestone_issues (namespace, project, milestone_iid, iid, title, state, mr_iid, mr_url, position, expires_at)
SELECT 'research-dev', project, ms, iid, title, state, mr,
    CASE WHEN mr IS NULL THEN '' ELSE 'https://gitlab.example.com/research-dev/' || project || '/-/merge_requests/' || mr END,
    (row_number() OVER (PARTITION BY project, ms ORDER BY iid) - 1)::int, now() + interval '365 days'
FROM (VALUES
    ('dev', 3, 50, 'Use structured logs', 'merged', 110), ('dev', 3, 51, 'Drop the debug flag', 'merged', 111),
    ('dev', 4, 77, 'Record the cost of every call', 'merged', 120), ('dev', 4, 78, 'Show spend per milestone', 'merged', 121),
    ('dev', 5, 81, 'Index the issue titles', 'merged', 130), ('dev', 5, 82, 'Query the index', 'pending', NULL),
    ('web', 2, 30, 'Colour tokens', 'merged', 200), ('web', 2, 31, 'Dark theme for the header', 'merged', 201),
    ('web', 2, 32, 'Dark theme for tables', 'active', 202), ('web', 2, 33, 'Persist the choice', 'pending', NULL),
    ('web', 2, 34, 'Follow the system setting', 'pending', NULL),
    ('web', 3, 40, 'Cart summary', 'merged', 210), ('web', 3, 41, 'Payment step', 'active', 211),
    ('web', 3, 42, 'Order confirmation', 'pending', NULL)
) AS i(project, ms, iid, title, state, mr);

INSERT INTO desk_follow_ups (namespace, project, milestone_iid, number, title, body, state, target_iid, expires_at)
SELECT 'research-dev', 'dev', 4, number, title, body, state, target, now() + interval '365 days'
FROM (VALUES
    (1, 'Show unmetered calls apart', 'The Spend tab counts them in the total. List them under their own heading.', 'open', NULL::bigint),
    (2, 'Record the cost of a cursor fix', 'After a claude fallback the call has no cost. Estimate it from tokens.', 'issue', 95),
    (3, 'Chart the context series', 'Plot the context series per run.', 'dismissed', NULL)
) AS f(number, title, body, state, target);

-- Runs, merges and pauses. A pause event's key is <task>/<kind>/<start>/<pause
-- kind>; the resumed event shares the tail with the paused one. #40 has a
-- closed pause (1h30), #41 an open one.
INSERT INTO history_events (key, at, namespace, project, milestone_iid, issue_iid, mr_iid, task, kind, summary, data, expires_at)
SELECT key, at + delta, 'research-dev', project, ms, issue, mr, task, kind, summary, data, at + delta + interval '365 days'
FROM (VALUES
    ('seed/dev-issue-77/run/start', 'dev', 4, 77, NULL::bigint, 'dev-issue-77', 'run.started', 'Run started on #77', timestamptz '2026-10-27 08:55Z', '{}'::jsonb),
    ('seed/dev-issue-77/merged', 'dev', 4, 77, 120, 'dev-issue-77', 'merged', 'Merged !120', timestamptz '2026-10-27 14:30Z', '{"sha": "a1b2c3d"}'),
    ('seed/dev-issue-78/run/start', 'dev', 4, 78, NULL, 'dev-issue-78', 'run.started', 'Run started on #78', timestamptz '2026-10-26 15:00Z', '{}'),
    ('seed/dev-issue-78/merged', 'dev', 4, 78, 121, 'dev-issue-78', 'merged', 'Merged !121', timestamptz '2026-10-28 11:30Z', '{"sha": "d4e5f6a"}'),
    ('seed/dev-issue-81/run/start', 'dev', 5, 81, NULL, 'dev-issue-81', 'run.started', 'Run started on #81', timestamptz '2026-10-29 07:05Z', '{}'),
    ('seed/dev-issue-81/merged', 'dev', 5, 81, 130, 'dev-issue-81', 'merged', 'Merged !130', timestamptz '2026-10-29 07:50Z', '{"sha": "0a1b2c3"}'),
    ('seed/web-issue-30/run/start', 'web', 2, 30, NULL, 'web-issue-30', 'run.started', 'Run started on #30', timestamptz '2026-10-28 09:05Z', '{}'),
    ('seed/web-issue-30/merged', 'web', 2, 30, 200, 'web-issue-30', 'merged', 'Merged !200', timestamptz '2026-10-28 10:20Z', '{"sha": "1111111"}'),
    ('seed/web-issue-31/run/start', 'web', 2, 31, NULL, 'web-issue-31', 'run.started', 'Run started on #31', timestamptz '2026-10-28 10:30Z', '{}'),
    ('seed/web-issue-31/merged', 'web', 2, 31, 201, 'web-issue-31', 'merged', 'Merged !201', timestamptz '2026-10-28 13:00Z', '{"sha": "2222222"}'),
    ('seed/web-issue-32/run/start', 'web', 2, 32, NULL, 'web-issue-32', 'run.started', 'Run started on #32', timestamptz '2026-10-29 06:00Z', '{}'),
    ('seed/web-issue-40/run/start', 'web', 3, 40, NULL, 'web-issue-40', 'run.started', 'Run started on #40', timestamptz '2026-10-27 09:10Z', '{}'),
    ('seed/web-issue-40/merged', 'web', 3, 40, 210, 'web-issue-40', 'merged', 'Merged !210', timestamptz '2026-10-27 14:00Z', '{"sha": "3333333"}'),
    ('seed/web-issue-41/run/start', 'web', 3, 41, NULL, 'web-issue-41', 'run.started', 'Run started on #41', timestamptz '2026-10-27 14:10Z', '{}'),
    ('seed/web-ms3/autopilot.paused/1/needsInput', 'web', 3, 40, NULL, 'web-ms3', 'autopilot.paused',
     'Autopilot paused (needsInput): the agent asks a question', timestamptz '2026-10-27 12:00Z',
     '{"kind": "needsInput", "reason": "The agent asks a question", "item": 40}'),
    ('seed/web-ms3/autopilot.resumed/1/needsInput', 'web', 3, 40, NULL, 'web-ms3', 'autopilot.resumed',
     'Autopilot resumed (needsInput) by owner', timestamptz '2026-10-27 13:30Z',
     '{"kind": "needsInput", "reason": "The agent asks a question", "item": 40, "resolvedBy": "owner", "pausedSec": 5400}'),
    ('seed/web-ms3/autopilot.paused/2/ciRed', 'web', 3, 41, NULL, 'web-ms3', 'autopilot.paused',
     'Autopilot paused (ciRed): CI of !211 is red', timestamptz '2026-10-29 05:00Z',
     '{"kind": "ciRed", "reason": "CI of !211 is red", "item": 41}')
) AS e(key, project, ms, issue, mr, task, kind, summary, at, data), shift;

INSERT INTO agent_calls (key, at, namespace, project, milestone_iid, issue_iid, mr_iid, task, run, seq, kind, role,
    engine, model, effort, resumed, fallback_from, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
    cost_usd, cost_source, turns, duration_ms, models, subagent_calls, context_series, subagent_turns, expires_at)
SELECT 'call/seed-' || task || '/1', at + delta, 'research-dev', project, ms, issue, mr, task, 1, 1, 'implement', 'implement',
    'claude', 'claude-sonnet-5-5', 'medium', false, '', input, output, input * 6, input / 4,
    cost, 'reported', 10, dur, '[]', 0, '[]', '[]', at + delta + interval '365 days'
FROM (VALUES
    ('web', 2, 30, 200, 'web-issue-30', timestamptz '2026-10-28 09:10Z', 21000, 11000, 0.7400, 2400000),
    ('web', 2, 31, 201, 'web-issue-31', timestamptz '2026-10-28 10:40Z', 30000, 15000, 1.1200, 3100000),
    ('web', 2, 32, 202, 'web-issue-32', timestamptz '2026-10-29 06:10Z', 12000, 6000, 0.4300, 1200000),
    ('web', 3, 40, 210, 'web-issue-40', timestamptz '2026-10-27 09:20Z', 25000, 12000, 0.9100, 2700000),
    ('web', 3, 41, 211, 'web-issue-41', timestamptz '2026-10-27 14:20Z', 40000, 20000, 1.6500, 4200000)
) AS c(project, ms, issue, mr, task, at, input, output, cost, dur), shift;

COMMIT;
