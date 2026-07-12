# @name pvc-expand
# @summary Request a larger PersistentVolumeClaim (grow-only, not reversible).
# @risk high
# @reversible none
# @targets PersistentVolumeClaim
#
# Volume expansion is one-way: PVCs cannot shrink, so this cannot be reverted. The
# storage class must have allowVolumeExpansion=true or the request stays pending.
ns = args["namespace"]; name = args["name"]
pvc = k8s.get("v1", "PersistentVolumeClaim", ns, name)
if not pvc:
    fail("PersistentVolumeClaim %s/%s not found" % (ns, name))
current = pvc.get("spec", {}).get("resources", {}).get("requests", {}).get("storage", "unknown")
step("expand %s" % name)
report("current storage %s -> requested %s" % (current, args["size"]))
k8s.patch(ns, "PersistentVolumeClaim", name, {"spec": {"resources": {"requests": {"storage": args["size"]}}}})
report("resize requested (grow-only; storage class must support expansion)")
