# @name evict-pod
# @summary Gracefully evict one pod via the PDB-aware Eviction API; its owner reschedules it.
# @risk medium
# @reversible none
# @targets Pod
#
# The Eviction API respects PodDisruptionBudgets. Not reversible: the replacement
# is a fresh pod, so there is no prior state to restore. A PDB block fails the action.
ns = args["namespace"]; pod = args["name"]
grace = args.get("gracePeriodSeconds", "")
step("evict %s" % pod)
status = k8s.evict(ns, pod, grace_seconds=int(grace)) if grace != "" else k8s.evict(ns, pod)
if status == "blocked":
    fail("eviction blocked by a PodDisruptionBudget")
if status == "notfound":
    report("pod already gone")
else:
    report("eviction requested — the owning controller will reschedule it")
