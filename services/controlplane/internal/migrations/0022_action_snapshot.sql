-- Generischer Before-Snapshot je Mutation: das Zielobjekt (managedFields/status
-- gestrippt) als JSON, VOR der Änderung gesnapshottet. Grundlage für
-- restore_resource-Reverts und den Audit-Trail ("was stand da vorher wirklich").
ALTER TABLE cluster_actions ADD COLUMN IF NOT EXISTS snapshot jsonb;
