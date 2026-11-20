-- One row per problem of a milestone: an issue the autopilot is stuck on
-- (issue_iid), or the whole milestone (issue_iid 0). The operator opens and
-- resolves it from the problem.opened and problem.resolved history events,
-- in the same transaction; the same issue stuck again within a day reopens
-- its row. The Desk reads a problem's thread from history_events and
-- chat_messages between opened_at and resolved_at.

CREATE TABLE desk_problems (
    id            bigserial PRIMARY KEY,
    namespace     text NOT NULL,
    project       text NOT NULL,
    milestone_iid bigint NOT NULL,
    issue_iid     bigint NOT NULL DEFAULT 0,
    state         text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'resolved')),
    cause         text NOT NULL DEFAULT '',
    opened_at     timestamptz NOT NULL,
    reopened_at   timestamptz,
    resolved_at   timestamptz,
    resolved_by   text NOT NULL DEFAULT ''
);
CREATE INDEX desk_problems_milestone_idx ON desk_problems (namespace, project, milestone_iid, opened_at DESC);
CREATE UNIQUE INDEX desk_problems_open_idx ON desk_problems (namespace, project, milestone_iid, issue_iid)
    WHERE state = 'open';
CREATE INDEX desk_problems_open_at_idx ON desk_problems (opened_at DESC) WHERE state = 'open';
