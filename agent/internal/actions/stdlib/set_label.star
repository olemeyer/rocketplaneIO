# @name set label
# @summary Set or remove one metadata label on a cluster-scoped object; snapshot restores.
# @risk low
# @reversible snapshot
# @targets Node,Namespace
#
# Node and Namespace are cluster-scoped, so the namespace argument is empty.
# Field-scoped: rollback removes an added key without touching sibling labels.
ns = args.get("namespace", ""); kind = args["kind"]; name = args["name"]
path = ["metadata", "labels", args["key"]]
if args.get("remove", "") == "true":
    step("remove label %s" % args["key"])
    k8s.set_field(ns, kind, name, path, None)
    report("removed label %s" % args["key"])
else:
    step("set label %s" % args["key"])
    k8s.set_field(ns, kind, name, path, args.get("value", ""))
    report("%s=%s" % (args["key"], args.get("value", "")))
