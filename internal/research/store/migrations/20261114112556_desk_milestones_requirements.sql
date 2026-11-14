-- The number of requirements (distinct R<n> lines) of a milestone's
-- description, for the cost estimate's spend per requirement (#426). NULL is
-- unknown: rows from before this column and milestones not counted yet. The
-- snapshot does not write it; SetMilestoneRequirements does.
ALTER TABLE desk_milestones ADD COLUMN requirements int;
