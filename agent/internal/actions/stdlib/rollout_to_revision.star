# @name rollout to revision
# @summary Roll a Deployment to a specific historical revision (verified rollout).
# @risk medium
# @reversible snapshot
# @targets Deployment
#
# Find the ReplicaSet carrying the requested revision and patch its template onto
# the Deployment. The snapshot restores the current template if it does not settle.
ns = args["namespace"]; name = args["name"]
target = int(args["revision"])
step("find revision %d" % target)
revs = {}
for rs in k8s.raw_list("apps/v1", "ReplicaSet", ns):
    md = rs.get("metadata", {})
    owned = False
    for o in md.get("ownerReferences", []):
        if o.get("name") == name:
            owned = True
    if not owned:
        continue
    rev = int(md.get("annotations", {}).get("deployment.kubernetes.io/revision", "0"))
    revs[rev] = rs["spec"]["template"]
if target not in revs:
    fail("revision %d not found in ReplicaSet history" % target)
step("snapshot")
snapshot(ns, "Deployment", name)
step("roll to revision %d" % target)
k8s.patch(ns, "Deployment", name, {"spec": {"template": revs[target]}})
step("verify")
if not wait_rollout(ns, "Deployment", name, timeout=300):
    fail("rollout to revision %d did not settle" % target)
report("rolled to revision %d" % target)
