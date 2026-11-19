-- The history of a plan's decisions (#615): every version of each decision
-- in AgentPlan.status.decisions, and every owner's sign-off of a version. The
-- status keeps only the current list and the latest sign-off of each
-- version; the operator copies it here as the owner each time it copies the
-- plan's chat replies, and the UI reads it. A version's content does not
-- change once stored, except its why, which the agent may reword without a
-- new version. Rows carry the chat history retention's expires_at and
-- retention deletes them, as it does the plan's chat messages.
--
-- seq is the number after "D" of the decision ID, for sorting. decided_by is
-- "agent" or the owner's OIDC subject.

CREATE TABLE plan_decision_versions (
    namespace       text NOT NULL,
    project         text NOT NULL,
    plan            text NOT NULL,
    decision_id     text NOT NULL,
    seq             int NOT NULL DEFAULT 0,
    version         bigint NOT NULL,
    title           text NOT NULL DEFAULT '',
    question        text NOT NULL DEFAULT '',
    options         text[] NOT NULL DEFAULT '{}',
    recommended     int NOT NULL DEFAULT 0,
    why             text NOT NULL DEFAULT '',
    state           text NOT NULL CHECK (state IN ('open', 'locked')),
    decided_by      text NOT NULL DEFAULT '',
    decided_by_name text NOT NULL DEFAULT '',
    decided_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    PRIMARY KEY (namespace, project, plan, decision_id, version)
);
CREATE INDEX plan_decision_versions_plan_idx ON plan_decision_versions (namespace, project, plan, seq, version DESC);
CREATE INDEX plan_decision_versions_expires_idx ON plan_decision_versions (expires_at);

-- One owner's sign-off of a version. A second owner's keep, or the same
-- owner's later one, is a row of its own; the primary key reads one plan.
CREATE TABLE plan_decision_signoffs (
    namespace          text NOT NULL,
    project            text NOT NULL,
    plan               text NOT NULL,
    decision_id        text NOT NULL,
    version            bigint NOT NULL,
    signed_off_by      text NOT NULL,
    signed_off_by_name text NOT NULL DEFAULT '',
    signed_off_at      timestamptz NOT NULL,
    expires_at         timestamptz NOT NULL,
    PRIMARY KEY (namespace, project, plan, decision_id, version, signed_off_by, signed_off_at)
);
CREATE INDEX plan_decision_signoffs_expires_idx ON plan_decision_signoffs (expires_at);
