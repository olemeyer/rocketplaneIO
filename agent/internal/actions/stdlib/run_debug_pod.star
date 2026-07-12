# @name run-debug-pod
# @summary Run an ephemeral, unprivileged probe pod (image + argv); logs are the result; always cleaned up.
# @risk medium
# @reversible none
# @targets Namespace
#
# Image-bisect, PVC inspection or a node-pinned probe. The pod carries no service
# account token and no privilege escalation — a sonde, not a foothold. It runs to
# exit/timeout, its logs are returned, and it is ALWAYS deleted. Not reversible.
ns = args["namespace"]
name = "rp-debug-" + args["id"][:8]
cmd = json.decode(args["command"])
spec = {
    "restartPolicy": "Never",
    "automountServiceAccountToken": False,
    "tolerations": [{"operator": "Exists"}],
    "containers": [{
        "name": "probe",
        "image": args["image"],
        "command": cmd,
        "resources": {"limits": {"cpu": args.get("cpu", "500m"), "memory": args.get("memory", "512Mi")}},
        "securityContext": {"allowPrivilegeEscalation": False},
    }],
}
node = args.get("nodeName", "")
if node != "":
    spec["nodeName"] = node
pvc = args.get("pvcName", "")
if pvc != "":
    spec["volumes"] = [{"name": "pvc", "persistentVolumeClaim": {"claimName": pvc, "readOnly": True}}]
    spec["containers"][0]["volumeMounts"] = [{"name": "pvc", "mountPath": "/pvc", "readOnly": True}]
pod = {
    "apiVersion": "v1",
    "kind": "Pod",
    "metadata": {"name": name, "namespace": ns, "labels": {"rocketplane.io/debug-pod": "true"}},
    "spec": spec,
}

step("create probe pod %s" % name)
k8s.delete(ns, "Pod", name)  # clear any leftover from a previous run of this action
k8s.create(pod)
step("run")
result = ""
for _round in range(120):
    st = k8s.pod_status(ns, name)
    if st == None:
        break
    if st["terminated"]:
        result = "exit %d (%s)" % (st["exit_code"], st["reason"])
        break
    stuck = st["stuck"]
    if stuck != "" and "CrashLoopBackOff" not in stuck:
        k8s.delete(ns, "Pod", name)
        fail("probe cannot start — %s" % stuck)
    report(st["phase"])
    sleep(2)
step("logs")
logs = k8s.logs(ns, name, tail=200)
step("cleanup")
k8s.delete(ns, "Pod", name)
if result == "":
    fail("probe did not finish before the step budget — raise timeoutSeconds for longer probes")
report("probe finished: %s\n%s" % (result, logs))
