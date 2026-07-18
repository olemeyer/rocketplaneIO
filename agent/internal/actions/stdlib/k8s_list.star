# @name k8s_list
# @summary List objects of any kind, optionally filtered by namespace and label selector.
# @risk low
# @reversible readonly
# @targets *
#
# Read-only: no snapshot, no mutation. Works for any GVK including CRDs.
# Pass namespace='-' to list across all namespaces (cluster-scoped list).
# Pass api_version= and resource= for kinds not in the built-in map.
ns      = args["namespace"]
kind    = args["kind"]
api_ver = args.get("apiVersion", args.get("api_version", ""))
res     = args.get("resource", "")
sel     = args.get("selector", "")

step("list %s" % kind)
items = k8s.list(api_ver, kind, ns, selector=sel, resource=res)
count = len(items)
report("%d %s object(s)" % (count, kind))

# For large lists report a brief summary per item instead of the full objects,
# so the step detail stays within the 64 KB report cap.
if count <= 20:
    for obj in items:
        meta = obj.get("metadata", {})
        report("  %s/%s" % (meta.get("namespace", ns), meta.get("name", "?")))
else:
    for obj in items:
        meta = obj.get("metadata", {})
        report("  %s" % meta.get("name", "?"))
