DROP INDEX IF EXISTS uq_workloads_project_name;
ALTER TABLE workloads DROP COLUMN IF EXISTS spec;
