-- Actions sind ABLÄUFE, keine Einzelbefehle: der Agent meldet während der
-- Ausführung Fortschritt (progress) und den Zustand der einzelnen Schritte
-- (steps: [{name,status,detail}]) — von „triggered" über „rollout 1/3" bis
-- zum verifizierten Erfolg. Fundament für künftige mehrstufige Runbooks.
ALTER TABLE cluster_actions
  ADD COLUMN IF NOT EXISTS progress text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS steps jsonb NOT NULL DEFAULT '[]';
