# @name uncordon node
# @summary Mark a node schedulable again; snapshot restores its prior state.
# @risk low
# @reversible snapshot
# @targets Node
#
# Patch spec.unschedulable=false. Node is cluster-scoped, so the namespace is "-".
step("uncordon")
k8s.patch("-", "Node", args["name"], {"spec": {"unschedulable": False}})
report("uncordoned %s" % args["name"])
