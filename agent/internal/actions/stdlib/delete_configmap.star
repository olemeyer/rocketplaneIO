# @name delete-configmap
# @summary Delete a ConfigMap; snapshot lets Revert re-create it verbatim.
# @risk medium
# @reversible snapshot
# @targets ConfigMap
#
# Snapshot + delete. The snapshot captures the full object so a Revert recreates
# the exact ConfigMap that existed before this run.
ns = args["namespace"]; name = args["name"]
step("delete %s" % name)
k8s.delete(ns, "ConfigMap", name)
report("deleted configmap %s — revert restores it from the snapshot" % name)
