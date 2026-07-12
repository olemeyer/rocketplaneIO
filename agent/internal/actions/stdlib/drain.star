# @name drain
# @summary Cordon a node and evict its pods (PDB-aware); revert re-schedules it (uncordon).
# @risk high
# @reversible snapshot
# @targets Node
#
# Cordon the node, then evict every drainable pod (DaemonSet/mirror pods stay) via
# the PDB-aware Eviction API, retrying pods a budget blocks. The whole-object Node
# snapshot makes the revert an uncordon; evicted pods are rescheduled by their
# owners and are not restored.
node = args["name"]
step("snapshot")
snapshot("-", "Node", node)
step("cordon")
k8s.patch("-", "Node", node, {"spec": {"unschedulable": True}})
report("cordoned %s" % node)
step("evict pods")
drained = False
for _round in range(120):
    pods = k8s.node_pods(node)
    if len(pods) == 0:
        drained = True
        break
    report("%d pod(s) still on the node" % len(pods))
    for p in pods:
        st = k8s.evict(p["namespace"], p["name"])
        if st == "blocked":
            report("%s/%s blocked by a PodDisruptionBudget — will retry" % (p["namespace"], p["name"]))
    sleep(3)
if not drained:
    fail("node did not drain within the step budget")
report("node drained (daemonsets remain)")
