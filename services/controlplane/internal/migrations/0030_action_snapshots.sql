-- Safe Actions (snapshot substrate) · durable snapshot list for generic rollback.
--
-- The v4 durable-revert stored a SINGLE inverse spec (cluster_actions.revert,
-- scalar) — so a multi-mutation crash could replay only the last write. The
-- snapshot substrate captures the MINIMAL restorable delta of every mutation a
-- run makes (whole object OR field-scoped), as an ORDERED list. This column
-- holds that list so a crashed run can be rolled back in reverse by a restarted
-- or peer agent, and so the manual "revert later" replays the same list.
--
-- A jsonb ARRAY (not a scalar) is the ordered list; the agent appends to it as
-- each mutation commits (snapshots = snapshots || <new>), so the undo state is
-- durable the instant a mutation happens — not only at completion.
ALTER TABLE cluster_actions
  ADD COLUMN IF NOT EXISTS snapshots jsonb NOT NULL DEFAULT '[]'::jsonb;
