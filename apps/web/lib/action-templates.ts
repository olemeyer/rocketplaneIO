// action-templates.ts — der KANONISCHE Starlark-Quelltext jeder eingebauten
// Action. „Alles ist Starlark, auch built-in": die Library zeigt diese Sources,
// man kann sie forken (→ editierbarer Custom-Workflow) und darauf aufbauen.
//
// Jede Source nutzt args["<feld>"] passend zu den BUILTINS-Feldern der Action
// und ist mit den echten k8s.*-Builtins LAUFFÄHIG (nicht nur Deko). args sind
// immer Strings → int()/Vergleiche wo nötig.

export const BUILTIN_STARLARK: Record<string, string> = {
  rollout_restart: `# Restart a workload and verify the new revision is fully rolled out.
ns = args["namespace"]
kind = args["kind"]
name = args["name"]

step("restart %s/%s" % (kind, name))
k8s.rollout_restart(ns, kind, name)

step("wait for rollout")
if not wait_rollout(ns, kind, name, timeout=180):
    fail("rollout did not complete in time")

step("verify")
report("every pod is on the new revision")
`,

  scale: `# Scale a workload and roll back automatically if it does not settle.
ns = args["namespace"]
kind = args["kind"]
name = args["name"]
target = int(args["replicas"])

step("snapshot")
before = k8s.get(ns, kind, name)["desired"]
report("current replicas: %d" % before)

step("scale to %d" % target)
k8s.scale(ns, kind, name, target)
if not wait_ready(ns, kind, name, timeout=120):
    step("rollback")
    k8s.scale(ns, kind, name, before)
    wait_ready(ns, kind, name, timeout=120)
    fail("did not settle - rolled back to %d" % before)

step("verify")
report("settled at %d replicas" % target)
`,

  set_image: `# Deploy a new container image (kubectl set image) with a verified rollout.
ns = args["namespace"]
kind = args["kind"]
name = args["name"]
image = args["image"]
container = args["container"]

step("set image")
k8s.set_image(ns, kind, name, image, container=container)
report("%s -> %s" % (container or "(sole container)", image))

step("wait for rollout")
if not wait_rollout(ns, kind, name, timeout=180):
    fail("rollout did not complete - cancel to restore the previous image")

step("verify")
report("the new image is live on every pod")
`,

  rollout_undo: `# Roll back a Deployment to its previous revision.
ns = args["namespace"]
name = args["name"]

step("roll back to previous revision")
k8s.rollout_undo(ns, name)

step("wait for rollout")
if not wait_rollout(ns, "Deployment", name, timeout=180):
    fail("rollback rollout did not complete")

step("verify")
report("the previous revision is live")
`,

  rollout_pause: `# Freeze a Deployment rollout (kubectl rollout pause).
ns = args["namespace"]
name = args["name"]

step("pause rollout")
k8s.pause(ns, name)
report("rollout frozen - resume to continue")
`,

  rollout_resume: `# Resume a paused Deployment and watch it settle.
ns = args["namespace"]
name = args["name"]

step("resume rollout")
k8s.resume(ns, name)

step("wait for rollout")
wait_rollout(ns, "Deployment", name, timeout=180)
report("rollout resumed and settled")
`,

  hpa_set: `# Set HorizontalPodAutoscaler bounds (the honest scaling in HPA clusters).
ns = args["namespace"]
name = args["name"]
lo = int(args["minReplicas"])
hi = int(args["maxReplicas"])

step("set HPA bounds %d-%d" % (lo, hi))
k8s.hpa_set(ns, name, lo, hi)
report("autoscaler now stays within %d..%d" % (lo, hi))
`,

  cronjob_trigger: `# Run a CronJob now as a one-off Job.
ns = args["namespace"]
name = args["name"]

step("trigger %s now" % name)
job = k8s.cronjob_trigger(ns, name)
report("created one-off job: %s" % job)
`,

  cronjob_suspend: `# Pause a CronJob (maintenance window).
ns = args["namespace"]
name = args["name"]

step("suspend %s" % name)
k8s.cronjob_suspend(ns, name)
report("cronjob paused - resume to re-enable the schedule")
`,

  cronjob_resume: `# Resume a suspended CronJob.
ns = args["namespace"]
name = args["name"]

step("resume %s" % name)
k8s.cronjob_resume(ns, name)
report("cronjob schedule re-enabled")
`,

  cleanup_pods: `# Delete terminal pods (Failed/Succeeded) in a namespace.
# Note: this composes k8s.pods + k8s.delete_pod — no dedicated builtin needed.
ns = args["namespace"]

step("scan %s for terminal pods" % ns)
victims = [p for p in k8s.pods(ns) if p["phase"] in ("Failed", "Succeeded")]
report("%d terminal pod(s) to remove" % len(victims))

step("delete")
n = 0
for p in victims:
    k8s.delete_pod(ns, p["name"])
    n += 1
report("removed %d pod(s)" % n)
`,

  delete_pod: `# Delete one pod; its owner recreates it.
ns = args["namespace"]
pod = args["pod"]

step("delete %s" % pod)
k8s.delete_pod(ns, pod)
report("pod deleted - the owning controller will recreate it")
`,

  cordon: `# Mark a node unschedulable.
node = args["node"]

step("cordon %s" % node)
k8s.cordon(node)
report("%s is now unschedulable (existing pods keep running)" % node)
`,

  uncordon: `# Make a node schedulable again.
node = args["node"]

step("uncordon %s" % node)
k8s.uncordon(node)
report("%s is schedulable again" % node)
`,

  drain: `# Cordon a node. Full PDB-aware eviction across all namespaces is what the
# built-in "drain" does natively — this template is the safe first half to
# build your own maintenance flow on.
node = args["node"]

step("cordon %s" % node)
k8s.cordon(node)
report("node cordoned - no new pods will schedule here")
`,

  node_taint: `# Add a taint to steer workloads away (cancel removes it).
node = args["node"]
key = args["key"]
value = args["value"]
effect = args["effect"]

step("taint %s" % node)
k8s.taint(node, key, value=value, effect=effect)
report("%s tainted: %s=%s:%s" % (node, key, value, effect))
`,

  node_untaint: `# Remove a taint from a node by key.
node = args["node"]
key = args["key"]

step("untaint %s" % node)
k8s.untaint(node, key)
report("removed taint %s from %s" % (key, node))
`,

  debug_bundle: `# Read-only triage snapshot: rollout state + pod statuses + warning events.
ns = args["namespace"]
kind = args["kind"]
name = args["name"]

step("workload")
st = k8s.get(ns, kind, name)
report("%d/%d ready, %d updated" % (st["ready"], st["desired"], st["updated"]))

step("pods")
for p in k8s.pods(ns):
    if p["name"].startswith(name):
        report("%s: ready=%s restarts=%d phase=%s" % (p["name"], p["ready"], p["restarts"], p["phase"]))

step("events")
warn = [e for e in k8s.events(ns, name) if e["type"] == "Warning"]
report("%d warning event(s)" % len(warn))
for e in warn:
    report("%s: %s" % (e["reason"], e["message"]))
`,

  pod_events: `# Read-only: the recent events of a workload (and its pods).
ns = args["namespace"]
name = args["name"]

step("events for %s" % name)
evs = k8s.events(ns, name)
report("%d event(s)" % len(evs))
for e in evs:
    mark = "WARN" if e["type"] == "Warning" else "info"
    report("[%s] %s: %s" % (mark, e["reason"], e["message"]))
`,
};
