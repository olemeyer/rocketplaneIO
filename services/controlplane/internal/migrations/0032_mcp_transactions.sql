-- MCP transactions — the session-spanning envelope for external AI agents.
--
-- An external agent (Claude Code, Cursor, …) connects via the MCP endpoint and
-- MUST open a transaction before mutating anything. Every read and every action
-- is logged under the transaction; all mutations run through the snapshot
-- substrate, so the transaction is the rollback unit: commit keeps the changes,
-- cancel or deadline expiry restores every captured before-state LIFO.

CREATE TABLE IF NOT EXISTS mcp_transactions (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id             uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  cluster_id         uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  token_id           uuid REFERENCES api_tokens(id) ON DELETE SET NULL,
  requested_by       uuid REFERENCES users(id) ON DELETE SET NULL, -- token creator (actor)
  incident_id        uuid REFERENCES incidents(id) ON DELETE SET NULL,
  title              text NOT NULL, -- the agent's stated intent
  status             text NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open','committed','cancelling','rolled_back','rollback_failed')),
  close_reason       text CHECK (close_reason IN ('commit','cancel','expired')),
  deadline           timestamptz NOT NULL,
  -- The one snapshot_restore run that rolls the whole transaction back.
  -- Set exactly once (idempotency guard for the reaper).
  rollback_action_id uuid,
  created_at         timestamptz NOT NULL DEFAULT now(),
  closed_at          timestamptz,
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_mcp_txn_cluster ON mcp_transactions (cluster_id, created_at DESC);
-- The reaper scans open/cancelling transactions by deadline.
CREATE INDEX IF NOT EXISTS idx_mcp_txn_active ON mcp_transactions (status, deadline)
  WHERE status IN ('open','cancelling');
-- One open transaction per (token, cluster): a second begin_transaction is
-- rejected and told to reuse/close the existing one. One token per agent.
CREATE UNIQUE INDEX IF NOT EXISTS uq_mcp_txn_open_token ON mcp_transactions (token_id, cluster_id)
  WHERE status = 'open' AND token_id IS NOT NULL;

-- Append-only timeline of everything that happened inside a transaction:
-- tool calls (incl. reads that have no action row), action refs, approval
-- decisions, lifecycle transitions. seq is a global bigserial so the UI can
-- resume an SSE-driven timeline with ?from=<seq> (same pattern as
-- cluster_action_events).
CREATE TABLE IF NOT EXISTS mcp_transaction_events (
  seq       bigserial PRIMARY KEY,
  txn_id    uuid NOT NULL REFERENCES mcp_transactions(id) ON DELETE CASCADE,
  type      text NOT NULL,
  tool      text NOT NULL DEFAULT '',
  action_id uuid,
  payload   jsonb, -- redacted args/result summary, capped by the writer
  at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_mcp_txn_events ON mcp_transaction_events (txn_id, seq);

-- Bridge: every mutation inside a transaction creates an action group linked
-- to it (origin='mcp'), so the existing runs/steps/snapshots machinery renders
-- and reverts unchanged.
ALTER TABLE action_groups
  ADD COLUMN IF NOT EXISTS transaction_id uuid REFERENCES mcp_transactions(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_action_groups_txn ON action_groups (transaction_id);

-- Approval gate: gated actions are parked as status='awaiting_approval' (never
-- claimed by the agent — ClaimPendingActions only takes 'pending'). Approve
-- flips them to pending; reject cancels them.
ALTER TABLE cluster_actions
  ADD COLUMN IF NOT EXISTS approval_state text NOT NULL DEFAULT 'none'
    CHECK (approval_state IN ('none','pending','approved','rejected')),
  ADD COLUMN IF NOT EXISTS approved_by uuid REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS approval_decided_at timestamptz;

-- Small org-scoped settings KV (first consumer: key 'mcp' with the approval
-- policy + TTL bounds). Defaults live in code and are fail-closed.
CREATE TABLE IF NOT EXISTS org_settings (
  org_id     uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  key        text NOT NULL,
  value      jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, key)
);
