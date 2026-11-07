-- The backfill of the Desk's snapshot: the operator fills desk_milestones
-- once from GitLab for closed milestones the recorder never saw, one
-- milestone per poll, and records each here so it can stop and restart.
-- A milestone is done once saved (or found already stored by the recorder),
-- and failed with its attempts and last error until it succeeds; it is
-- skipped after three failures. A done row leaves with its milestone.
-- desk_backfill_projects remembers that a project's closed milestones were
-- listed, and whether all of them are backfilled. Only the operator (the
-- owner) writes them.

CREATE TABLE desk_backfill (
    namespace     text NOT NULL,
    project       text NOT NULL,
    milestone_iid bigint NOT NULL,
    status        text NOT NULL CHECK (status IN ('done', 'failed')),
    attempts      int NOT NULL DEFAULT 0,
    error         text NOT NULL DEFAULT '',
    at            timestamptz NOT NULL,
    PRIMARY KEY (namespace, project, milestone_iid)
);

CREATE TABLE desk_backfill_projects (
    namespace text NOT NULL,
    project   text NOT NULL,
    listed_at timestamptz NOT NULL,
    complete  bool NOT NULL DEFAULT false,
    PRIMARY KEY (namespace, project)
);
