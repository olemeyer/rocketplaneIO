# @name get resource
# @summary Read the full live spec of any object (Secrets show keys only).
# @risk low
# @reversible readonly
# @targets ConfigMap,Deployment,StatefulSet,DaemonSet,Pod,Secret,Service,Ingress,NetworkPolicy,PodDisruptionBudget,HorizontalPodAutoscaler,CronJob,Job,PersistentVolumeClaim,Node,Namespace,ServiceAccount,ResourceQuota,LimitRange
#
# Read-only: no snapshot, no mutation. Pass apiVersion for CRDs.
kind = args["kind"]; ns = args.get("namespace", ""); name = args["name"]
step("read %s/%s" % (kind, name))
obj = k8s.raw_get(args.get("apiVersion", "v1"), kind, ns, name)
if not obj:
    fail("%s/%s not found" % (kind, name))
report(str(obj.get("spec", obj.get("data", obj))))
