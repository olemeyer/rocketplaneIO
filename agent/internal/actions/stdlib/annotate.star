# Key-level: field-scoped so rollback removes an added key (or restores a changed
# one) WITHOUT clobbering sibling annotations.
step("annotate")
k8s.set_field(args["namespace"], args["kind"], args["name"],
    ["metadata", "annotations", args["key"]], args.get("value", ""))
