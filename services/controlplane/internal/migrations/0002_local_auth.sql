-- Lokale Accounts (First-User-Setup / n8n-Muster): Passwort-Login als Default-Weg,
-- Google SSO bleibt additiv. password_hash ist NULL für reine SSO-User.
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash text;
