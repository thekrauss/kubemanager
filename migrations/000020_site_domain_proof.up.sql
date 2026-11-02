-- Domaines des sites : preuve de propriété par enregistrement TXT, comme pour
-- les conteneurs. Une demande ne réserve plus le nom (sinon n'importe qui
-- pouvait bloquer le vrai propriétaire) : seul un domaine vérifié le réserve.
-- Un domaine déjà vérifié le reste, sans jeton : il cède devant une preuve.
ALTER TABLE site_domains ADD COLUMN IF NOT EXISTS token VARCHAR(64) NOT NULL DEFAULT '';
UPDATE site_domains SET token = 'betorun-verify=' || gen_random_uuid() WHERE NOT verified AND token = '';
ALTER TABLE site_domains DROP CONSTRAINT IF EXISTS uq_site_domains_hostname;
CREATE UNIQUE INDEX IF NOT EXISTS uq_site_domains_verified ON site_domains (hostname) WHERE verified;
CREATE UNIQUE INDEX IF NOT EXISTS uq_site_domains_site_hostname ON site_domains (site_id, hostname);
