# @name cordon
# @summary Mark a node unschedulable; snapshot restores its prior schedulability.
# @risk low
# @reversible snapshot
# @targets Node
#
# Patch spec.unschedulable=true. Node is cluster-scoped, so the namespace is "-".
step("snapshot")
snapshot("-", "Node", args["name"])
step("cordon")
k8s.patch("-", "Node", args["name"], {"spec": {"unschedulable": True}})
report("cordoned %s" % args["name"])
