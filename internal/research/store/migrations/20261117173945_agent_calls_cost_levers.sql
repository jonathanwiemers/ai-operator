-- Cost levers (#538): the experiment arm of the call's issue, the
-- step-session mode of an implement call of a stepwise run (resume or
-- fresh), what each tool put into the context ({"Read": {"calls": n,
-- "bytes": n}, ...}, claude only) and the context size of the first turn
-- of a call that started its session. NULL: none, not recorded, or rows
-- from before these columns. No index: the Metrics queries filter by time
-- first.
ALTER TABLE agent_calls
	ADD COLUMN arm TEXT NULL,
	ADD COLUMN step_session TEXT NULL,
	ADD COLUMN tool_results JSONB NULL,
	ADD COLUMN base_context_tokens BIGINT NULL;
