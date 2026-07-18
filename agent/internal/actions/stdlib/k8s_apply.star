# @name k8s_apply
# @summary Server-side apply a full manifest to any Kubernetes object; snapshot enables restore.
# @risk medium
# @reversible snapshot
# @targets *
#
# The `manifest` arg is a JSON string of the full desired object. apiVersion and
# kind are read from the manifest itself. Pass resource= as a kwarg only when the
# plural resource name cannot be inferred (edge-case CRDs). Works for any GVK.
res      = args.get("resource", "")
manifest = json.decode(args["manifest"])

kind = manifest.get("kind", "")
meta = manifest.get("metadata", {})
name = meta.get("name", "")

if not kind or not name:
    fail("manifest must have kind and metadata.name")

step("apply %s/%s" % (kind, name))
k8s.apply(manifest, resource=res)
report("applied %s/%s" % (kind, name))
