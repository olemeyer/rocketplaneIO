# @name restore-resource
# @summary Re-apply a prior before-snapshot of one object (server-side apply); snapshot makes it revertible.
# @risk medium
# @reversible snapshot
# @targets Service,Ingress,NetworkPolicy,PodDisruptionBudget,ConfigMap,ResourceQuota,LimitRange
#
# Re-apply the before-object captured by an earlier action as the desired truth
# (server-side apply, force). The identity in the snapshot must match the target —
# no object smuggling. The whole-object capture makes even this re-apply revertible.
ns = args["namespace"]; kind = args["kind"]; name = args["name"]
snap = json.decode(args["snapshot"])
if snap.get("kind", kind) != kind:
    fail("snapshot kind does not match the target")
if snap.get("metadata", {}).get("name", name) != name:
    fail("snapshot name does not match the target")
step("snapshot")
snapshot(ns, kind, name)
step("re-apply snapshot")
k8s.apply(snap)
report("snapshot re-applied to %s/%s" % (kind, name))
