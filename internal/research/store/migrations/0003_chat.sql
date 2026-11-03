-- Desk chat: messages of a task (user, agent, system), appended by the UI's
-- chat writer (user rows only, RLS) and by the operator as the owner (agent
-- and system rows, state updates). pg_notify wakes the operator; retention
-- deletes expired rows. UNIQUE (author, client_key) makes composer retries
-- idempotent; UNIQUE (reply_to) makes answer inserts idempotent. A reply
-- outlives its message until its own expires_at (ON DELETE SET NULL).

CREATE TABLE chat_messages (
    id          bigserial PRIMARY KEY,
    namespace   text NOT NULL,
    project     text NOT NULL,
    task        text NOT NULL,
    author      text NOT NULL DEFAULT '',
    author_name text NOT NULL DEFAULT '',
    role        text NOT NULL CHECK (role IN ('user', 'agent', 'system')),
    kind        text NOT NULL DEFAULT 'message' CHECK (kind IN ('message', 'interrupt', 'command')),
    body        text NOT NULL,
    reply_to    bigint UNIQUE REFERENCES chat_messages (id) ON DELETE SET NULL,
    client_key  text,
    state       text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'delivered', 'answered', 'failed', 'deferred')),
    reason      text NOT NULL DEFAULT '',
    at          timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    UNIQUE (author, client_key)
);
CREATE INDEX chat_messages_task_idx ON chat_messages (namespace, project, task, id);
CREATE INDEX chat_messages_queued_idx ON chat_messages (id) WHERE state = 'queued';
CREATE INDEX chat_messages_expires_idx ON chat_messages (expires_at);

CREATE OR REPLACE FUNCTION chat_notify() RETURNS trigger AS $$
BEGIN
    IF NEW.role = 'user' THEN
        PERFORM pg_notify('chat', NEW.id::text);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER chat_messages_notify
    AFTER INSERT ON chat_messages
    FOR EACH ROW EXECUTE FUNCTION chat_notify();

ALTER TABLE chat_messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY chat_messages_select ON chat_messages FOR SELECT USING (true);
CREATE POLICY chat_messages_insert ON chat_messages FOR INSERT WITH CHECK (
    role = 'user' AND state = 'queued' AND reply_to IS NULL AND length(body) <= 16384
);
