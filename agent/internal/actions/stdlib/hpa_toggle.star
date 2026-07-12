# @name hpa toggle
# @summary Freeze an HPA (pin min=max at the current size) or unfreeze it (restore the saved bounds).
# @risk low
# @reversible snapshot
# @targets HorizontalPodAutoscaler
#
# Freeze stashes the original min/max in an annotation, then pins min=max to the
# current replica count so the autoscaler stops moving. Unfreeze (enabled=true)
# reads that annotation and restores the saved bounds. Both directions snapshot the
# HPA, so a failure rolls back and the run stays revertible.
ns = args["namespace"]; name = args["name"]
ann = "rocketplane.io/hpa-frozen-bounds"
hpa = k8s.raw_get("autoscaling/v2", "HorizontalPodAutoscaler", ns, name)
if hpa == None:
    fail("HPA %s/%s not found" % (ns, name))
step("snapshot")
snapshot(ns, "HorizontalPodAutoscaler", name)
if args.get("enabled", "") != "true":
    spec = hpa.get("spec", {})
    lo = spec.get("minReplicas", 1)
    hi = spec.get("maxReplicas", 1)
    cur = hpa.get("status", {}).get("currentReplicas", lo)
    step("freeze at %d" % cur)
    # stash the original bounds so unfreeze can restore them, then pin min=max=cur
    k8s.patch(ns, "HorizontalPodAutoscaler", name, {
        "metadata": {"annotations": {ann: "%d,%d" % (lo, hi)}},
        "spec": {"minReplicas": cur, "maxReplicas": cur},
    })
    report("pinned HPA to %d replica(s) (saved bounds %d..%d)" % (cur, lo, hi))
else:
    saved = hpa.get("metadata", {}).get("annotations", {}).get(ann, "")
    if saved == "":
        fail("this HPA is not frozen by rocketplane (no saved bounds) — nothing to unfreeze")
    parts = saved.split(",")
    lo = int(parts[0])
    hi = int(parts[1])
    step("unfreeze to %d..%d" % (lo, hi))
    # restore the saved bounds and clear the stash annotation (merge-null removes it)
    k8s.patch(ns, "HorizontalPodAutoscaler", name, {
        "metadata": {"annotations": {ann: None}},
        "spec": {"minReplicas": lo, "maxReplicas": hi},
    })
    report("restored HPA bounds to %d..%d" % (lo, hi))
