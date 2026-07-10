step("pause")
k8s.patch(args["namespace"], "Deployment", args["name"], {"spec": {"paused": True}})
