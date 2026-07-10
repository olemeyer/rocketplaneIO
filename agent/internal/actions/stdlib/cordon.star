step("cordon")
k8s.patch("-", "Node", args["name"], {"spec": {"unschedulable": True}})
