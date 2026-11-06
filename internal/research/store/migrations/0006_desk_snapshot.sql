-- The Desk's snapshot of milestones: what a milestone was (title, state,
-- autopilot phase, times, owners, retrospective), its issues and its
-- follow-ups, so a milestone still shows after the operator deleted its
-- tasks. The operator writes them as the owner from task status and the UI
-- reads them. Writes are idempotent: a repeated snapshot changes no row.
-- Issue rows leave with a re-plan, follow-up rows never do. expires_at is the
-- time of the row's last change plus the history retention; retention deletes
-- a milestone with its issues and follow-ups once all of them expired.

CREATE TABLE desk_milestones (
    namespace       text NOT NULL,
    project         text NOT NULL,
    iid             bigint NOT NULL,
    milestone_id    bigint NOT NULL DEFAULT 0,
    title           text NOT NULL DEFAULT '',
    web_url         text NOT NULL DEFAULT '',
    state           text NOT NULL CHECK (state IN ('open', 'closed')),
    phase           text NOT NULL DEFAULT '',
    started_at      timestamptz,
    done_at         timestamptz,
    tracking_iid    bigint,
    owners          text[] NOT NULL DEFAULT '{}',
    owner_subjects  text[] NOT NULL DEFAULT '{}',
    retro_note_id   bigint,
    retro_text      text NOT NULL DEFAULT '',
    retro_posted_at timestamptz,
    updated_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    PRIMARY KEY (namespace, project, iid)
);
CREATE INDEX desk_milestones_project_idx ON desk_milestones (namespace, project, state);
CREATE INDEX desk_milestones_done_idx ON desk_milestones (state, done_at DESC);
CREATE INDEX desk_milestones_expires_idx ON desk_milestones (expires_at);

CREATE TABLE desk_milestone_issues (
    namespace     text NOT NULL,
    project       text NOT NULL,
    milestone_iid bigint NOT NULL,
    iid           bigint NOT NULL,
    title         text NOT NULL DEFAULT '',
    state         text NOT NULL DEFAULT '',
    mr_iid        bigint,
    mr_url        text NOT NULL DEFAULT '',
    position      int NOT NULL DEFAULT 0,
    expires_at    timestamptz NOT NULL,
    PRIMARY KEY (namespace, project, milestone_iid, iid),
    FOREIGN KEY (namespace, project, milestone_iid)
        REFERENCES desk_milestones (namespace, project, iid) ON DELETE CASCADE
);

CREATE TABLE desk_follow_ups (
    namespace     text NOT NULL,
    project       text NOT NULL,
    milestone_iid bigint NOT NULL,
    number        int NOT NULL,
    title         text NOT NULL DEFAULT '',
    body          text NOT NULL DEFAULT '',
    state         text NOT NULL CHECK (state IN ('open', 'issue', 'milestone', 'dismissed')),
    target_iid    bigint,
    expires_at    timestamptz NOT NULL,
    PRIMARY KEY (namespace, project, milestone_iid, number),
    FOREIGN KEY (namespace, project, milestone_iid)
        REFERENCES desk_milestones (namespace, project, iid) ON DELETE CASCADE
);
