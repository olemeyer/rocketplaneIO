step("partition")
k8s.patch(args["namespace"], "StatefulSet", args["name"],
    {"spec": {"updateStrategy": {"rollingUpdate": {"partition": int(args["partition"])}}}})
