# @name patch configmap
# @summary Set or remove one ConfigMap key; snapshot restores the prior value.
# @risk low
# @reversible snapshot
# @targets ConfigMap
#
# Field-scoped: remove=true drops the key, otherwise the single key is written.
# The pre-write snapshot restores the exact prior ConfigMap on Revert.
ns = args["namespace"]; name = args["name"]; key = args["key"]
if args.get("remove", "") == "true":
    step("remove key %s" % key)
    k8s.set_field(ns, "ConfigMap", name, ["data", key], None)
    report("removed key %s from configmap %s" % (key, name))
else:
    step("set key %s" % key)
    k8s.patch_configmap(ns, name, key, args.get("value", ""))
    report("set key %s on configmap %s" % (key, name))
