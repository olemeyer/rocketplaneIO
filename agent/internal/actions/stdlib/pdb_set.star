# @name set pdb budget
# @summary Set a PodDisruptionBudget minAvailable or maxUnavailable; snapshot restores.
# @risk medium
# @reversible snapshot
# @targets PodDisruptionBudget
#
# minAvailable and maxUnavailable are mutually exclusive, so setting one clears the
# other IN ONE atomic patch (an intermediate with both set would be rejected). The
# field-scoped snapshot restores the prior pair exactly — removing the key this run
# added and restoring the one it cleared. Value may be an integer or a percentage.
ns = args["namespace"]; name = args["name"]
if args.get("maxUnavailable", "") != "":
    raw = args["maxUnavailable"]
    val = raw if raw.endswith("%") else int(raw)
    step("set maxUnavailable=%s" % raw)
    k8s.set_fields(ns, "PodDisruptionBudget", name, [
        (["spec", "maxUnavailable"], val),
        (["spec", "minAvailable"], None),
    ])
    report("maxUnavailable set to %s on %s" % (raw, name))
else:
    raw = args.get("minAvailable", "1")
    val = raw if raw.endswith("%") else int(raw)
    step("set minAvailable=%s" % raw)
    k8s.set_fields(ns, "PodDisruptionBudget", name, [
        (["spec", "minAvailable"], val),
        (["spec", "maxUnavailable"], None),
    ])
    report("minAvailable set to %s on %s" % (raw, name))
