-- Incident-Management: erstklassige Incidents, die Alerts, Copilot-
-- Investigations und Safe-Actions zu EINEM Lebenszyklus verbinden. Ein Incident
-- ist die Klammer über einen Vorfall: er wird deklariert (manuell ODER
-- automatisch beim Feuern eines Alerts), durchläuft open → acknowledged →
-- mitigated → resolved und trägt eine chronologische Timeline aller Ereignisse.
-- MTTA (created→acknowledged) und MTTR (created→resolved) sind daraus ableitbar.
--
-- Auto-Glue: der Alert-Evaluator deklariert beim Übergang nach `firing` einen
-- Incident (dedupliziert über dedup_key = "alert:<ruleId>", solange offen) und
-- schließt ihn beim Zurückfallen nach `ok` wieder. Copilot-Investigations und
-- Actions verweisen per incident_id zurück und tauchen in der Timeline auf.

CREATE TABLE IF NOT EXISTS incidents (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  cluster_id    uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  number        int  NOT NULL,                       -- pro Org fortlaufend (INC-<number>)
  title         text NOT NULL,
  summary       text NOT NULL DEFAULT '',
  severity      text NOT NULL DEFAULT 'high',        -- critical | high | medium | low
  status        text NOT NULL DEFAULT 'open',        -- open | acknowledged | mitigated | resolved
  source        text NOT NULL DEFAULT 'manual',      -- manual | alert | copilot
  dedup_key     text,                                -- Auto-Dedup offener Alert-Incidents
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
-- Höchstens EIN offener Auto-Incident je dedup_key (Alert-Regel): erneutes
-- Feuern hängt sich an, statt zu duplizieren.
CREATE UNIQUE INDEX IF NOT EXISTS idx_incidents_open_dedup
  ON incidents(dedup_key) WHERE status <> 'resolved' AND dedup_key IS NOT NULL;

-- Pro-Org fortlaufender Zähler (atomar via UPSERT-RETURNING) — ergibt die
-- menschenlesbare INC-Nummer ohne Race.
CREATE TABLE IF NOT EXISTS incident_counters (
  org_id uuid PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
  seq    int  NOT NULL DEFAULT 0
);

-- Timeline: jede Zeile ein Ereignis am Incident. ref_type/ref_id verlinken auf
-- das auslösende Objekt (alert_event | investigation | action), metadata trägt
-- Kontext (alter/neuer Status, Wert, …).
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

-- Rückverweise: Alert-Übergänge, Copilot-Investigations und Actions tragen die
-- incident_id, damit die Timeline sie einsammeln kann (SET NULL: der Incident
-- kann gelöscht/archiviert werden, ohne die Quelle zu verlieren).
ALTER TABLE alert_events
  ADD COLUMN IF NOT EXISTS incident_id uuid REFERENCES incidents(id) ON DELETE SET NULL;
ALTER TABLE copilot_investigations
  ADD COLUMN IF NOT EXISTS incident_id uuid REFERENCES incidents(id) ON DELETE SET NULL;
ALTER TABLE cluster_actions
  ADD COLUMN IF NOT EXISTS incident_id uuid REFERENCES incidents(id) ON DELETE SET NULL;
