# @name k8s_patch
# @summary Apply a JSON merge patch to any Kubernetes object; snapshot enables restore.
# @risk medium
# @reversible snapshot
# @targets *
#
# The `patch` arg is a JSON string that is decoded and applied as a merge patch
# (or strategic-merge when strategic=true). A malformed patch causes json.decode
# to error and aborts before any write. Works for any GVK including CRDs.
ns      = args["namespace"]
kind    = args["kind"]
name    = args["name"]
api_ver = args.get("apiVersion", args.get("api_version", ""))
res     = args.get("resource", "")
p       = json.decode(args["patch"])
strategic = args.get("strategic", "") == "true"

step("patch %s/%s" % (kind, name))
k8s.patch(ns, kind, name, p, strategic=strategic, api_version=api_ver, resource=res)
report("patched %s/%s" % (kind, name))
