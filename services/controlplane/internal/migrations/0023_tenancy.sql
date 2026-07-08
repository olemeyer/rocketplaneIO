-- Tenancy & Access Control: platform-admin (super admin), user suspension,
-- org invitations and a mutation audit log. Roles (owner|admin|member) already
-- exist on memberships (0001) but were never enforced — the API layer now gates
-- on them; this migration adds the surrounding surface.

-- ── Super admin + suspension on the global user ──────────────────────────────
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_platform_admin boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_at timestamptz;

-- The very first user of a fresh instance is the platform admin (self-hosting:
-- the person who ran setup owns the box). Idempotent: only fires while exactly
-- one user exists and none is admin yet.
UPDATE users SET is_platform_admin = true
WHERE (SELECT count(*) FROM users) = 1
  AND NOT EXISTS (SELECT 1 FROM users WHERE is_platform_admin);

-- ── Org invitations (email → role, token-based accept) ───────────────────────
CREATE TABLE IF NOT EXISTS invitations (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id      uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  email       text NOT NULL,
  role        text NOT NULL DEFAULT 'member',          -- member | admin | owner
  token_hash  text NOT NULL UNIQUE,                    -- sha256 of the invite token
  invited_by  uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  accepted_at timestamptz,
  accepted_by uuid REFERENCES users(id) ON DELETE SET NULL
);
-- One live invite per (org,email): a re-invite replaces the pending one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_pending
  ON invitations(org_id, lower(email)) WHERE accepted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_invitations_org ON invitations(org_id, created_at DESC);

-- ── Audit log (who did what, when) ───────────────────────────────────────────
-- Every mutating request that matters (member/role/cluster/action/alert changes)
-- writes one row. org_id nullable for platform-admin-level actions.
CREATE TABLE IF NOT EXISTS audit_log (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id      uuid REFERENCES orgs(id) ON DELETE CASCADE,
  actor_id    uuid REFERENCES users(id) ON DELETE SET NULL,
  actor_email text NOT NULL DEFAULT '',
  action      text NOT NULL,                           -- e.g. member.role_changed, cluster.created
  target_type text NOT NULL DEFAULT '',                -- member | cluster | alert_rule | action | org | user
  target_id   text NOT NULL DEFAULT '',
  target_name text NOT NULL DEFAULT '',
  metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
  ip          text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_org ON audit_log(org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log(actor_id, created_at DESC);
