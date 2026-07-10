step("resume")
k8s.patch(args["namespace"], "Deployment", args["name"], {"spec": {"paused": False}})
