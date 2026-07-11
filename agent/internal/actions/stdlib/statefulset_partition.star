# @name statefulset partition
# @summary Set a StatefulSet rolling-update partition for staged rollouts.
# @risk low
# @reversible snapshot
# @targets StatefulSet
#
# Snapshot the StatefulSet, patch the rolling-update partition; rollback restores
# the prior partition.
step("partition")
k8s.patch(args["namespace"], "StatefulSet", args["name"],
    {"spec": {"updateStrategy": {"rollingUpdate": {"partition": int(args["partition"])}}}})
