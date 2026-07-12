# @name cronjob-resume
# @summary Resume a suspended CronJob so it schedules again; snapshot restores prior state.
# @risk low
# @reversible snapshot
# @targets CronJob
#
# Patch spec.suspend=false so the CronJob resumes scheduling on its cron cadence.
step("snapshot")
snapshot(args["namespace"], "CronJob", args["name"])
step("resume")
k8s.patch(args["namespace"], "CronJob", args["name"], {"spec": {"suspend": False}})
report("resumed cronjob %s/%s" % (args["namespace"], args["name"]))
