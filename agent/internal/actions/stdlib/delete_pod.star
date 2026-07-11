# @name recreate pod
# @summary Delete one pod; its owner schedules a fresh replacement.
# @risk medium
# @reversible none
# @targets Pod
#
# The pod is the target (args["name"]). Snapshot + delete; the owning controller
# recreates it, so there is nothing to restore — the snapshot lets Revert re-create
# the exact pod if it was orphaned.
ns = args["namespace"]; pod = args["name"]
step("delete %s" % pod)
k8s.delete(ns, "Pod", pod)
report("pod deleted — the owning controller will recreate it")
