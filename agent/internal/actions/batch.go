package actions

// batch.go — CronJob-Handgriffe des Alltags:
//   cronjob_trigger  → JETZT einen Job aus dem CronJob-Template starten
//                      (`kubectl create job --from=cronjob/x`) und dessen Start
//                      beobachten. Der manuelle Lauf ist bewusst NICHT undo-bar
//                      (er läuft schon), wird aber sauber verifiziert.
//   cronjob_suspend  → CronJob pausieren (Wartungsfenster). Rollback = resume.
//   cronjob_resume   → Gegenteil.

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

/* ── cronjob_trigger ────────────────────────────────────────────────────── */

// createJobFromCronJob erzeugt einen einmaligen Job aus dem CronJob-Template
// (das ist `kubectl create job --from=cronjob/x`) und gibt den Job-Namen zurück.
// Von der Pipeline UND vom Starlark-Builtin k8s.cronjob_trigger genutzt.
func (r *Runner) createJobFromCronJob(ctx context.Context, ns, name string) (string, error) {
	cj, err := r.clientset.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: name + "-manual-",
			Namespace:    ns,
			Labels:       map[string]string{"rocketplane.io/manual-trigger": name},
			Annotations:  map[string]string{"cronjob.kubernetes.io/instantiate": "manual"},
		},
		Spec: cj.Spec.JobTemplate.Spec,
	}
	created, err := r.clientset.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{FieldManager: "rocketplane-agent"})
	if err != nil {
		return "", err
	}
	return created.Name, nil
}

// planCronjobTrigger: Job aus dem CronJob-Template erzeugen und auf Start warten.
func (r *Runner) planCronjobTrigger(a Action) []step {
	var jobName string
	return []step{
		{name: "create job", run: func(ctx context.Context, _ func(string)) (string, error) {
			name, err := r.createJobFromCronJob(ctx, a.TargetNamespace, a.TargetName)
			if err != nil {
				return "", err
			}
			jobName = name
			return "job " + jobName + " created", nil
		}},
		{name: "start", run: func(ctx context.Context, report func(string)) (string, error) {
			t := time.NewTicker(observeEvery)
			defer t.Stop()
			for {
				j, err := r.clientset.BatchV1().Jobs(a.TargetNamespace).Get(ctx, jobName, metav1.GetOptions{})
				if err == nil {
					// Fertig (Complete/Failed) ODER mindestens ein aktiver Pod =
					// der Trigger hat gegriffen; wir blockieren nicht auf lange Jobs.
					if j.Status.Succeeded > 0 {
						return fmt.Sprintf("job completed (%d succeeded)", j.Status.Succeeded), nil
					}
					if j.Status.Failed > 0 {
						return "", fmt.Errorf("job pod failed")
					}
					if j.Status.Active > 0 {
						return fmt.Sprintf("job running (%d active)", j.Status.Active), nil
					}
					report("waiting for job pod…")
				}
				select {
				case <-ctx.Done():
					return "", fmt.Errorf("timeout waiting for job to start")
				case <-t.C:
				}
			}
		}},
	}
}

/* ── cronjob suspend / resume ───────────────────────────────────────────── */

func (r *Runner) setCronjobSuspend(ctx context.Context, a Action, suspend bool) error {
	patch := []byte(fmt.Sprintf(`{"spec":{"suspend":%t}}`, suspend))
	_, err := r.clientset.BatchV1().CronJobs(a.TargetNamespace).Patch(ctx, a.TargetName,
		types.MergePatchType, patch, metav1.PatchOptions{FieldManager: "rocketplane-agent"})
	return err
}

func (r *Runner) planCronjobSuspend(a Action, suspend bool) []step {
	verb := "suspended"
	if !suspend {
		verb = "resumed"
	}
	return []step{
		{name: verb, run: func(ctx context.Context, _ func(string)) (string, error) {
			if err := r.setCronjobSuspend(ctx, a, suspend); err != nil {
				return "", err
			}
			return "cronjob " + verb, nil
		}},
		{name: "verify", run: func(ctx context.Context, _ func(string)) (string, error) {
			t := time.NewTicker(1 * time.Second)
			defer t.Stop()
			for {
				cj, err := r.clientset.BatchV1().CronJobs(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
				if err == nil && cj.Spec.Suspend != nil && *cj.Spec.Suspend == suspend {
					return fmt.Sprintf("verified: suspend=%t", suspend), nil
				}
				if err != nil && apierrors.IsNotFound(err) {
					return "", fmt.Errorf("cronjob not found")
				}
				select {
				case <-ctx.Done():
					return "", fmt.Errorf("timeout verifying suspend state")
				case <-t.C:
				}
			}
		}},
	}
}
