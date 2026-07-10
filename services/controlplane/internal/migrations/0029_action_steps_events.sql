-- Safe Actions v2 · Phase 2 — first-class steps + a durable live-event spine.
--
-- Today an action's steps live only in an ephemeral `steps` jsonb blob and live
-- progress is a fire-and-forget SSE line. v2 promotes steps to timestamped rows
-- and adds an append-only, globally-sequenced event log so EVERY action streams
-- the same real-time steps everywhere and replays identically on reload
-- (seed via ?from=0, resume via ?from=<lastSeq>). Additive: the jsonb cache on
-- cluster_actions stays as a cheap denormalized view for list rendering.

-- Steps as durable span rows. group_id is safe to require because 0028 gave
-- every action a group; pre-v2 actions simply have zero step rows (their jsonb
-- cache still renders their history).
CREATE TABLE IF NOT EXISTS cluster_action_steps (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  action_id    uuid NOT NULL REFERENCES cluster_actions(id) ON DELETE CASCADE,
  group_id     uuid NOT NULL REFERENCES action_groups(id) ON DELETE CASCADE,
  seq          int  NOT NULL,
  parent_seq   int,                                -- nesting: a recipe sub-step
  name         text NOT NULL,
  kind         text NOT NULL DEFAULT 'trigger',    -- trigger|observe|verify|mutate|read|effect|compensate
  effect_class text NOT NULL DEFAULT 'observe',    -- observe|reversible|irreversible|external
  status       text NOT NULL DEFAULT 'pending',    -- pending|running|ok|failed|skipped|compensated|compensation_failed
  detail       text,
  structured   jsonb,                              -- typed payload, e.g. {"desired":3,"ready":1}
  error_code   text,                               -- machine-readable (vs free-form detail-as-error)
  started_at   timestamptz,
  ended_at     timestamptz,
  UNIQUE (action_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_action_steps_action ON cluster_action_steps (action_id, seq);
CREATE INDEX IF NOT EXISTS idx_action_steps_group  ON cluster_action_steps (group_id, started_at);

-- Append-only event spine. Global monotonic seq (bigserial) drives SSE resume
-- (Last-Event-ID / ?from=). cluster_id/group_id/action_id are plain uuids (no FK)
-- to keep the ingest hot path cheap; the CASCADE cleanup rides on the steps/
-- actions tables + a retention prune (mirrors the ReapActions housekeeping).
CREATE TABLE IF NOT EXISTS cluster_action_events (
  seq          bigserial PRIMARY KEY,
  cluster_id   uuid NOT NULL,
  group_id     uuid NOT NULL,
  action_id    uuid NOT NULL,
  step_seq     int,
  type         text NOT NULL,   -- step_start|step_progress|step_ok|step_fail|effect|compensate_start|compensate_ok|compensate_fail|group_start|group_done
  status       text,
  detail       text,
  payload      jsonb,
  at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_action_events_cluster_seq ON cluster_action_events (cluster_id, seq);
CREATE INDEX IF NOT EXISTS idx_action_events_group       ON cluster_action_events (group_id, seq);
