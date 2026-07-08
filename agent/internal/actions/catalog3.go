package actions

// catalog3.go — Katalog-Batch 3: die Lücken aus echten Crash-Investigations.
//   pod_logs      → kubectl logs [--previous] direkt vom Kubelet — unabhängig
//                   von der Log-Pipeline (die im Incident-Fall gern selbst
//                   abgedreht/kaputt ist, z.B. no-monitoring.xml).
//   run_debug_pod → ephemerer Probe-Pod: Image + argv (+ optional PVC read-only
//                   unter /pvc, optional Node-Pinning), läuft bis Exit/Timeout,
//                   Logs sind das Ergebnis, der Pod wird IMMER aufgeräumt.
//                   Das typisierte Gegenstück zu „kubectl run chprobe-…" —
//                   Image-Bisect, PVC-Inspektion, Node-Verdacht.
//   exec_readonly bekommt retry (readgeneric.go): fängt das kurze Lauffenster
//   eines CrashLoop-Pods ab, statt einmal zu scheitern.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/* ── pod_logs ───────────────────────────────────────────────────────────── */

func (r *Runner) planPodLogs(a Action) []step {
	return []step{
		{name: "logs", run: func(ctx context.Context, _ func(string)) (string, error) {
			var p struct {
				Container string `json:"container"`
				Previous  bool   `json:"previous"`
				TailLines int64  `json:"tailLines"`
			}
			_ = json.Unmarshal(a.Params, &p)
			if p.TailLines <= 0 || p.TailLines > 500 {
				p.TailLines = 200
			}
			opts := &corev1.PodLogOptions{Container: p.Container, Previous: p.Previous, TailLines: &p.TailLines}
			stream, err := r.clientset.CoreV1().Pods(a.TargetNamespace).GetLogs(a.TargetName, opts).Stream(ctx)
			if err != nil {
				if p.Previous {
					return "", fmt.Errorf("previous container log unavailable: %v", err)
				}
				return "", err
			}
			defer stream.Close()
			b, err := io.ReadAll(io.LimitReader(stream, readOutputCap))
			if err != nil {
				return "", err
			}
			out := strings.TrimRight(string(b), "\n")
			if out == "" {
				out = "(no output)"
			}
			which := "current"
			if p.Previous {
				which = "previous (crashed) container"
			}
			return fmt.Sprintf("[%s, last %d lines]\n%s", which, p.TailLines, out), nil
		}},
	}
}

/* ── run_debug_pod ──────────────────────────────────────────────────────── */

type debugPodParams struct {
	Image   string   `json:"image"`
	Command []string `json:"command"`
	PVCName string   `json:"pvcName"`  // optional: read-only unter /pvc
	Node    string   `json:"nodeName"` // optional: Node-Pinning (Hardware-Verdacht)
	Memory  string   `json:"memory"`   // Limit, default 512Mi
	CPU     string   `json:"cpu"`      // Limit, default 500m
}

// debugPodName: deterministisch je Action — Cleanup/Undo finden den Pod wieder.
func debugPodName(a Action) string {
	id := a.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return "rp-debug-" + id
}

func buildDebugPod(a Action, p debugPodParams) *corev1.Pod {
	mem := p.Memory
	if mem == "" {
		mem = "512Mi"
	}
	cpu := p.CPU
	if cpu == "" {
		cpu = "500m"
	}
	limits := corev1.ResourceList{}
	if q, err := resource.ParseQuantity(mem); err == nil {
		limits[corev1.ResourceMemory] = q
	}
	if q, err := resource.ParseQuantity(cpu); err == nil {
		limits[corev1.ResourceCPU] = q
	}
	no := false
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      debugPodName(a),
			Namespace: a.TargetNamespace,
			Labels:    map[string]string{"rocketplane.io/debug-pod": "true"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			NodeName:      p.Node,
			// Kein SA-Token, keine Privilegien — ein Probe-Pod ist eine Sonde,
			// kein Einfallstor.
			AutomountServiceAccountToken: &no,
			Tolerations:                  []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Containers: []corev1.Container{{
				Name:      "probe",
				Image:     p.Image,
				Command:   p.Command,
				Resources: corev1.ResourceRequirements{Limits: limits},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &no,
				},
			}},
		},
	}
	if p.PVCName != "" {
		pod.Spec.Volumes = []corev1.Volume{{
			Name: "pvc",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: p.PVCName, ReadOnly: true},
			},
		}}
		pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "pvc", MountPath: "/pvc", ReadOnly: true}}
	}
	return pod
}

func (r *Runner) planRunDebugPod(a Action) []step {
	var p debugPodParams
	name := debugPodName(a)
	return []step{
		{name: "create", run: func(ctx context.Context, _ func(string)) (string, error) {
			if err := json.Unmarshal(a.Params, &p); err != nil || p.Image == "" || len(p.Command) == 0 {
				return "", fmt.Errorf("run_debug_pod requires params.image and params.command (argv)")
			}
			// Reste eines früheren Laufs derselben Action wegräumen.
			_ = r.clientset.CoreV1().Pods(a.TargetNamespace).Delete(ctx, name, metav1.DeleteOptions{})
			if _, err := r.clientset.CoreV1().Pods(a.TargetNamespace).Create(ctx, buildDebugPod(a, p), metav1.CreateOptions{FieldManager: "rocketplane-agent"}); err != nil && !apierrors.IsAlreadyExists(err) {
				return "", err
			}
			return "probe pod " + name + " created", nil
		}},
		{name: "run", run: func(ctx context.Context, report func(string)) (string, error) {
			t := time.NewTicker(observeEvery)
			defer t.Stop()
			for {
				pod, err := r.clientset.CoreV1().Pods(a.TargetNamespace).Get(ctx, name, metav1.GetOptions{})
				if err == nil {
					if len(pod.Status.ContainerStatuses) > 0 {
						cs := pod.Status.ContainerStatuses[0]
						if term := cs.State.Terminated; term != nil {
							return fmt.Sprintf("probe finished: exit %d (%s)", term.ExitCode, orDash2(term.Reason)), nil
						}
						if stuck := podStuckReason(pod); stuck != "" && !strings.Contains(stuck, "CrashLoopBackOff") {
							return "", fmt.Errorf("probe cannot start — %s", stuck)
						}
					}
					report(string(pod.Status.Phase))
				}
				select {
				case <-ctx.Done():
					return "", fmt.Errorf("probe did not finish before the timeout — use params.timeoutSeconds for longer probes")
				case <-t.C:
				}
			}
		}},
		{name: "logs", run: func(ctx context.Context, _ func(string)) (string, error) {
			tail := int64(200)
			stream, err := r.clientset.CoreV1().Pods(a.TargetNamespace).GetLogs(name, &corev1.PodLogOptions{TailLines: &tail}).Stream(ctx)
			if err != nil {
				return "(no logs: " + err.Error() + ")", nil
			}
			defer stream.Close()
			b, _ := io.ReadAll(io.LimitReader(stream, readOutputCap))
			out := strings.TrimRight(string(b), "\n")
			if out == "" {
				out = "(no output)"
			}
			return out, nil
		}},
		{name: "cleanup", run: func(ctx context.Context, _ func(string)) (string, error) {
			if err := r.clientset.CoreV1().Pods(a.TargetNamespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return "", err
			}
			return "probe pod removed", nil
		}},
	}
}

func orDash2(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

/* ── list_events ────────────────────────────────────────────────────────── */

// planListEvents: ALLE jüngsten Events eines Namespaces (nicht nur die eines
// Objekts) — „was ist um 17:30 im Namespace passiert?". Warnings zuerst.
func (r *Runner) planListEvents(a Action) []step {
	return []step{
		{name: "events", run: func(ctx context.Context, _ func(string)) (string, error) {
			var p struct {
				WarningsOnly bool `json:"warningsOnly"`
				Limit        int  `json:"limit"`
			}
			_ = json.Unmarshal(a.Params, &p)
			if p.Limit <= 0 || p.Limit > 100 {
				p.Limit = 40
			}
			ns := a.TargetName
			if ns == "-" {
				ns = "" // alle Namespaces
			}
			list, err := r.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{Limit: 1000})
			if err != nil {
				return "", err
			}
			evs := list.Items
			if p.WarningsOnly {
				filtered := evs[:0]
				for i := range evs {
					if evs[i].Type == corev1.EventTypeWarning {
						filtered = append(filtered, evs[i])
					}
				}
				evs = filtered
			}
			sortEventsByTime(evs)
			if len(evs) > p.Limit {
				evs = evs[:p.Limit]
			}
			if len(evs) == 0 {
				return "no events", nil
			}
			var b strings.Builder
			for i := range evs {
				e := &evs[i]
				glyph := "·"
				if e.Type == corev1.EventTypeWarning {
					glyph = "▲"
				}
				msg := strings.TrimSpace(e.Message)
				if len(msg) > 160 {
					msg = msg[:160] + "…"
				}
				cnt := ""
				if e.Count > 1 {
					cnt = fmt.Sprintf(" (x%d)", e.Count)
				}
				fmt.Fprintf(&b, "%s %s %s %s/%s %s%s: %s\n", glyph, eventTime(e).Format("15:04:05"), e.Namespace, e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Reason, cnt, msg)
			}
			return strings.TrimRight(b.String(), "\n"), nil
		}},
	}
}

func eventTime(e *corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if e.EventTime.Time != (time.Time{}) {
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}

func sortEventsByTime(evs []corev1.Event) {
	for i := 1; i < len(evs); i++ { // insertion sort, Listen sind klein
		for j := i; j > 0 && eventTime(&evs[j]).After(eventTime(&evs[j-1])); j-- {
			evs[j], evs[j-1] = evs[j-1], evs[j]
		}
	}
}
