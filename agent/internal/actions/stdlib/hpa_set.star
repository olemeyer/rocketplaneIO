# Snapshot the HPA, patch its bounds; rollback restores the prior min/max.
ns = args["namespace"]; name = args["name"]
spec = {}
if args.get("minReplicas", "") != "": spec["minReplicas"] = int(args["minReplicas"])
if args.get("maxReplicas", "") != "": spec["maxReplicas"] = int(args["maxReplicas"])
step("hpa bounds")
k8s.patch(ns, "HorizontalPodAutoscaler", name, {"spec": spec})
