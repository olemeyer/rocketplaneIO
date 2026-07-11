# @name list events
# @summary List recent namespace events, optionally warnings only (read-only).
# @risk low
# @reversible readonly
# @targets Namespace
#
# The namespace is the target (args["name"]); "-" reads across all namespaces.
# warningsOnly=true keeps only Warning events; limit caps how many are printed.
ns = args["name"]
if ns == "-":
    ns = ""
step("list events")
evs = k8s.events(ns, "")
if args.get("warningsOnly", "") == "true":
    evs = [e for e in evs if e.get("type", "") == "Warning"]
report("%d event(s)" % len(evs))
limit = int(args.get("limit", "40"))
for i in range(limit):
    if i >= len(evs):
        break
    e = evs[i]
    mark = "WARN" if e.get("type", "") == "Warning" else "info"
    report("[%s] %s %s: %s" % (mark, e.get("object", ""), e.get("reason", ""), e.get("message", "")))
