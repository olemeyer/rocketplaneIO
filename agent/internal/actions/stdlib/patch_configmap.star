step("patch configmap")
k8s.patch_configmap(args["namespace"], args["name"], args["key"], args.get("value", ""))
