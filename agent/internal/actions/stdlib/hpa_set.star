# @name hpa set bounds
# @summary Patch an HPA's min/max replica bounds; rollback restores the prior bounds.
# @risk low
# @reversible snapshot
# @targets HorizontalPodAutoscaler
#
# Snapshot the HPA, patch its bounds; rollback restores the prior min/max.
ns = args["namespace"]; name = args["name"]
spec = {}
if args.get("minReplicas", "") != "": spec["minReplicas"] = int(args["minReplicas"])
if args.get("maxReplicas", "") != "": spec["maxReplicas"] = int(args["maxReplicas"])
step("snapshot")
snapshot(ns, "HorizontalPodAutoscaler", name)
step("hpa bounds")
k8s.patch(ns, "HorizontalPodAutoscaler", name, {"spec": spec})
