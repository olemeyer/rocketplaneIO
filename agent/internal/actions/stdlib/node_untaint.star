# @name untaint node
# @summary Remove a taint by key from a node; snapshot restores it on revert.
# @risk medium
# @reversible snapshot
# @targets Node
#
# Read the node's taints and drop every entry with the given key. Node is
# cluster-scoped, so the namespace is "-".
node = args["node"]; key = args["key"]
n = k8s.raw_get("v1", "Node", "-", node)
if not n:
    fail("node %s not found" % node)
taints = n.get("spec", {}).get("taints", []) or []
newlist = [t for t in taints if t.get("key") != key]
step("untaint %s" % node)
k8s.patch("-", "Node", node, {"spec": {"taints": newlist}})
report("removed taint %s from %s" % (key, node))
