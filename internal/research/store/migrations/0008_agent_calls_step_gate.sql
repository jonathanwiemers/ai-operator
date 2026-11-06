-- step_gate_nudges is how often the step-size gate (ai-runner step-gate, a
-- PostToolUse hook) told the implementer to close its step during the
-- call. Other calls, and calls before this migration, read 0.

ALTER TABLE agent_calls ADD COLUMN step_gate_nudges int NOT NULL DEFAULT 0;
