# @name rollout resume
# @summary Resume a paused Deployment (spec.paused=false) and wait for it to settle.
# @risk low
# @reversible snapshot
# @targets Deployment
#
# Snapshot the Deployment and set spec.paused=false, then wait for the resumed
# rollout to settle. The snapshot restores the prior paused state.
ns = args["namespace"]; name = args["name"]
step("resume")
k8s.patch(ns, "Deployment", name, {"spec": {"paused": False}})
step("verify")
if not wait_rollout(ns, "Deployment", name, timeout=300):
    fail("resumed rollout did not settle")
report("rollout resumed")
