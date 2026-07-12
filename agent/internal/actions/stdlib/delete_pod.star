# @name delete-pod
# @summary Delete one pod; its owner schedules a fresh replacement.
# @risk medium
# @reversible none
# @targets Pod
#
# The pod is the target (args["name"]). Delete it; the owning controller schedules
# a fresh replacement. Not reversible — the replacement is a new pod, so there is no
# prior state to restore (no snapshot is kept and no revert is offered).
ns = args["namespace"]; pod = args["name"]
step("delete %s" % pod)
k8s.delete(ns, "Pod", pod)
report("pod deleted — the owning controller will recreate it")
