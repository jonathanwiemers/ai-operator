-- Research runs, their results and the items of a run (news, findings).
-- Retention deletes expired runs; results and items cascade.

CREATE TABLE research_runs (
    id            uuid PRIMARY KEY,
    namespace     text NOT NULL,
    research      text NOT NULL,
    started_at    timestamptz NOT NULL,
    finished_at   timestamptz,
    status        text NOT NULL CHECK (status IN ('running', 'succeeded', 'degraded', 'failed', 'limited')),
    engine        text NOT NULL DEFAULT '',
    model         text NOT NULL DEFAULT '',
    effort        text NOT NULL DEFAULT '',
    cost_usd      numeric(10, 4),
    choice_reason text NOT NULL DEFAULT '',
    error         text NOT NULL DEFAULT '',
    warnings      text[] NOT NULL DEFAULT '{}',
    expires_at    timestamptz NOT NULL
);
CREATE INDEX research_runs_research_idx ON research_runs (namespace, research, started_at DESC);
CREATE INDEX research_runs_expires_idx ON research_runs (expires_at);

CREATE TABLE research_results (
    run_id     uuid PRIMARY KEY REFERENCES research_runs ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    title      text NOT NULL,
    summary    text NOT NULL,
    blocks     jsonb NOT NULL,
    search     tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', title), 'A') ||
        setweight(to_tsvector('simple', summary), 'B') ||
        jsonb_to_tsvector('simple', blocks, '["string"]')
    ) STORED
);
CREATE INDEX research_results_search_idx ON research_results USING gin (search);

CREATE TABLE research_items (
    run_id     uuid NOT NULL REFERENCES research_runs ON DELETE CASCADE,
    url_hash   bytea NOT NULL,
    topic      text NOT NULL DEFAULT '',
    first_seen timestamptz NOT NULL,
    PRIMARY KEY (run_id, url_hash)
);
CREATE INDEX research_items_url_hash_idx ON research_items (url_hash);
