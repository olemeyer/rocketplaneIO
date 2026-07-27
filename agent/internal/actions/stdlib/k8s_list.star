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

# One JSON document per report line so the consumer (UI or MCP client) can parse
# it. Small lists carry the full objects; large ones degrade to identity only,
# keeping the step detail under the report cap.
report(json.encode({"kind": kind, "namespace": ns, "count": count}))
for obj in items:
    meta = obj.get("metadata", {})
    if count <= 20:
        report(json.encode(obj))
        continue
    # Above the full-object threshold, keep the fields that carry the diagnosis
    # instead of the name alone — a 50-event list of bare names answers nothing.
    st = obj.get("status", {})
    summary = {
        "namespace": meta.get("namespace", ns),
        "name": meta.get("name", "?"),
    }
    for key in ["reason", "message", "type", "count", "lastTimestamp"]:
        if key in obj:
            summary[key] = obj[key]
    for key in ["phase", "reason", "message"]:
        if key in st:
            summary[key] = st[key]
    report(json.encode(summary))
