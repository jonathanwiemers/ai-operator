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
CREATE FUNCTION pg_temp.shifted(blocks jsonb, delta interval) RETURNS jsonb LANGUAGE sql AS $$
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

COMMIT;
