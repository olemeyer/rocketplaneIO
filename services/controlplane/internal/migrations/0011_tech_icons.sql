-- Technologie-Erkennung auf der Map: das Container-Image je Pod (Quelle der
-- Auto-Erkennung) + ein manuell wählbares Icon je Workload (überschreibt).
ALTER TABLE pods ADD COLUMN IF NOT EXISTS image text NOT NULL DEFAULT '';
ALTER TABLE workloads ADD COLUMN IF NOT EXISTS icon text NOT NULL DEFAULT '';
