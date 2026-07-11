# @name describe resource
# @summary Summarize an object's status conditions and recent events (read-only).
# @risk low
# @reversible readonly
# @targets Deployment,StatefulSet,DaemonSet,Pod,Service,Ingress,PodDisruptionBudget,HorizontalPodAutoscaler,CronJob,Job,PersistentVolumeClaim,Node,Namespace
#
# Read-only: no snapshot, no mutation. Prints each status condition then the most
# recent events on the object. Pass apiVersion for non-core kinds.
kind = args["kind"]; ns = args.get("namespace", ""); name = args["name"]
step("describe %s/%s" % (kind, name))
obj = k8s.raw_get(args.get("apiVersion", "v1"), kind, ns, name)
if not obj:
    fail("%s/%s not found" % (kind, name))
conds = obj.get("status", {}).get("conditions", [])
report("%d condition(s)" % len(conds))
for c in conds:
    report("%s=%s %s" % (c.get("type", ""), c.get("status", ""), c.get("message", "")))
step("events")
for e in k8s.events(ns, name):
    report("[%s] %s: %s" % (e.get("type", ""), e.get("reason", ""), e.get("message", "")))
