-- Type de projet détecté quand le code n'a pas de Dockerfile.
ALTER TABLE builds ADD COLUMN IF NOT EXISTS detected VARCHAR(30) NOT NULL DEFAULT '';
