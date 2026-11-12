-- Time ranges over several projects (the Costs page, Store.SpendSince) filter
-- on (namespace, project) IN (...) AND at in a range. The existing indexes are
-- by milestone, issue and task, so such a range scanned every row of those
-- projects. This one serves both the project list and the time range.
CREATE INDEX agent_calls_project_at_idx ON agent_calls (namespace, project, at);
