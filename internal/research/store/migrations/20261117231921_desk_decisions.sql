-- The decisions of every planned milestone (#542): a copy of each decision
-- the planner or an implementer posted on an issue, and the requests to
-- revisit one. GitLab stays the source of truth: the operator writes the rows
-- as the owner each time it reads an issue, replacing that issue's rows, and
-- the UI reads them. There is no foreign key to desk_milestones, as the
-- decisions of a milestone can be read before its snapshot exists; rows carry
-- the snapshot's expires_at and retention deletes them on their own.
--
-- seq is the number after "-" or "-P" of the decision ID, for sorting.
-- supersedes is the ID this decision replaces ('' when none), superseded the
-- marker on the comment. The decision that replaced one is derived on read.

CREATE TABLE desk_decisions (
    namespace     text NOT NULL,
    project       text NOT NULL,
    milestone_iid bigint NOT NULL,
    decision_id   text NOT NULL,
    issue_iid     bigint NOT NULL,
    kind          text NOT NULL CHECK (kind IN ('planner', 'implementer')),
    seq           int NOT NULL DEFAULT 0,
    title         text NOT NULL DEFAULT '',
    question      text NOT NULL DEFAULT '',
    choice        text NOT NULL DEFAULT '',
    why           text NOT NULL DEFAULT '',
    alternatives  text[] NOT NULL DEFAULT '{}',
    supersedes    text NOT NULL DEFAULT '',
    superseded    boolean NOT NULL DEFAULT false,
    run_id        text NOT NULL DEFAULT '',
    url           text NOT NULL DEFAULT '',
    issue_title   text NOT NULL DEFAULT '',
    issue_state   text NOT NULL DEFAULT '' CHECK (issue_state IN ('', 'opened', 'closed')),
    posted_at     timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    PRIMARY KEY (namespace, project, decision_id)
);
CREATE INDEX desk_decisions_milestone_idx ON desk_decisions (namespace, project, milestone_iid);
CREATE INDEX desk_decisions_supersedes_idx ON desk_decisions (namespace, project, supersedes);
CREATE INDEX desk_decisions_expires_idx ON desk_decisions (expires_at);

-- A request to revisit a decision: a human comment (source 'comment') or a
-- follow-up issue the bot opened for it (source 'followUp'). source_issue_iid
-- is the issue the request was read from (the commented issue or the
-- follow-up), whose rows a later read of that issue replaces.
CREATE TABLE desk_decision_revisits (
    namespace        text NOT NULL,
    project          text NOT NULL,
    milestone_iid    bigint NOT NULL,
    source_issue_iid bigint NOT NULL,
    decision_id      text NOT NULL,
    source           text NOT NULL CHECK (source IN ('comment', 'followUp')),
    requested_by     text NOT NULL DEFAULT '',
    instruction      text NOT NULL DEFAULT '',
    url              text NOT NULL DEFAULT '',
    follow_up_iid    bigint,
    follow_up_state  text NOT NULL DEFAULT '' CHECK (follow_up_state IN ('', 'opened', 'closed')),
    at               timestamptz NOT NULL,
    expires_at       timestamptz NOT NULL
);
CREATE INDEX desk_decision_revisits_decision_idx ON desk_decision_revisits (namespace, project, decision_id);
CREATE INDEX desk_decision_revisits_source_idx ON desk_decision_revisits (namespace, project, source_issue_iid);
CREATE INDEX desk_decision_revisits_expires_idx ON desk_decision_revisits (expires_at);
