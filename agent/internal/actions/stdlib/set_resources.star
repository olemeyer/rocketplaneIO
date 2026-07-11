# @name set resources
# @summary Set a container's CPU/memory requests and limits (verified rollout).
# @risk medium
# @reversible snapshot
# @targets Deployment,StatefulSet,DaemonSet
#
# Strategic-merge just the target container's resources so sibling containers
# survive. The whole-object snapshot restores the prior requests/limits.
ns = args["namespace"]; kind = args["kind"]; name = args["name"]
c = args.get("container", "")
if c == "":
    fail("set_resources needs a container name")
requests = {}
limits = {}
if args.get("requestsCpu", "") != "":
    requests["cpu"] = args["requestsCpu"]
if args.get("requestsMemory", "") != "":
    requests["memory"] = args["requestsMemory"]
if args.get("limitsCpu", "") != "":
    limits["cpu"] = args["limitsCpu"]
if args.get("limitsMemory", "") != "":
    limits["memory"] = args["limitsMemory"]
resources = {}
if requests:
    resources["requests"] = requests
if limits:
    resources["limits"] = limits
if not resources:
    fail("no resources given")
step("set resources")
k8s.patch(ns, kind, name,
    {"spec": {"template": {"spec": {"containers": [{"name": c, "resources": resources}]}}}},
    strategic=True)
step("verify")
if not wait_rollout(ns, kind, name, timeout=300):
    fail("rollout did not settle")
report("resources set on %s" % c)
