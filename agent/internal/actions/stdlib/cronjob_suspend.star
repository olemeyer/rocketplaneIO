# @name suspend cronjob
# @summary Suspend a CronJob so it stops scheduling; snapshot restores its prior state.
# @risk low
# @reversible snapshot
# @targets CronJob
#
# Patch spec.suspend=true. Running jobs are unaffected; only future runs are paused.
step("suspend")
k8s.patch(args["namespace"], "CronJob", args["name"], {"spec": {"suspend": True}})
report("suspended cronjob %s/%s" % (args["namespace"], args["name"]))
