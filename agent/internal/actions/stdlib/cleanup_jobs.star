# @name cleanup finished jobs
# @summary Delete completed or failed Jobs in a namespace — terminal jobs, not reversible.
# @risk low
# @reversible none
# @targets Job
#
# List jobs and delete those that have already succeeded or failed. These jobs are
# terminal, so there is nothing to restore.
ns = args["namespace"]
deleted = 0
for j in k8s.raw_list("batch/v1", "Job", ns):
    st = j.get("status", {})
    if st.get("succeeded", 0) > 0 or st.get("failed", 0) > 0:
        name = j["metadata"]["name"]
        step("delete %s" % name)
        k8s.delete(ns, "Job", name)
        deleted = deleted + 1
report("removed %d finished job(s)" % deleted)
