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

  /* ── Batch 1 extras — generisch über k8s.raw_* (Auto-Snapshot + Undo) ── */

  rollout_history: `# Read-only: revision history of a Deployment via its ReplicaSets.
ns = args["namespace"]
name = args["name"]

step("revisions of %s" % name)
sets = k8s.raw_list("apps/v1", "ReplicaSet", ns)
for rs in sets:
    owners = rs.get("metadata", {}).get("ownerReferences", [])
    if any([o.get("name") == name for o in owners]):
        rev = rs.get("metadata", {}).get("annotations", {}).get("deployment.kubernetes.io/revision", "?")
        img = rs["spec"]["template"]["spec"]["containers"][0]["image"]
        report("revision %s -> %s" % (rev, img))
`,

  rollout_to_revision: `# Roll a Deployment to a specific revision's template.
ns = args["namespace"]
name = args["name"]
rev = args["revision"]

step("find template of revision %s" % rev)
tmpl = None
for rs in k8s.raw_list("apps/v1", "ReplicaSet", ns):
    md = rs.get("metadata", {})
    if any([o.get("name") == name for o in md.get("ownerReferences", [])]):
        if md.get("annotations", {}).get("deployment.kubernetes.io/revision") == str(rev):
            tmpl = rs["spec"]["template"]
if not tmpl:
    fail("revision %s not found" % rev)

step("apply template")
k8s.raw_patch("apps/v1", "Deployment", ns, name, {"spec": {"template": tmpl}})
if not wait_rollout(ns, "Deployment", name, timeout=180):
    fail("rollout did not settle")
`,

  set_resources: `# Set container resource requests/limits and verify the rollout.
ns = args["namespace"]
name = args["name"]

step("patch resources")
k8s.raw_patch("apps/v1", args.get("kind", "Deployment"), ns, name, {"spec": {"template": {"spec": {"containers": [{
    "name": args["container"],
    "resources": {"limits": {"memory": args.get("limitsMemory", "512Mi")}},
}]}}}})
step("verify")
if not wait_rollout(ns, args.get("kind", "Deployment"), name, timeout=180):
    fail("rollout did not settle after resource change")
`,

  set_env: `# Set an env var on a container and verify the rollout.
ns = args["namespace"]
name = args["name"]

step("patch env")
k8s.raw_patch("apps/v1", args.get("kind", "Deployment"), ns, name, {"spec": {"template": {"spec": {"containers": [{
    "name": args["container"],
    "env": [{"name": args["envName"], "value": args.get("value", "")}],
}]}}}})
step("verify")
if not wait_rollout(ns, args.get("kind", "Deployment"), name, timeout=180):
    fail("rollout did not settle")
`,

  statefulset_partition: `# Stage a canary: only ordinals >= partition update on the next change.
ns = args["namespace"]
name = args["name"]

step("set partition")
k8s.raw_patch("apps/v1", "StatefulSet", ns, name, {"spec": {"updateStrategy": {"rollingUpdate": {"partition": int(args["partition"])}}}})
report("partition = %s" % args["partition"])
`,

  hpa_toggle: `# Freeze an HPA (pin min=max at the current size) or restore its bounds.
ns = args["namespace"]
name = args["name"]

step("read hpa")
hpa = k8s.raw_get("autoscaling/v2", "HorizontalPodAutoscaler", ns, name)
cur = hpa["status"].get("currentReplicas", hpa["spec"].get("minReplicas", 1)) if hpa.get("status") else 1

step("freeze")
k8s.hpa_set(ns, name, cur, cur)
report("pinned min=max=%d — the autoscaler stops fighting manual fixes" % cur)
`,

  patch_configmap: `# Set one key in a ConfigMap (consumers need a rollout restart).
ns = args["namespace"]
name = args["name"]

step("patch key")
k8s.raw_patch("v1", "ConfigMap", ns, name, {"data": {args["key"]: args.get("value", "")}})
report("set %s" % args["key"])
`,

  annotate: `# Set an annotation on any object (pause an operator, tag for triage).
step("annotate")
k8s.raw_patch(args.get("apiVersion", "apps/v1"), args.get("kind", "Deployment"), args.get("namespace", ""), args["name"],
    {"metadata": {"annotations": {args["key"]: args.get("value", "")}}})
report("%s=%s" % (args["key"], args.get("value", "")))
`,

  set_label: `# Set a label on a Node or Namespace (scheduling / admission).
step("label")
k8s.raw_patch("v1", args.get("kind", "Node"), "", args["name"],
    {"metadata": {"labels": {args["key"]: args.get("value", "")}}})
report("%s=%s" % (args["key"], args.get("value", "")))
`,

  evict_pod: `# Graceful, PDB-aware eviction with verified replacement.
ns = args["namespace"]
pod = args["pod"]

step("evict")
k8s.delete_pod(ns, pod)
step("verify siblings")
ok = all([p["ready"] for p in k8s.pods(ns)])
report("all ready" if ok else "replacement still settling")
`,

  cleanup_jobs: `# Delete finished (Complete/Failed) Jobs in a namespace.
ns = args["namespace"]

step("scan jobs")
gone = 0
for j in k8s.raw_list("batch/v1", "Job", ns):
    st = j.get("status", {})
    if st.get("succeeded", 0) > 0 or st.get("failed", 0) > 0:
        k8s.raw_delete("batch/v1", "Job", ns, j["metadata"]["name"])
        gone += 1
report("deleted %d finished job(s)" % gone)
`,

  drain_preview: `# Read-only blast radius: which pods a drain would evict.
node = args["node"]

step("scan pods on %s" % node)
victims = [p for p in k8s.pods("") if p.get("node") == node]
report("%d pod(s) would be evicted" % len(victims))
for p in victims:
    report(p["name"])
`,

  /* ── Batch 2/3 — die generischen Primitive als kanonisches Starlark ── */

  get_resource: `# Read any object (CRDs too: pass apiVersion + kind).
step("read")
obj = k8s.raw_get(args.get("apiVersion", "v1"), args.get("kind", "ConfigMap"), args.get("namespace", ""), args["name"])
if not obj:
    fail("not found")
report(str(obj.get("spec", obj.get("data", obj))))
`,

  describe_resource: `# Status + conditions of any object (CRDs too).
step("status")
obj = k8s.raw_get(args.get("apiVersion", "v1"), args.get("kind", "Deployment"), args.get("namespace", ""), args["name"])
if not obj:
    fail("not found")
for c in obj.get("status", {}).get("conditions", []):
    report("%s=%s %s" % (c.get("type"), c.get("status"), c.get("message", "")))
`,

  get_secret: `# Inspect a Secret WITHOUT leaking values — keys only.
step("inspect")
sec = k8s.raw_get("v1", "Secret", args["namespace"], args["name"])
if not sec:
    fail("not found")
keys = list(sec.get("data", {}).keys())
report("%d key(s): %s" % (len(keys), ", ".join(keys)))
`,

  helm_releases: `# List Helm releases from their sh.helm.release.v1 secrets.
step("releases")
for s in k8s.raw_list("v1", "Secret", args.get("namespace", ""), selector="owner=helm"):
    lb = s["metadata"].get("labels", {})
    report("%s rev=%s status=%s" % (lb.get("name"), lb.get("version"), lb.get("status")))
`,

  patch_secret: `# Set one key in a Secret (undo restores the prior value).
# NOTE: values written here end up in the org-visible workflow source —
# prefer the built-in patch_secret action for real credentials.
ns = args["namespace"]
name = args["name"]

step("patch")
k8s.raw_patch("v1", "Secret", ns, name, {"stringData": {args["key"]: args.get("value", "")}})
report("set %s" % args["key"])
`,

  create_configmap: `# Create a ConfigMap (undo deletes it again).
step("create")
k8s.raw_apply({
    "apiVersion": "v1", "kind": "ConfigMap",
    "metadata": {"name": args["name"], "namespace": args["namespace"]},
    "data": {"key": args.get("value", "hello")},
})
report("created %s" % args["name"])
`,

  delete_configmap: `# Delete a ConfigMap — undo recreates it from the before-state.
step("delete")
k8s.raw_delete("v1", "ConfigMap", args["namespace"], args["name"])
report("deleted (undo restores)")
`,

  pdb_set: `# Set PDB bounds (minAvailable XOR maxUnavailable).
step("patch pdb")
k8s.raw_patch("policy/v1", "PodDisruptionBudget", args["namespace"], args["name"],
    {"spec": {"minAvailable": int(args.get("minAvailable", 1)), "maxUnavailable": None}})
report("minAvailable=%s" % args.get("minAvailable", 1))
`,

  patch_resource: `# Generic merge patch on any whitelisted object (auto-snapshot + undo).
step("patch")
k8s.raw_patch(args.get("apiVersion", "v1"), args["kind"], args.get("namespace", ""), args["name"],
    {"metadata": {"annotations": {"rocketplane.io/patched": "true"}}})
report("patched — replace the annotation example with your real merge patch")
`,

  pvc_expand: `# Grow a PVC (shrinking is impossible — guard included).
ns = args["namespace"]
name = args["name"]

step("guard")
pvc = k8s.raw_get("v1", "PersistentVolumeClaim", ns, name)
cur = pvc["spec"]["resources"]["requests"]["storage"]
report("current: %s -> requested: %s" % (cur, args["size"]))

step("expand")
k8s.raw_patch("v1", "PersistentVolumeClaim", ns, name,
    {"spec": {"resources": {"requests": {"storage": args["size"]}}}})
report("resize requested (storage class must support expansion)")
`,

  delete_job: `# Delete one Job (undo recreates it from the before-state).
step("delete")
k8s.raw_delete("batch/v1", "Job", args["namespace"], args["name"])
report("job deleted")
`,

  list_events: `# All recent events of a namespace — what happened here?
step("events")
evs = k8s.events(args.get("namespace", ""), "")
report("%d event(s)" % len(evs))
for e in evs[:40]:
    mark = "WARN" if e["type"] == "Warning" else "info"
    report("[%s] %s %s: %s" % (mark, e.get("object", ""), e["reason"], e["message"]))
`,

  run_debug_pod: `# Ephemeral probe pod via raw_apply — image bisecting, HTTP/DNS probes,
# PVC inspection. The built-in run_debug_pod action does this with auto-cleanup;
# this is the same pattern as an editable workflow.
ns = args["namespace"]
name = "rp-probe-" + args.get("suffix", "1")

step("create probe")
k8s.raw_apply({
    "apiVersion": "v1", "kind": "Pod",
    "metadata": {"name": name, "namespace": ns},
    "spec": {
        "restartPolicy": "Never",
        "automountServiceAccountToken": False,
        "containers": [{
            "name": "probe",
            "image": args.get("image", "curlimages/curl"),
            "command": ["sh", "-c", args.get("cmd", "echo probe ok")],
            "resources": {"limits": {"memory": "256Mi", "cpu": "250m"}},
        }],
    },
})

step("wait for exit")
done = False
for i in range(40):
    pod = k8s.raw_get("v1", "Pod", ns, name)
    phase = pod.get("status", {}).get("phase", "") if pod else ""
    if phase in ("Succeeded", "Failed"):
        report("probe %s" % phase)
        done = True
        break
    sleep(3)
if not done:
    report("probe still running — raising no error, cleanup follows")

step("cleanup")
k8s.raw_delete("v1", "Pod", ns, name)
report("probe removed")
`,

  pod_logs: `# pod_logs reads kubelet logs (incl. --previous) — that requires the
# built-in action; log access is not exposed to workflow scripts.
# Closest scriptable signal: the pod's status + last termination state.
step("status")
pod = k8s.raw_get("v1", "Pod", args["namespace"], args["pod"])
if not pod:
    fail("pod not found")
for cs in pod.get("status", {}).get("containerStatuses", []):
    last = cs.get("lastState", {}).get("terminated")
    if last:
        report("%s last exit: %s (%s)" % (cs["name"], last.get("exitCode"), last.get("reason")))
`,

  net_probe: `# net_probe runs through the built-in action (the agent probes the
# network directly — http/tcp/dns/tls). Workflow scripts have no network
# builtins; use the built-in net_probe action for reachability/TLS checks.
step("hint")
report("use the built-in net_probe action (mode=http|tcp|dns|tls)")
`,

  exec_readonly: `# exec runs through the built-in action (whitelisted argv, no shell) —
# container exec is not exposed to workflow scripts. Use run_debug_pod for
# arbitrary commands in a FRESH pod, or the built-in exec_readonly action.
step("hint")
report("use the built-in exec_readonly action (supports retry for CrashLoop pods)")
`,
};
