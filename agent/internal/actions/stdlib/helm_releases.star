# @name helm releases
# @summary List Helm releases in a namespace from their release Secrets (read-only).
# @risk low
# @reversible readonly
# @targets Secret
#
# Read-only: Helm v3 stores each release as a Secret labelled owner=helm. This reads
# the release name, revision and status from those labels without touching anything.
ns = args.get("namespace", "")
step("list helm releases")
secs = k8s.raw_list("v1", "Secret", ns, selector="owner=helm")
report("%d release secret(s)" % len(secs))
for s in secs:
    lb = s["metadata"].get("labels", {})
    report("%s rev=%s status=%s" % (lb.get("name"), lb.get("version"), lb.get("status")))
