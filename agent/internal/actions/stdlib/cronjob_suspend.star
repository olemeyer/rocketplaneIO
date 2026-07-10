step("suspend")
k8s.patch(args["namespace"], "CronJob", args["name"], {"spec": {"suspend": True}})
