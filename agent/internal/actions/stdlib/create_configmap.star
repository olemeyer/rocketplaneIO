# @name create-configmap
# @summary Create a ConfigMap from the given data. Rollback deletes it again.
# @risk medium
# @reversible snapshot
# @targets ConfigMap
#
# The data param arrives as a JSON object (key -> value). Create the ConfigMap; the
# snapshot records that it did not exist before, so restore deletes what this run
# created.
ns = args["namespace"]; name = args["name"]
data = json.decode(args.get("data", "{}"))
step("create %s" % name)
k8s.create({
    "apiVersion": "v1", "kind": "ConfigMap",
    "metadata": {"name": name, "namespace": ns},
    "data": data,
})
report("created configmap %s with %d key(s)" % (name, len(data)))
