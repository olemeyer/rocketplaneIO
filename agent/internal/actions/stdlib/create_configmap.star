# @name create configmap
# @summary Create a ConfigMap from key=value lines. Rollback deletes it again.
# @risk medium
# @reversible snapshot
# @targets ConfigMap
#
# Build the object from the multiline `data` param, then create it. The snapshot
# records that it did not exist before, so restore deletes what this run created.
ns = args["namespace"]; name = args["name"]
data = {}
for line in args.get("data", "").split("\n"):
    line = line.strip()
    if line == "" or "=" not in line:
        continue
    k, v = line.split("=", 1)
    data[k.strip()] = v.strip()
step("create %s" % name)
k8s.create({
    "apiVersion": "v1", "kind": "ConfigMap",
    "metadata": {"name": name, "namespace": ns},
    "data": data,
})
report("created configmap %s with %d key(s)" % (name, len(data)))
