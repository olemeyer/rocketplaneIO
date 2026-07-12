# @name rollout-restart
# @summary Force a fresh rollout by bumping a counter annotation on the pod template.
# @risk low
# @reversible none
# @targets Deployment,StatefulSet,DaemonSet
#
# Increment rocketplane.io/restartedAt on the pod template so every pod is
# recreated. Not reversible: restarting pods cannot be un-restarted.
ns = args["namespace"]; kind = args["kind"]; name = args["name"]
step("read current restart counter")
obj = k8s.raw_get("apps/v1", kind, ns, name)
if obj == None:
    fail("workload not found")
cur = int(obj["spec"]["template"]["metadata"].get("annotations", {}).get("rocketplane.io/restartedAt", "0"))
step("bump restart annotation")
k8s.patch(ns, kind, name,
    {"spec": {"template": {"metadata": {"annotations": {"rocketplane.io/restartedAt": str(cur + 1)}}}}})
step("verify")
if not wait_rollout(ns, kind, name, timeout=300):
    fail("restart rollout did not settle")
report("restarted (counter now %d)" % (cur + 1))
