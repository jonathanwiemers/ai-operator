-- One row per agent call (an engine invocation: implement step, review, fix,
-- pilot turn, chat turn, ...), written by the operator as the owner from the
-- AgentTasks' status.run.calls and read by the UI's spend views. The key
-- (call/<task UID>/<seq>) makes writes idempotent: a call seen twice is one
-- row. cost_usd is NULL when the cost is unknown (never 0). models holds the
-- per-model split ({model, inputTokens, outputTokens, cacheReadTokens,
-- cacheWriteTokens, costUSD}), context_series the context size of each
-- main-thread turn (at most 500 points) and subagent_turns the indexes into
-- it after which a subagent ran. Retention deletes expired rows.

CREATE TABLE agent_calls (
    id                 bigserial PRIMARY KEY,
    key                text NOT NULL UNIQUE,
    at                 timestamptz NOT NULL,
    namespace          text NOT NULL,
    project            text NOT NULL,
    milestone_iid      bigint,
    issue_iid          bigint,
    mr_iid             bigint,
    task               text NOT NULL,
    run                bigint NOT NULL DEFAULT 0,
    seq                bigint NOT NULL,
    kind               text NOT NULL,
    role               text NOT NULL DEFAULT '',
    engine             text NOT NULL DEFAULT '',
    model              text NOT NULL DEFAULT '',
    effort             text NOT NULL DEFAULT '',
    resumed            boolean NOT NULL DEFAULT false,
    fallback_from      text NOT NULL DEFAULT '',
    input_tokens       bigint NOT NULL DEFAULT 0,
    output_tokens      bigint NOT NULL DEFAULT 0,
    cache_read_tokens  bigint NOT NULL DEFAULT 0,
    cache_write_tokens bigint NOT NULL DEFAULT 0,
    cost_usd           numeric(12, 4),
    cost_source        text NOT NULL CHECK (cost_source IN ('reported', 'estimated', 'unknown')),
    turns              int NOT NULL DEFAULT 0,
    duration_ms        bigint NOT NULL DEFAULT 0,
    models             jsonb NOT NULL DEFAULT '[]',
    subagent_calls     int NOT NULL DEFAULT 0,
    context_series     jsonb NOT NULL DEFAULT '[]',
    subagent_turns     jsonb NOT NULL DEFAULT '[]',
    expires_at         timestamptz NOT NULL
);
CREATE INDEX agent_calls_milestone_idx ON agent_calls (namespace, project, milestone_iid);
CREATE INDEX agent_calls_issue_idx ON agent_calls (namespace, project, issue_iid);
CREATE INDEX agent_calls_task_idx ON agent_calls (namespace, project, task, run);
CREATE INDEX agent_calls_expires_idx ON agent_calls (expires_at);
