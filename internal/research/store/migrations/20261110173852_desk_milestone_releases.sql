-- The release decisions of a milestone (R10, #287), so a done milestone keeps
-- them after the operator deleted its tasks. One row per decision, keyed by
-- its ID in the task's status (release_id). The operator writes them as the
-- owner and the UI reads them. Like follow-ups, rows are never deleted by a
-- later snapshot: status trims old ended decisions, the snapshot keeps them.
-- Zero IIDs and times are NULL. state is the decision's state as the
-- operator names it (Planned, Released, ...), not checked here so a new
-- state needs no migration. expires_at is the time of the row's last change
-- plus the history retention, as for desk_milestone_issues; retention
-- deletes a milestone with its releases once all of its rows expired.

CREATE TABLE desk_milestone_releases (
    namespace     text NOT NULL,
    project       text NOT NULL,
    milestone_iid bigint NOT NULL,
    release_id    bigint NOT NULL,
    by            text NOT NULL DEFAULT '',
    actor         text NOT NULL DEFAULT '',
    why           text NOT NULL DEFAULT '',
    after_iid     bigint,
    sha           text NOT NULL DEFAULT '',
    released_sha  text NOT NULL DEFAULT '',
    state         text NOT NULL DEFAULT '',
    reason        text NOT NULL DEFAULT '',
    issues        bigint[] NOT NULL DEFAULT '{}',
    mr_iids       bigint[] NOT NULL DEFAULT '{}',
    version       text NOT NULL DEFAULT '',
    pipeline_url  text NOT NULL DEFAULT '',
    release_url   text NOT NULL DEFAULT '',
    decided_at    timestamptz NOT NULL,
    started_at    timestamptz,
    ended_at      timestamptz,
    expires_at    timestamptz NOT NULL,
    PRIMARY KEY (namespace, project, milestone_iid, release_id),
    FOREIGN KEY (namespace, project, milestone_iid)
        REFERENCES desk_milestones (namespace, project, iid) ON DELETE CASCADE
);
