# Built-in: scale a workload. Snapshot + patch spec.replicas; a snapshot-based
# rollback restores the prior count. Shown == executed; a fork is a copy of this.
ns = args["namespace"]; kind = args["kind"]; name = args["name"]
step("scale")
k8s.patch(ns, kind, name, {"spec": {"replicas": int(args["replicas"])}})
step("verify")
if not wait_ready(ns, kind, name, timeout=300):
    fail("workload did not become ready")
