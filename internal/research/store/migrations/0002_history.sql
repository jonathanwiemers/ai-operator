-- The factory's history: what happened in a project (comments, runs, pushes,
-- CI, reviews, merges, autopilot), appended by the operator as the owner and
-- read by the UI. The key makes writes idempotent: the same transition or
-- comment seen twice is one row. Retention deletes expired rows.

CREATE TABLE history_events (
    id            bigserial PRIMARY KEY,
    key           text NOT NULL UNIQUE,
    at            timestamptz NOT NULL,
    namespace     text NOT NULL,
    project       text NOT NULL,
    milestone_iid bigint,
    issue_iid     bigint,
    mr_iid        bigint,
    task          text NOT NULL DEFAULT '',
    kind          text NOT NULL,
    actor         text NOT NULL DEFAULT '',
    summary       text NOT NULL DEFAULT '',
    data          jsonb NOT NULL DEFAULT '{}',
    expires_at    timestamptz NOT NULL,
    search        tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', summary), 'A') ||
        setweight(to_tsvector('simple', coalesce(data->>'body', '')), 'B')
    ) STORED
);
CREATE INDEX history_events_project_idx ON history_events (namespace, project, at DESC, id DESC);
CREATE INDEX history_events_at_idx ON history_events (at DESC, id DESC);
CREATE INDEX history_events_issue_idx ON history_events (namespace, project, issue_iid) WHERE issue_iid IS NOT NULL;
CREATE INDEX history_events_milestone_idx ON history_events (namespace, project, milestone_iid) WHERE milestone_iid IS NOT NULL;
CREATE INDEX history_events_search_idx ON history_events USING gin (search);
CREATE INDEX history_events_expires_idx ON history_events (expires_at);
