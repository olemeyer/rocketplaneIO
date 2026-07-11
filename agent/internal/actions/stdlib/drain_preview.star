# @name drain preview
# @summary Preview which pods a node drain would evict (read-only).
# @risk low
# @reversible readonly
# @targets Node
#
# Read-only: lists every pod currently scheduled on the node so an operator can see
# the blast radius before cordoning or draining. No mutation.
step("preview drain of %s" % args["node"])
victims = [p for p in k8s.pods("") if p.get("node") == args["node"]]
report("%d pod(s) would be evicted" % len(victims))
for p in victims:
    report(p["name"])
