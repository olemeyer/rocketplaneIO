-- rocketplaneIO Control-Plane — Initial-Schema (Multi-Tenant: Org > Cluster > Namespace)
-- Postgres 16. Idempotent genug für einen einfachen Migrations-Runner (IF NOT EXISTS).

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

-- ── Users ──────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email       text NOT NULL UNIQUE,
  name        text NOT NULL DEFAULT '',
  avatar_url  text NOT NULL DEFAULT '',
  google_sub  text UNIQUE,                      -- OIDC subject (stabile Google-User-ID)
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- ── Orgs (Tenant-Grenze) ───────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS orgs (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text NOT NULL,
  slug        text NOT NULL UNIQUE,
  is_personal boolean NOT NULL DEFAULT false,
  created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- ── Memberships (User <-> Org, N:M) ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS memberships (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id     uuid NOT NULL REFERENCES orgs(id)  ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       text NOT NULL DEFAULT 'owner',      -- owner | admin | member
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_memberships_user ON memberships(user_id);

-- ── Clusters (gehören zu einer Org; Identität = kube-system-Namespace-UID) ──
CREATE TABLE IF NOT EXISTS clusters (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name          text NOT NULL,
  k8s_uid       text,                            -- UID des kube-system-Namespace (nach Enroll gesetzt)
  status        text NOT NULL DEFAULT 'pending', -- pending | connected | stale
  agent_version text NOT NULL DEFAULT '',
  last_seen_at  timestamptz,
  created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, k8s_uid)                        -- ein Cluster je Org (k8s_uid NULL erlaubt Mehrfach-Pending)
);
CREATE INDEX IF NOT EXISTS idx_clusters_org ON clusters(org_id);

-- ── Namespaces (gehören zu einem Cluster; vom Agent gesynct) ───────────────
CREATE TABLE IF NOT EXISTS namespaces (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id    uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  name          text NOT NULL,
  k8s_uid       text NOT NULL DEFAULT '',
  phase         text NOT NULL DEFAULT 'Active',
  labels        jsonb NOT NULL DEFAULT '{}',
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (cluster_id, name)
);
CREATE INDEX IF NOT EXISTS idx_namespaces_cluster ON namespaces(cluster_id);

-- ── Enrollment-Tokens (einmalig, für den Connect-Flow) ─────────────────────
CREATE TABLE IF NOT EXISTS enrollment_tokens (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id     uuid NOT NULL REFERENCES orgs(id)     ON DELETE CASCADE,
  cluster_id uuid REFERENCES clusters(id)          ON DELETE CASCADE,
  token_hash text NOT NULL,                        -- sha256(secret) — Klartext nur einmal ausgegeben
  created_by uuid REFERENCES users(id)             ON DELETE SET NULL,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_enroll_tokens_hash ON enrollment_tokens(token_hash);

-- ── Cluster-Credentials (langlebiges Agent-Token nach Enroll) ──────────────
CREATE TABLE IF NOT EXISTS cluster_credentials (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  token_hash text NOT NULL,                        -- sha256(agentToken)
  created_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_cluster_creds_hash ON cluster_credentials(token_hash);
