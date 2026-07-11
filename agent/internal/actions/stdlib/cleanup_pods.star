# @name cleanup finished pods
# @summary Delete all Failed/Succeeded pods in a namespace — terminal pods, not reversible.
# @risk low
# @reversible none
# @targets Pod
#
# List pods and delete those in a terminal phase. These pods are already done,
# so there is nothing to restore.
ns = args["namespace"]
victims = [p for p in k8s.pods(ns) if p["phase"] in ("Failed", "Succeeded")]
report("found %d finished pod(s) to remove" % len(victims))
for p in victims:
    step("delete %s" % p["name"])
    k8s.delete(ns, "Pod", p["name"])
report("removed %d finished pod(s)" % len(victims))
