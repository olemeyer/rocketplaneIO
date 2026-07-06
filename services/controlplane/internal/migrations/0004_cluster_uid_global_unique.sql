-- Die UID des kube-system-Namespace ist die GLOBALE Identität eines physischen
-- Clusters. Bisher war nur (org_id, k8s_uid) eindeutig — dadurch konnte sich
-- derselbe Cluster in zwei Orgs (oder zweimal in einer) einklinken, was zu
-- gespaltenen Topologie-/Flow-Daten führt. Ersetzt den Constraint durch einen
-- GLOBAL eindeutigen partiellen Index (pending-Cluster ohne UID bleiben erlaubt).

ALTER TABLE clusters DROP CONSTRAINT IF EXISTS clusters_org_id_k8s_uid_key;

CREATE UNIQUE INDEX IF NOT EXISTS clusters_k8s_uid_key
  ON clusters (k8s_uid)
  WHERE k8s_uid IS NOT NULL;
