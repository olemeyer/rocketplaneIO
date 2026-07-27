# @name k8s_get
# @summary Read the full live spec of any Kubernetes object (Secrets show keys only).
# @risk low
# @reversible readonly
# @targets *
#
# Read-only: no snapshot, no mutation. Works for any GVK including CRDs.
# Pass api_version= and resource= for kinds not in the built-in map.
ns      = args["namespace"]
kind    = args["kind"]
name    = args["name"]
api_ver = args.get("apiVersion", args.get("api_version", ""))
res     = args.get("resource", "")

step("get %s/%s" % (kind, name))
obj = k8s.get(api_ver, kind, ns, name, resource=res)
if not obj:
    fail("%s/%s not found" % (kind, name))
# JSON, not str(): str() emits the Starlark repr (True, single quotes), which no
# consumer of the step detail — UI or MCP client — can parse.
report(json.encode(obj))
