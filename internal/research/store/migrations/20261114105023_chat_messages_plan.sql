-- Plan chat: messages of an AgentPlan (the milestone planner) reuse
-- chat_messages with plan set to the plan's name and task = '' (task stays
-- NOT NULL). Old rows keep plan NULL. Task-scoped reads (stream, task chat,
-- chat context) filter plan IS NULL; the dispatcher reads both and routes by
-- plan. The insert policy is unchanged: the chat writer still inserts only
-- queued user rows, now with or without a plan.
ALTER TABLE chat_messages ADD COLUMN plan text;
CREATE INDEX chat_messages_plan_idx ON chat_messages (namespace, project, plan, id) WHERE plan IS NOT NULL;
