# @name debug-bundle
# @summary Collect readiness, pod status and warnings for a workload (read-only).
# @risk low
# @reversible readonly
# @targets Deployment,StatefulSet,DaemonSet
#
# Read-only triage snapshot: ready/desired count, per-pod status for pods whose name
# starts with the workload, and any Warning events. No mutation.
ns = args["namespace"]; kind = args["kind"]; name = args["name"]
step("workload status")
st = k8s.get(ns, kind, name)
if st:
    report("%d/%d ready" % (st["ready"], st["desired"]))
else:
    report("workload not found")
step("pods")
for p in k8s.pods(ns):
    if p["name"].startswith(name):
        report("%s ready=%s restarts=%d phase=%s" % (p["name"], p["ready"], p["restarts"], p["phase"]))
step("warnings")
warnings = [e for e in k8s.events(ns, name) if e["type"] == "Warning"]
report("%d warning(s)" % len(warnings))
for e in warnings:
    report("%s: %s" % (e.get("reason", ""), e.get("message", "")))
