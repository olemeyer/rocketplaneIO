step("label")
k8s.set_field(args["namespace"], args["kind"], args["name"],
    ["metadata", "labels", args["key"]], args.get("value", ""))
