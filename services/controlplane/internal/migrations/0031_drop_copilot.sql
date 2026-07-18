-- Copilot removal — the built-in LLM orchestrator is replaced by the external
-- MCP interface (transactions with snapshot rollback, see 0032). This drops the
-- copilot tables and detaches action grouping from chats/investigations.
-- NOTE: irreversible. Take a pg_dump of copilot_chats/copilot_investigations/
-- copilot_investigation_nodes before upgrading if the history matters.

-- Detach cluster_actions/action_groups from the copilot tables first so the
-- table drops below don't cascade into action history.
ALTER TABLE cluster_actions DROP COLUMN IF EXISTS investigation_node_id;

DROP INDEX IF EXISTS uq_action_groups_turn;
DROP INDEX IF EXISTS idx_action_groups_investigation;
ALTER TABLE action_groups
  DROP COLUMN IF EXISTS chat_id,
  DROP COLUMN IF EXISTS investigation_id,
  DROP COLUMN IF EXISTS turn_id;

-- Re-home historical copilot groups so the Runs view keeps rendering them.
UPDATE action_groups SET origin = 'manual_batch'
  WHERE origin IN ('copilot_turn', 'investigation');
ALTER TABLE action_groups DROP CONSTRAINT IF EXISTS action_groups_origin_check;
ALTER TABLE action_groups ADD CONSTRAINT action_groups_origin_check
  CHECK (origin IN ('manual_batch', 'manual_single', 'alert_remediation', 'schedule', 'mcp'));

DROP TABLE IF EXISTS copilot_investigation_nodes;
DROP TABLE IF EXISTS copilot_investigations;
DROP TABLE IF EXISTS copilot_chats;
