# @name net-probe
# @summary Probe reachability/TLS/DNS from the agent's in-cluster vantage (http|tcp|dns|tls).
# @risk low
# @reversible readonly
# @targets Namespace
#
# Read-only network diagnostics run by the agent itself (it sits in the cluster
# network): http (status+latency), tcp (port open?), dns (resolvable?), tls
# (handshake + cert-chain expiry — a top incident class).
mode = args.get("mode", "http")
target = args["target"]
step("probe %s %s" % (mode, target))
out = net_probe(mode, target, method=args.get("method", "GET"))
report(out)
