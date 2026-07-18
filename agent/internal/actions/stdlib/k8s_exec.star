# @name k8s_exec
# @summary Execute an arbitrary command inside a pod container and report the output.
# @risk medium
# @reversible none
# @targets *
#
# Uses k8s.exec_raw (no argv whitelist). The command arrives as a JSON array
# string so the control plane can pass arbitrary arguments safely. No TTY.
# Output is capped at 64 KB. Timeout is clamped to 1..300 s (default 30).
ns        = args["namespace"]
name      = args["name"]
container = args.get("container", "")
timeout   = int(args.get("timeoutSeconds", "30"))
cmd       = json.decode(args["command"])

step("exec in %s/%s" % (ns, name))
out = k8s.exec_raw(ns, name, cmd, container=container, timeout=timeout)
report(out)
