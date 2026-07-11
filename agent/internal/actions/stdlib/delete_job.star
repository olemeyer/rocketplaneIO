# @name delete job
# @summary Delete a single Job; the snapshot lets Revert recreate it.
# @risk medium
# @reversible snapshot
# @targets Job
#
# Snapshot + delete a named Job. The snapshot records the full object, so Revert
# recreates it exactly.
ns = args["namespace"]; name = args["name"]
step("delete %s" % name)
k8s.delete(ns, "Job", name)
report("deleted job %s/%s" % (ns, name))
