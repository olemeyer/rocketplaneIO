# @name taint node
# @summary Add or replace a taint on a node; snapshot restores the prior taint set.
# @risk medium
# @reversible snapshot
# @targets Node
#
# The node is the target (args["name"]). Read its current taints, drop any existing
# entry with the same key, then append the requested taint. Node is cluster-scoped
# (namespace "-").
node = args["name"]; key = args["key"]
n = k8s.raw_get("v1", "Node", "-", node)
if not n:
    fail("node %s not found" % node)
taints = n.get("spec", {}).get("taints", []) or []
newlist = [t for t in taints if t.get("key") != key]
newlist.append({
    "key": key,
    "value": args.get("value", ""),
    "effect": args.get("effect", "NoSchedule"),
})
step("snapshot")
snapshot("-", "Node", node)
step("taint %s" % node)
k8s.patch("-", "Node", node, {"spec": {"taints": newlist}})
report("tainted %s with %s=%s:%s" % (node, key, args.get("value", ""), args.get("effect", "NoSchedule")))
