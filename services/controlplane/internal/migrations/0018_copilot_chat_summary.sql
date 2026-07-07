-- Kurzbeschreibung je Copilot-Vorgang (vom Agenten via set_title gepflegt),
-- damit die Seitenleiste Titel + eine Zeile Kontext zeigt.
ALTER TABLE copilot_chats ADD COLUMN IF NOT EXISTS summary text NOT NULL DEFAULT '';
