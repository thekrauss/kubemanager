DROP INDEX IF EXISTS idx_builds_deploy_workload;
ALTER TABLE builds
    DROP COLUMN IF EXISTS git_commit,
    DROP COLUMN IF EXISTS git_repo_id,
    DROP COLUMN IF EXISTS git_installation_id,
    DROP COLUMN IF EXISTS deploy_workload_id,
    DROP COLUMN IF EXISTS "trigger",
    DROP COLUMN IF EXISTS deploy_state;
DROP TABLE IF EXISTS github_webhook_deliveries;
DROP TABLE IF EXISTS workload_git_links;
DROP TABLE IF EXISTS github_installations;
