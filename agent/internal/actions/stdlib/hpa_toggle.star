# @name hpa toggle
# @summary Freeze an HPA by pinning min=max to the current replica count; unfreeze via revert.
# @risk low
# @reversible snapshot
# @targets HorizontalPodAutoscaler
#
# Freeze pins minReplicas and maxReplicas to the current replica count so the
# autoscaler stops moving. The snapshot restores the original bounds — unfreeze
# is a revert of the freeze action.
ns = args["namespace"]; name = args["name"]
if args.get("enabled", "") != "true":
    step("freeze")
    hpa = k8s.raw_get("autoscaling/v2", "HorizontalPodAutoscaler", ns, name)
    if hpa == None:
        fail("HPA not found")
    cur = hpa.get("status", {}).get("currentReplicas", hpa.get("spec", {}).get("minReplicas", 1))
    k8s.patch(ns, "HorizontalPodAutoscaler", name, {"spec": {"minReplicas": cur, "maxReplicas": cur}})
    report("pinned HPA to %d replicas" % cur)
else:
    report("unfreeze by reverting the freeze action — the snapshot restores the original bounds")
