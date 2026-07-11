# @name get secret keys
# @summary List a Secret's key names (values stay redacted; read-only).
# @risk low
# @reversible readonly
# @targets Secret
#
# Read-only: Secret values are auto-redacted by the runtime, so only key names are
# ever surfaced. No snapshot, no mutation.
ns = args["namespace"]; name = args["name"]
step("read secret %s" % name)
sec = k8s.raw_get("v1", "Secret", ns, name)
if not sec:
    fail("Secret %s/%s not found" % (ns, name))
keys = list(sec.get("data", {}).keys())
report("%d key(s): %s" % (len(keys), ", ".join(keys)))
