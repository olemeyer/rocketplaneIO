# @name exec-readonly
# @summary Run one whitelisted read-only command (cat/ls/…) inside a container.
# @risk low
# @reversible readonly
# @targets Pod
#
# Read-only: no shell, argv only, output hard-capped. retry catches the short run
# window of a CrashLooping pod. The command whitelist is enforced by the primitive.
ns = args["namespace"]; pod = args["name"]
cmd = json.decode(args["command"])
step("exec %s" % " ".join(cmd))
out = k8s.exec(ns, pod, cmd, container=args.get("container", ""), retry=args.get("retry", "") == "true")
report(out)
