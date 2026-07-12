# @name cronjob-trigger
# @summary Create a one-off Job from a CronJob's template — a manual run, not reversible.
# @risk medium
# @reversible none
# @targets CronJob
#
# Read the CronJob and materialise its jobTemplate into a standalone Job. This is
# a fire-and-forget manual run; there is nothing meaningful to restore.
ns = args["namespace"]; name = args["name"]
cj = k8s.get("batch/v1", "CronJob", ns, name)
if not cj:
    fail("cronjob %s/%s not found" % (ns, name))
jt = cj["spec"]["jobTemplate"]
jobname = name + "-manual"
if k8s.get("batch/v1", "Job", ns, jobname) != None:
    fail("a manual job %s already exists — delete it before triggering again" % jobname)
step("create job %s" % jobname)
k8s.create({
    "apiVersion": "batch/v1",
    "kind": "Job",
    "metadata": {"name": jobname, "namespace": ns},
    "spec": jt.get("spec", {}),
})
report("created one-off job %s from cronjob %s" % (jobname, name))
