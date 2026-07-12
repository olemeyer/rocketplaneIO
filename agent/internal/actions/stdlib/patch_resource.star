# @name patch resource
# @summary Apply a raw JSON merge patch to a networking/policy object; snapshot restores.
# @risk medium
# @reversible snapshot
# @targets Service,Ingress,NetworkPolicy,PodDisruptionBudget
#
# The `patch` param is a JSON object merged onto the target. A malformed patch makes
# json.decode error out and aborts before any write — that is the intended guard.
ns = args["namespace"]; name = args["name"]; kind = args["kind"]
p = json.decode(args["patch"])
step("snapshot")
snapshot(ns, kind, name)
step("patch %s/%s" % (kind, name))
k8s.patch(ns, kind, name, p)
report("patched %s/%s" % (kind, name))
