DROP TABLE IF EXISTS site_domains;
ALTER TABLE IF EXISTS sites DROP CONSTRAINT IF EXISTS fk_sites_active_deployment;
DROP TABLE IF EXISTS site_deployments;
DROP TABLE IF EXISTS sites;
