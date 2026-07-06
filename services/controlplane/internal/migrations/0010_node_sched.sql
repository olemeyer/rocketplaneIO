-- Node-Wartung: unschedulable (cordoned) für die Nodes-Seite + Actions.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS unschedulable boolean NOT NULL DEFAULT false;
