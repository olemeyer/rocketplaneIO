-- API tokens / service accounts (Round 4): programmatic access to the
-- control-plane API without a browser session. A token is org-scoped, carries an
-- effective role (member|admin — NEVER owner/platform-admin) and is sent as a
-- Bearer `rp_…`. Only the SHA-256 hash is stored; the secret is
-- visible exactly once (at creation time). created_by CASCADE: deleting the
-- creating user removes their tokens (no orphaned principal).
CREATE TABLE IF NOT EXISTS api_tokens (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id       uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name         text NOT NULL,
  role         text NOT NULL DEFAULT 'member',   -- member | admin
  token_hash   text NOT NULL UNIQUE,             -- sha256(secret), hex
  prefix       text NOT NULL,                    -- display prefix, e.g. "rp_a1b2c3d4"
  created_by   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  expires_at   timestamptz,
  revoked_at   timestamptz
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_org ON api_tokens(org_id, created_at DESC);
