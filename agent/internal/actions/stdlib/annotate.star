# @name annotate object
# @summary Set or remove one metadata annotation (pause an operator, tag for triage).
# @risk low
# @reversible snapshot
# @targets Deployment,StatefulSet,DaemonSet,Pod,ConfigMap,Service,PersistentVolumeClaim,CronJob,HorizontalPodAutoscaler,Namespace,Node
#
# Field-scoped: rollback removes an added key (or restores a changed one) WITHOUT
# touching sibling annotations. remove=true sets the key to null (delete).
ns = args.get("namespace", ""); kind = args["kind"]; name = args["name"]
path = ["metadata", "annotations", args["key"]]
step("annotate")
if args.get("remove", "") == "true":
    k8s.set_field(ns, kind, name, path, None)
    report("removed annotation %s" % args["key"])
else:
    k8s.set_field(ns, kind, name, path, args.get("value", ""))
    report("%s=%s" % (args["key"], args.get("value", "")))
