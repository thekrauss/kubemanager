DROP INDEX IF EXISTS uq_site_domains_site_hostname;
DROP INDEX IF EXISTS uq_site_domains_verified;
-- Échoue si un même nom a été demandé par plusieurs sites : à résoudre à la main.
ALTER TABLE site_domains ADD CONSTRAINT uq_site_domains_hostname UNIQUE (hostname);
ALTER TABLE site_domains DROP COLUMN IF EXISTS token;
