# @name drain preview
# @summary Preview which pods a node drain would evict (read-only).
# @risk low
# @reversible readonly
# @targets Node
#
# The node is the target (args["name"]). Read-only: list every pod scheduled on the
# node so an operator can see the blast radius before cordoning or draining.
node = args["name"]
step("preview drain of %s" % node)
victims = [p for p in k8s.pods("") if p.get("node") == node]
report("%d pod(s) would be evicted" % len(victims))
for p in victims:
    report(p["name"])
