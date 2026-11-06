-- checkpoint is the reason (usage-limit, engine-error, ...) when the agent
-- call's end saved a checkpoint commit of the work tree, checkpoint_sha that
-- commit. lines_added and lines_deleted are the uncommitted diff an
-- implementer call left (the lines of the step commit after it); other
-- calls, and calls before this migration, read '' and 0.

ALTER TABLE agent_calls
    ADD COLUMN checkpoint text NOT NULL DEFAULT '',
    ADD COLUMN checkpoint_sha text NOT NULL DEFAULT '',
    ADD COLUMN lines_added bigint NOT NULL DEFAULT 0,
    ADD COLUMN lines_deleted bigint NOT NULL DEFAULT 0;
