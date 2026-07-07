-- Gespeicherte Copilot-Vorgänge (Chats): jede Investigation/Konversation wird
-- persistiert, damit man sie wieder aufrufen, fortsetzen und zwischen ihnen hin-
-- und herwechseln kann. Pro (Cluster, User). Der komplette Render-Zustand (Chat-
-- Verlauf + Tool-Aktivitäten fürs Inspector-Panel) liegt als JSONB — 1:1 wieder
-- herstellbar, ohne die Streaming-Architektur zu berühren.
CREATE TABLE IF NOT EXISTS copilot_chats (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title      text NOT NULL DEFAULT '',
  data       jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_copilot_chats_owner ON copilot_chats(cluster_id, user_id, updated_at DESC);
