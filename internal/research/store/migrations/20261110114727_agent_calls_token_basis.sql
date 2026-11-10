-- token_basis is how the runner computed the call's tokens and model
-- split: 1 = the sum of the call's per-model figures, each this call's or
-- chat turn's own share (R6, R7, #255). 0 = not recorded: rows written
-- before this column, and calls from an older agent image, whose tokens
-- may be the main thread's usage and whose models may hold a resumed
-- session's earlier totals. Not backfilled.
ALTER TABLE agent_calls ADD COLUMN token_basis smallint NOT NULL DEFAULT 0;
