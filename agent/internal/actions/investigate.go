package actions

// investigate.go — READ-ONLY Sammel-Actions, deren Ergebnis als Step-Details in
// den Feed fließt (kein Mutieren, kein Rollback). Sie ersetzen das Triage-
// Ritual „fünf kubectl-Befehle + Copy-Paste ins Ticket" durch EINEN Klick:
//   pod_events    → die jüngsten Events des Ziels (Scheduling-Fails,
//                   ImagePullBackOff, OOMKilled-Gründe) gebündelt.
//   debug_bundle  → Rollout-Zustand + Container-Statuses (inkl. last-state
//                   OOMKilled/CrashLoop) + Events — der Triage-Snapshot.
//
// Beide brauchen NUR Leserechte (get/list) — der Output landet in stepState.Detail,
// die UI zeigt ihn in der Run-Karte. Alles ist streng gedeckelt (Events/Pods),
// damit das steps-JSON klein bleibt.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	maxBundleEvents = 15
	maxBundlePods   = 12
)

// bundleTargets löst das Ziel in (Namen für Event-Filter, Pods für Statuses) auf.
// Ziel ist ein Pod ODER ein Template-Workload (dann dessen Pods per Selector).
func (r *Runner) bundleTargets(ctx context.Context, a Action) (names []string, pods []corev1.Pod, err error) {
	if a.TargetKind == "Pod" {
		p, err := r.clientset.CoreV1().Pods(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return nil, nil, err
		}
		return []string{a.TargetName}, []corev1.Pod{*p}, nil
	}
	pods, err = r.workloadPods(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	names = []string{a.TargetName}
	for i := range pods {
		names = append(names, pods[i].Name)
	}
	return names, pods, nil
}

// gatherEvents listet Events im Namespace und filtert auf die Ziel-Namen,
// jüngste zuerst, formatiert. Warnungen sind ▲, Normales ·.
func (r *Runner) gatherEvents(ctx context.Context, namespace string, names []string) (string, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	list, err := r.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	evs := make([]corev1.Event, 0, len(list.Items))
	for i := range list.Items {
		if want[list.Items[i].InvolvedObject.Name] {
			evs = append(evs, list.Items[i])
		}
	}
	if len(evs) == 0 {
		return "no events", nil
	}
	sort.Slice(evs, func(i, j int) bool {
		return evs[i].LastTimestamp.Time.After(evs[j].LastTimestamp.Time)
	})
	if len(evs) > maxBundleEvents {
		evs = evs[:maxBundleEvents]
	}
	var b strings.Builder
	for i := range evs {
		e := &evs[i]
		glyph := "·"
		if e.Type == corev1.EventTypeWarning {
			glyph = "▲"
		}
		msg := strings.TrimSpace(e.Message)
		if len(msg) > 140 {
			msg = msg[:140] + "…"
		}
		cnt := ""
		if e.Count > 1 {
			cnt = fmt.Sprintf(" (x%d)", e.Count)
		}
		fmt.Fprintf(&b, "%s %s/%s %s%s: %s\n", glyph, e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Reason, cnt, msg)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// containerStatusLine fasst die Container eines Pods zu einer Triage-Zeile
// zusammen — der springende Punkt ist der last-state (OOMKilled/exit-code) und
// der waiting-reason (CrashLoopBackOff/ImagePullBackOff).
func containerStatusLine(p *corev1.Pod) string {
	parts := make([]string, 0, len(p.Status.ContainerStatuses))
	for i := range p.Status.ContainerStatuses {
		cs := &p.Status.ContainerStatuses[i]
		seg := cs.Name
		switch {
		case cs.State.Waiting != nil:
			seg += " waiting:" + cs.State.Waiting.Reason
		case cs.State.Terminated != nil:
			seg += fmt.Sprintf(" terminated:%s(%d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
		case cs.Ready:
			seg += " ready"
		default:
			seg += " running"
		}
		if cs.RestartCount > 0 {
			seg += fmt.Sprintf(" ↻%d", cs.RestartCount)
		}
		// last-state deckt den EIGENTLICHEN Absturzgrund auf, auch wenn der
		// Container gerade wieder läuft (z. B. OOMKilled vor dem Restart).
		if cs.LastTerminationState.Terminated != nil {
			lt := cs.LastTerminationState.Terminated
			seg += fmt.Sprintf(" last:%s(%d)", lt.Reason, lt.ExitCode)
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return string(p.Status.Phase)
	}
	return strings.Join(parts, " · ")
}

/* ── pod_events ─────────────────────────────────────────────────────────── */

func (r *Runner) planPodEvents(a Action) []step {
	return []step{
		{name: "events", run: func(ctx context.Context, report func(string)) (string, error) {
			names, _, err := r.bundleTargets(ctx, a)
			if err != nil {
				return "", err
			}
			out, err := r.gatherEvents(ctx, a.TargetNamespace, names)
			if err != nil {
				return "", err
			}
			return out, nil
		}},
	}
}

/* ── debug_bundle ───────────────────────────────────────────────────────── */

func (r *Runner) planDebugBundle(a Action) []step {
	return []step{
		{name: "workload", run: func(ctx context.Context, _ func(string)) (string, error) {
			if a.TargetKind == "Pod" {
				return "pod target — see pods step", nil
			}
			st, err := r.readState(ctx, a)
			if err != nil {
				return "", err
			}
			imgs, _ := r.currentImages(ctx, a)
			parts := make([]string, 0, len(imgs))
			for name, img := range imgs {
				parts = append(parts, name+"="+img)
			}
			sort.Strings(parts)
			return fmt.Sprintf("%s · %s", st.String(), strings.Join(parts, ", ")), nil
		}},
		{name: "pods", run: func(ctx context.Context, _ func(string)) (string, error) {
			_, pods, err := r.bundleTargets(ctx, a)
			if err != nil {
				return "", err
			}
			if len(pods) == 0 {
				return "no pods", nil
			}
			if len(pods) > maxBundlePods {
				pods = pods[:maxBundlePods]
			}
			var b strings.Builder
			for i := range pods {
				p := &pods[i]
				fmt.Fprintf(&b, "%s [%s] %s\n", p.Name, p.Status.Phase, containerStatusLine(p))
			}
			return strings.TrimRight(b.String(), "\n"), nil
		}},
		{name: "events", run: func(ctx context.Context, _ func(string)) (string, error) {
			names, _, err := r.bundleTargets(ctx, a)
			if err != nil {
				return "", err
			}
			return r.gatherEvents(ctx, a.TargetNamespace, names)
		}},
	}
}
