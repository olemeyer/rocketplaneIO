# @name pod-logs
# @summary Read a pod's logs (optionally the previous, crashed container) from the kubelet.
# @risk low
# @reversible readonly
# @targets Pod
#
# Read-only: streams straight from the kubelet, independent of the log pipeline
# (which in an incident is often itself down). previous reads the crashed container.
ns = args["namespace"]; pod = args["name"]
prev = args.get("previous", "") == "true"
tail = int(args.get("tailLines", "200"))
which = "previous (crashed) container" if prev else "current"
step("read logs (%s)" % which)
out = k8s.logs(ns, pod, container=args.get("container", ""), previous=prev, tail=tail)
report("[%s, last %d lines]\n%s" % (which, tail, out))
