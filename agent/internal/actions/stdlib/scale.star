# @name scale
# @summary Set replica count and wait until every pod is ready and settled — up or down.
# @risk medium
# @reversible snapshot
# @targets Deployment,StatefulSet
#
# Snapshot + patch spec.replicas; on failure the generic snapshot-rollback
# restores the prior count. Shown == executed; a fork is a byte-copy of this.
ns = args["namespace"]; kind = args["kind"]; name = args["name"]
target = int(args["replicas"])
step("snapshot")
snapshot(ns, kind, name)
step("scale to %d" % target)
k8s.patch(ns, kind, name, {"spec": {"replicas": target}})
step("verify")
if not wait_ready(ns, kind, name, timeout=300):
    fail("workload did not become ready — rolling back to the prior count")
report("settled at %d replicas" % target)
