# @name rollout-history
# @summary List a Deployment's revision history with container images (read-only).
# @risk low
# @reversible readonly
# @targets Deployment
#
# Read-only: walks the Deployment's owned ReplicaSets and reports each revision with
# the first container image, newest-to-oldest as Kubernetes records them.
ns = args["namespace"]; name = args["name"]
step("rollout history")
sets = k8s.raw_list("apps/v1", "ReplicaSet", ns)
count = 0
for rs in sets:
    owners = rs["metadata"].get("ownerReferences", [])
    owned = False
    for o in owners:
        if o.get("name") == name:
            owned = True
    if not owned:
        continue
    rev = rs["metadata"].get("annotations", {}).get("deployment.kubernetes.io/revision", "?")
    img = rs["spec"]["template"]["spec"]["containers"][0]["image"]
    report("revision %s -> %s" % (rev, img))
    count = count + 1
report("%d revision(s)" % count)
