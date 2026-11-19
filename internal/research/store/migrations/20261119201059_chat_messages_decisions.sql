-- Plan decision answers: an owner's answers to a plan's decisions travel
-- with one plan chat message, as a JSON array of {id, version, keep |
-- option | other}. NULL on every other message and on old rows. The insert
-- policy is unchanged: the chat writer still inserts only queued user rows,
-- now with or without answers.
ALTER TABLE chat_messages ADD COLUMN decisions jsonb;
