# @name rollout pause
# @summary Freeze a Deployment's rollout (spec.paused=true); rollback restores it.
# @risk low
# @reversible snapshot
# @targets Deployment
#
# Snapshot the Deployment and set spec.paused=true so no new rollout proceeds.
# The snapshot restores the prior paused state.
step("snapshot")
snapshot(args["namespace"], "Deployment", args["name"])
step("pause")
k8s.patch(args["namespace"], "Deployment", args["name"], {"spec": {"paused": True}})
report("rollout frozen")
