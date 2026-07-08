-- API-Tokens / Service-Accounts (Round 4): programmatischer Zugriff auf die
-- Control-Plane-API ohne Browser-Session. Ein Token ist org-scoped, trägt eine
-- effektive Rolle (member|admin — NIE owner/platform-admin) und wird als
-- Bearer `rp_…` gesendet. Gespeichert wird nur der SHA-256-Hash; das Geheimnis
-- ist genau einmal (bei Erstellung) sichtbar. created_by CASCADE: löscht man den
-- erstellenden Nutzer, verschwinden seine Tokens (kein verwaistes Principal).
CREATE TABLE IF NOT EXISTS api_tokens (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id       uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name         text NOT NULL,
  role         text NOT NULL DEFAULT 'member',   -- member | admin
  token_hash   text NOT NULL UNIQUE,             -- sha256(secret), hex
  prefix       text NOT NULL,                    -- Anzeige-Präfix, z.B. "rp_a1b2c3d4"
  created_by   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  expires_at   timestamptz,
  revoked_at   timestamptz
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_org ON api_tokens(org_id, created_at DESC);
