# @name set pdb budget
# @summary Set a PodDisruptionBudget minAvailable or maxUnavailable; snapshot restores.
# @risk medium
# @reversible snapshot
# @targets PodDisruptionBudget
#
# minAvailable and maxUnavailable are mutually exclusive, so setting one clears the
# other. Value may be an integer or a percentage string like "20%".
ns = args["namespace"]; name = args["name"]
raw = args["value"]
val = raw if raw.endswith("%") else int(raw)
if args["mode"] == "minAvailable":
    step("set minAvailable=%s" % raw)
    k8s.patch(ns, "PodDisruptionBudget", name, {"spec": {"minAvailable": val, "maxUnavailable": None}})
    report("minAvailable set to %s on %s" % (raw, name))
else:
    step("set maxUnavailable=%s" % raw)
    k8s.patch(ns, "PodDisruptionBudget", name, {"spec": {"maxUnavailable": val, "minAvailable": None}})
    report("maxUnavailable set to %s on %s" % (raw, name))
