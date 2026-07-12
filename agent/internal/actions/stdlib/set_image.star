# @name set-image
# @summary Set one container's image via strategic merge (verified rollout).
# @risk medium
# @reversible snapshot
# @targets Deployment,StatefulSet,DaemonSet
#
# Snapshot the whole workload, then strategic-merge just the target container's
# image (preserves other containers/fields). Rollback restores the captured
# containers array (a whole-object JSON-merge restore replaces it back).
ns = args["namespace"]; kind = args["kind"]; name = args["name"]
container = args.get("container", "")
if container == "":
    fail("set_image needs a container name")
step("snapshot")
snapshot(ns, kind, name)
step("set image")
k8s.patch(ns, kind, name,
    {"spec": {"template": {"spec": {"containers": [{"name": container, "image": args["image"]}]}}}},
    strategic=True)
step("verify")
if not wait_rollout(ns, kind, name, timeout=300):
    fail("rollout did not complete")
