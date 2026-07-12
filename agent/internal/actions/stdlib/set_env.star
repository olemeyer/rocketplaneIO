# @name set env var
# @summary Set or remove one container environment variable (verified rollout).
# @risk medium
# @reversible snapshot
# @targets Deployment,StatefulSet,DaemonSet
#
# Strategic-merge just the target container's env entry so sibling containers and
# other env vars survive. The whole-object snapshot restores the prior env array.
ns = args["namespace"]; kind = args["kind"]; name = args["name"]
c = args.get("container", "")
if c == "":
    fail("set_env needs a container name")
E = args["envName"]
step("snapshot")
snapshot(ns, kind, name)
step("set env")
if args.get("remove", "") == "true":
    k8s.patch(ns, kind, name,
        {"spec": {"template": {"spec": {"containers": [{"name": c, "env": [{"name": E, "$patch": "delete"}]}]}}}},
        strategic=True)
    report("removed env %s from %s" % (E, c))
else:
    k8s.patch(ns, kind, name,
        {"spec": {"template": {"spec": {"containers": [{"name": c, "env": [{"name": E, "value": args.get("value", "")}]}]}}}},
        strategic=True)
    report("%s=%s on %s" % (E, args.get("value", ""), c))
step("verify")
if not wait_rollout(ns, kind, name, timeout=300):
    fail("rollout did not settle")
