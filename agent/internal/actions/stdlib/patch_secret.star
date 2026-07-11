# @name patch secret
# @summary Set or remove one Secret key; snapshot restores the prior value.
# @risk medium
# @reversible snapshot
# @targets Secret
#
# stringData writes are stored base64-encoded by the API server; remove=true drops
# the key from data. The pre-write snapshot restores the exact prior Secret.
ns = args["namespace"]; name = args["name"]; key = args["key"]
if args.get("remove", "") == "true":
    step("remove key %s" % key)
    k8s.set_field(ns, "Secret", name, ["data", key], None)
    report("removed key %s from secret %s" % (key, name))
else:
    step("set key %s" % key)
    k8s.patch(ns, "Secret", name, {"stringData": {key: args.get("value", "")}})
    report("set key %s on secret %s" % (key, name))
