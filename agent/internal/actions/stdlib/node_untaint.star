# @name node-untaint
# @summary Remove a taint by key from a node; snapshot restores it on revert.
# @risk medium
# @reversible snapshot
# @targets Node
#
# The node is the target (args["name"]). Read its taints and drop every entry with
# the given key. Node is cluster-scoped (namespace "-").
node = args["name"]; key = args["key"]
n = k8s.get("v1", "Node", "-", node)
if not n:
    fail("node %s not found" % node)
taints = n.get("spec", {}).get("taints", []) or []
newlist = [t for t in taints if t.get("key") != key]
step("snapshot")
snapshot("-", "Node", node)
step("untaint %s" % node)
k8s.patch("-", "Node", node, {"spec": {"taints": newlist}})
report("removed taint %s from %s" % (key, node))
