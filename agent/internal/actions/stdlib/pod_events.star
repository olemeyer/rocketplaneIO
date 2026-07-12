# @name pod-events
# @summary Show events for a workload's pods by name match (read-only).
# @risk low
# @reversible readonly
# @targets Deployment,StatefulSet,DaemonSet
#
# Read-only: filters the namespace event stream to objects whose name contains the
# workload name, surfacing per-pod events without any mutation.
ns = args["namespace"]; name = args["name"]
step("events for %s" % name)
evs = k8s.events(ns, "")
hits = [e for e in evs if name in e["object"]]
report("%d event(s) for %s" % (len(hits), name))
for e in hits:
    report("[%s] %s: %s" % (e.get("type", ""), e.get("reason", ""), e.get("message", "")))
