# @name k8s_delete
# @summary Delete any Kubernetes object; snapshot captures the whole object so restore recreates it.
# @risk medium
# @reversible snapshot
# @targets *
#
# The whole object is captured before deletion. The generic Restore recreates it
# from the capture (Get→NotFound→Create), so a delete is reversible like any
# other mutation. Works for any GVK including CRDs. Pass api_version= and
# resource= for kinds not in the built-in map.
ns      = args["namespace"]
kind    = args["kind"]
name    = args["name"]
api_ver = args.get("apiVersion", args.get("api_version", ""))
res     = args.get("resource", "")

step("delete %s/%s" % (kind, name))
k8s.delete(ns, kind, name, api_version=api_ver, resource=res)
report("deleted %s/%s" % (kind, name))
