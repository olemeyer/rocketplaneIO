-- Revert-Spec je Action: die INVERSE Katalog-Action mit den VOR der Mutation
-- gesnapshotteten Werten (z.B. scale→prevReplicas, set_image→prevImage). Vom
-- Agenten bei succeeded mitgeliefert; die Runs-Seite bietet daraus "Revert" an.
ALTER TABLE cluster_actions ADD COLUMN IF NOT EXISTS revert jsonb;
