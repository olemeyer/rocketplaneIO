-- Incident management: first-class incidents that tie alerts, Copilot
-- investigations, and safe actions together into ONE lifecycle. An incident is
-- the wrapper around an event: it is declared (manually OR automatically when an
-- alert fires), moves through open → acknowledged → mitigated → resolved, and
-- carries a chronological timeline of all events.
-- MTTA (created→acknowledged) and MTTR (created→resolved) are derived from it.
--
-- Auto-glue: the alert evaluator declares an incident on the transition to
-- `firing` (deduplicated via dedup_key = "alert:<ruleId>" while still open) and
-- closes it again when it falls back to `ok`. Copilot investigations and
-- actions reference it back via incident_id and show up in the timeline.

CREATE TABLE IF NOT EXISTS incidents (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  cluster_id    uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  number        int  NOT NULL,                       -- sequential per org (INC-<number>)
  title         text NOT NULL,
  summary       text NOT NULL DEFAULT '',
  severity      text NOT NULL DEFAULT 'high',        -- critical | high | medium | low
  status        text NOT NULL DEFAULT 'open',        -- open | acknowledged | mitigated | resolved
  source        text NOT NULL DEFAULT 'manual',      -- manual | alert | copilot
  dedup_key     text,                                -- auto-dedup of open alert incidents
  assignee_id   uuid REFERENCES users(id) ON DELETE SET NULL,
  created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
  acknowledged_at timestamptz,
  acknowledged_by uuid REFERENCES users(id) ON DELETE SET NULL,
  mitigated_at  timestamptz,
  resolved_at   timestamptz,
  resolved_by   uuid REFERENCES users(id) ON DELETE SET NULL,
  postmortem    text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_incidents_cluster ON incidents(cluster_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_incidents_org ON incidents(org_id, created_at DESC);
-- At most ONE open auto-incident per dedup_key (alert rule): re-firing attaches
-- to it instead of duplicating.
CREATE UNIQUE INDEX IF NOT EXISTS idx_incidents_open_dedup
  ON incidents(dedup_key) WHERE status <> 'resolved' AND dedup_key IS NOT NULL;

-- Per-org sequential counter (atomic via UPSERT-RETURNING) — produces the
-- human-readable INC number without a race.
CREATE TABLE IF NOT EXISTS incident_counters (
  org_id uuid PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
  seq    int  NOT NULL DEFAULT 0
);

-- Timeline: each row is one event on the incident. ref_type/ref_id link to the
-- triggering object (alert_event | investigation | action), metadata carries
-- context (old/new status, value, …).
CREATE TABLE IF NOT EXISTS incident_events (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  incident_id uuid NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  at          timestamptz NOT NULL DEFAULT now(),
  kind        text NOT NULL,        -- declared|status|severity|assigned|note|alert|alert_cleared|investigation|action|postmortem
  actor_id    uuid REFERENCES users(id) ON DELETE SET NULL,
  actor_email text NOT NULL DEFAULT '',
  message     text NOT NULL DEFAULT '',
  ref_type    text NOT NULL DEFAULT '',
  ref_id      uuid,
  metadata    jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_incident_events_inc ON incident_events(incident_id, at);

-- Back-references: alert transitions, Copilot investigations, and actions carry
-- the incident_id so the timeline can collect them (SET NULL: the incident can
-- be deleted/archived without losing the source).
ALTER TABLE alert_events
  ADD COLUMN IF NOT EXISTS incident_id uuid REFERENCES incidents(id) ON DELETE SET NULL;
ALTER TABLE copilot_investigations
  ADD COLUMN IF NOT EXISTS incident_id uuid REFERENCES incidents(id) ON DELETE SET NULL;
ALTER TABLE cluster_actions
  ADD COLUMN IF NOT EXISTS incident_id uuid REFERENCES incidents(id) ON DELETE SET NULL;
