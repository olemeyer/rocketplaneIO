-- Safe Actions v2 · Phase 1 — Grouping + investigation link.
--
-- Every action run now belongs to exactly ONE action_group: the "belong
-- together" + revert-together unit. A group maps to a Copilot chat turn, an
-- Investigation, an alert remediation, a manual batch — or, for a lone action,
-- a group of one. This is the load-bearing link that lets the Runs view render
-- actions that were triggered together as a single trace and (later, Phase 6)
-- revert them together. Additive + backfilled → zero downtime, no agent change.

CREATE TABLE IF NOT EXISTS action_groups (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id           uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  org_id               uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  -- where the group came from — drives how the trace is titled/grouped.
  origin               text NOT NULL DEFAULT 'manual_single'
                         CHECK (origin IN ('copilot_turn','investigation','manual_batch',
                                           'manual_single','alert_remediation','schedule')),
  chat_id              uuid REFERENCES copilot_chats(id) ON DELETE SET NULL,
  investigation_id     uuid REFERENCES copilot_investigations(id) ON DELETE SET NULL,
  incident_id          uuid REFERENCES incidents(id) ON DELETE SET NULL,
  -- LLM run/turn id — "which turn produced this group" (idempotency on resume).
  turn_id              text,
  title                text NOT NULL,
  requested_by         uuid REFERENCES users(id) ON DELETE SET NULL,
  -- forward-execution gating within the group (NOT a transactional guarantee —
  -- revert is always best-effort reverse-order compensation, see the design doc).
  atomicity            text NOT NULL DEFAULT 'independent'
                         CHECK (atomicity IN ('independent','all_or_nothing')),
  on_failure           text NOT NULL DEFAULT 'continue'
                         CHECK (on_failure IN ('continue','stop','rollback_group')),
  status               text NOT NULL DEFAULT 'pending',
  revert_status        text NOT NULL DEFAULT 'none',
  started_at           timestamptz,
  ended_at             timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

-- Hot paths: the Runs feed (recent groups per cluster) and the Investigation join.
CREATE INDEX IF NOT EXISTS idx_action_groups_cluster_recent ON action_groups (cluster_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_action_groups_investigation  ON action_groups (investigation_id);
-- idempotency for the per-turn group (one group per (chat_id, turn_id)).
CREATE UNIQUE INDEX IF NOT EXISTS uq_action_groups_turn ON action_groups (chat_id, turn_id)
  WHERE chat_id IS NOT NULL AND turn_id IS NOT NULL;

-- Extend the Run row: group linkage + the classification columns v2 fills in.
ALTER TABLE cluster_actions
  ADD COLUMN IF NOT EXISTS group_id              uuid REFERENCES action_groups(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS group_seq             int NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS investigation_node_id uuid REFERENCES copilot_investigation_nodes(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS recipe_key            text,
  ADD COLUMN IF NOT EXISTS recipe_version        int,
  ADD COLUMN IF NOT EXISTS source_kind           text NOT NULL DEFAULT 'builtin'
                            CHECK (source_kind IN ('builtin','script')),
  ADD COLUMN IF NOT EXISTS risk                  text,
  ADD COLUMN IF NOT EXISTS reversibility         text,
  ADD COLUMN IF NOT EXISTS effect_classes        text[];

CREATE INDEX IF NOT EXISTS idx_cluster_actions_group ON cluster_actions (group_id, group_seq);

-- Backfill: give every pre-v2 action its own group-of-one (origin=manual_single),
-- so the Runs view is uniform from day one. Idempotent — only touches rows that
-- do not yet have a group, so re-running the migration is a no-op.
DO $$
DECLARE r RECORD; gid uuid;
BEGIN
  FOR r IN
    SELECT a.id, a.cluster_id, a.kind, a.target_name, a.requested_by, a.status, a.created_at, c.org_id
    FROM cluster_actions a JOIN clusters c ON c.id = a.cluster_id
    WHERE a.group_id IS NULL
  LOOP
    INSERT INTO action_groups (cluster_id, org_id, origin, title, requested_by, status, created_at, updated_at)
    VALUES (r.cluster_id, r.org_id, 'manual_single',
            left(coalesce(r.kind,'action') || ' ' || coalesce(r.target_name,''), 200),
            r.requested_by,
            CASE WHEN r.status IN ('succeeded','failed','cancelled') THEN r.status ELSE 'pending' END,
            r.created_at, r.created_at)
    RETURNING id INTO gid;
    UPDATE cluster_actions SET group_id = gid, group_seq = 0 WHERE id = r.id;
  END LOOP;
END $$;
