# @name rollout undo
# @summary Roll a Deployment back to its previous revision (verified rollout).
# @risk medium
# @reversible snapshot
# @targets Deployment
#
# Find the ReplicaSet one revision behind the current one and patch its template
# back onto the Deployment. The snapshot of the Deployment restores the current
# template if the rollout does not settle.
ns = args["namespace"]; name = args["name"]
step("find previous revision")
cur = -1
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
    if rev > cur:
        cur = rev
# the highest revision strictly below the current one — revision numbers are not
# contiguous after prior rollbacks, so cur-1 may not exist.
prev = -1
for rev in revs:
    if rev < cur and rev > prev:
        prev = rev
if prev < 0:
    fail("no previous revision to roll back to")
step("roll back to revision %d" % prev)
k8s.patch(ns, "Deployment", name, {"spec": {"template": revs[prev]}})
step("verify")
if not wait_rollout(ns, "Deployment", name, timeout=300):
    fail("rollback rollout did not settle")
report("rolled back to revision %d" % prev)
