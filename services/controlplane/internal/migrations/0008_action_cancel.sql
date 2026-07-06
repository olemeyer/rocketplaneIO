-- Actions sind CANCELABLE + TIMEOUTABLE:
--   cancel_requested — vom User gesetzt; der Agent sieht es in der Antwort
--   auf seine Progress-Reports (outbound-only bleibt gewahrt) und rollt den
--   Undo-Stack der Engine automatisch zurück → Endstatus 'cancelled'.
--   timeout_seconds — Ablauf-Budget je Definition (Engine clamped 30..1800).
ALTER TABLE cluster_actions
  ADD COLUMN IF NOT EXISTS cancel_requested boolean NOT NULL DEFAULT false;
ALTER TABLE action_definitions
  ADD COLUMN IF NOT EXISTS timeout_seconds int NOT NULL DEFAULT 600;
