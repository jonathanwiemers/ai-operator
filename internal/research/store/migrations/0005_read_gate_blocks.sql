-- read_gate_blocks is how many tool calls of the agent call the read gate
-- (ai-runner read-gate, a PreToolUse hook) denied. Calls before the gate
-- existed had none, so existing rows read 0.

ALTER TABLE agent_calls ADD COLUMN read_gate_blocks int NOT NULL DEFAULT 0;
