-- Investigation-Graph des Copilot-Orchestrators: Der Master-Agent delegiert
-- jede Hypothese an einen Investigator-Subagenten; Hypothese→Verdict ist ein
-- KNOTEN. Branching = neuer Knoten mit parent_id auf einen älteren Knoten.
-- Nur die Verdicts fließen zurück in den Master-Kontext — der Graph ist die
-- persistente Wahrheit für Resume, UI-Baum und Audit.
CREATE TABLE IF NOT EXISTS copilot_investigations (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  chat_id    uuid NOT NULL REFERENCES copilot_chats(id) ON DELETE CASCADE,
  cluster_id uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  status     text NOT NULL DEFAULT 'active', -- active | concluded | abandoned
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_copilot_inv_chat ON copilot_investigations(chat_id);

CREATE TABLE IF NOT EXISTS copilot_investigation_nodes (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  investigation_id uuid NOT NULL REFERENCES copilot_investigations(id) ON DELETE CASCADE,
  parent_id        uuid REFERENCES copilot_investigation_nodes(id) ON DELETE SET NULL,
  seq              int  NOT NULL,               -- globale Reihenfolge im Graph
  kind             text NOT NULL,               -- hypothesis | action | question | conclusion
  hypothesis       text NOT NULL DEFAULT '',
  task             jsonb NOT NULL DEFAULT '{}'::jsonb, -- Task-JSON des Masters
  verdict          jsonb,                       -- write_verdict-Payload / Action-Result / Antwort-Ref
  status           text NOT NULL DEFAULT 'pending', -- pending|running|done|failed|abandoned
  confidence       real,
  tokens_in        int NOT NULL DEFAULT 0,
  tokens_out       int NOT NULL DEFAULT 0,
  created_at       timestamptz NOT NULL DEFAULT now(),
  finished_at      timestamptz
);
CREATE INDEX IF NOT EXISTS idx_copilot_nodes_inv ON copilot_investigation_nodes(investigation_id, seq);
