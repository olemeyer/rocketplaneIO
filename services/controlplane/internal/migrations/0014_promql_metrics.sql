-- Custom-Metriken können jetzt direkt als PromQL-Ausdruck definiert werden
-- (source='promql', query=Ausdruck) — das Recording-Rule-Muster.
ALTER TABLE metric_definitions ADD COLUMN IF NOT EXISTS query text NOT NULL DEFAULT '';
