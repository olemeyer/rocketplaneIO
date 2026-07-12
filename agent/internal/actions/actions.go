// Package actions empfängt Safe-Actions von der Control-Plane und führt sie
// im Cluster aus (outbound-only: der Agent ÖFFNET alle Verbindungen, niemand
// ruft hinein). Der Live-Kanal ist ein SSE-Stream (stream.go): dispatch-
// Signale stoßen den Claim-Fetch an, cancel-Signale brechen laufende Abläufe
// sofort ab. Ein seltener Fallback-Poll (bzw. der alte 3s-Takt, solange der
// Stream nicht steht) fängt verlorene Signale ab.
//
// Eine Action ist ein ABLAUF, kein Einzelbefehl: eine Step-Kette
// (trigger → observe → verify), deren Fortschritt der Agent live an die
// Control-Plane meldet — von „triggered" über „rollout: 1/3 available" bis
// zum VERIFIZIERTEN Erfolg (oder Timeout/Fehler). Die Step-Engine ist bewusst
// generisch: künftige komplexe Admin-Runbooks sind einfach längere Ketten.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// pollInterval gilt nur, solange der Event-Stream NICHT steht (Fallback
	// auf das alte Poll-Verhalten); mit Stream wird selten gegengeprüft.
	pollInterval       = 3 * time.Second
	streamFallbackPoll = 60 * time.Second
	requestTimeout     = 15 * time.Second
	// monitorTimeout begrenzt den GESAMTEN Ablauf einer Aktion (inkl. Rollout-
	// Beobachtung) — danach failed mit letztem beobachteten Zustand.
	monitorTimeout = 3 * time.Minute
	observeEvery   = 2 * time.Second
	// settleFor: so lange muss der Zielzustand stabil stehen, bevor der Ablauf
	// als verifiziert gilt (fängt Flapping direkt nach dem Rollout ab).
	settleFor = 4 * time.Second
	// progressMinGap drosselt Zwischenstands-POSTs an die Control-Plane.
	progressMinGap = 1200 * time.Millisecond
	maxConcurrent  = 4
)

// Action spiegelt model.Action (nur die Felder, die der Agent braucht).
type Action struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	TargetNamespace string          `json:"targetNamespace"`
	TargetKind      string          `json:"targetKind"`
	TargetName      string          `json:"targetName"`
	Params          json.RawMessage `json:"params"`
}

// stepState ist der an die Control-Plane gemeldete Zustand eines Schritts.
type stepState struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pending|running|ok|failed
	Detail string `json:"detail"`
}

// Runner empfängt (Stream + Fallback-Poll) und führt aus.
type Runner struct {
	baseURL   string
	token     string
	clientset kubernetes.Interface
	// restCfg + dyn: generischer Zugriff (get_resource, patch_resource,
	// restore_resource, Starlark raw_*, exec_readonly). Optional — ohne Config
	// (Unit-Tests mit fake clientset) verweigern die generischen Kinds sauber.
	restCfg *rest.Config
	dyn     dynamic.Interface
	http    *http.Client
	// streamHTTP hat KEIN Client-Timeout — das würde den SSE-Stream nach
	// requestTimeout kappen; tote Verbindungen erkennt der Watchdog (stream.go).
	streamHTTP *http.Client
	sem        chan struct{}
	// onExecuted feuert nach jedem abgeschlossenen Ablauf UND bei jedem
	// Zwischenstand (Topologie-Burst — die UI sieht Pods kommen und gehen).
	onExecuted func()

	// Live-Kanal (stream.go): streamLive streckt den Poll auf den Fallback-
	// Takt, pollNow stößt sofortige Claim-Fetches an (coalesced), cancels
	// bricht laufende Abläufe auf ein cancel-Signal hin sofort ab.
	streamLive atomic.Bool
	pollNow    chan struct{}
	cancelMu   sync.Mutex
	cancels    map[string]func()
}

func New(baseURL, token string, clientset kubernetes.Interface, restCfg *rest.Config, onExecuted func()) *Runner {
	var dyn dynamic.Interface
	if restCfg != nil {
		if d, err := dynamic.NewForConfig(restCfg); err == nil {
			dyn = d
		} else {
			log.Printf("actions: dynamic client unavailable: %v (generic kinds disabled)", err)
		}
	}
	return &Runner{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		clientset:  clientset,
		restCfg:    restCfg,
		dyn:        dyn,
		http:       &http.Client{Timeout: requestTimeout},
		streamHTTP: &http.Client{},
		sem:        make(chan struct{}, maxConcurrent),
		onExecuted: onExecuted,
		pollNow:    make(chan struct{}, 1),
		cancels:    map[string]func(){},
	}
}

// Run hält den Event-Stream und claimt bis ctx abbricht: sofort auf jedes
// dispatch-Signal, dazu ein Sicherheitsnetz-Poll (selten bei stehendem
// Stream, alter 3s-Takt ohne).
func (r *Runner) Run(ctx context.Context) error {
	log.Printf("actions: runner started (stream + fallback poll %s/%s, monitor timeout %s)",
		streamFallbackPoll, pollInterval, monitorTimeout)
	go r.streamLoop(ctx)

	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.pollNow:
		case <-timer.C:
		}
		r.poll(ctx)

		next := pollInterval
		if r.streamLive.Load() {
			next = streamFallbackPoll
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(next)
	}
}

func (r *Runner) poll(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/agent/actions", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.http.Do(req)
	if err != nil {
		return // transient — nächster Tick
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<14))
		return
	}
	var body struct {
		Actions []Action `json:"actions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return
	}
	for _, a := range body.Actions {
		a := a
		select {
		case r.sem <- struct{}{}:
			go func() {
				defer func() { <-r.sem }()
				r.execute(ctx, a)
			}()
		case <-ctx.Done():
			return
		}
	}
}

// execute runs an action on the snapshot substrate — the ONE execution surface.
// Every kind is a Starlark script: a built-in kind is its embedded .star, a custom
// workflow (kind "script") is its own source, and snapshot_restore replays a
// durable capture list. There is no native per-kind path — shown == executed.
func (r *Runner) execute(ctx context.Context, a Action) {
	switch {
	case a.Kind == "snapshot_restore":
		// replay a durable snapshot list (reaper crash-restore or manual revert).
		r.executeSnapshotRestore(ctx, a)
	case a.Kind == "snapshot_script", a.Kind == "script":
		// a custom workflow: the same snapshot surface as a built-in .star or a
		// fork of one — step timeline, durable (encrypted-for-secrets) snapshots,
		// auto-rollback on failure, and a revert.
		r.executeSnapshotScript(ctx, a)
	default:
		// a built-in kind runs as its embedded .star: durable snapshots,
		// auto-rollback on failure, a revert — no native plan() behind it.
		src, ok := builtinSnapshotScript(a.Kind)
		if !ok {
			r.report(ctx, a.ID, "failed", "unknown action kind: "+a.Kind, "", nil)
			return
		}
		r.runSnapshotAction(ctx, a, src, snapshotArgs(a), builtinReversible(a.Kind))
	}
}

// workloadState is a workload's rollout status, read by the snapshot surface's
// wait_rollout/wait_ready observers and the k8s.get summary.
type workloadState struct {
	desired, updated, ready, available int32
	generationCaughtUp                 bool
}

func (r *Runner) readStateOf(ctx context.Context, namespace, kind, name string) (workloadState, error) {
	a := Action{TargetNamespace: namespace, TargetKind: kind, TargetName: name}
	switch a.TargetKind {
	case "Deployment":
		d, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return workloadState{}, err
		}
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		return workloadState{
			desired:            desired,
			updated:            d.Status.UpdatedReplicas,
			ready:              d.Status.ReadyReplicas,
			available:          d.Status.AvailableReplicas,
			generationCaughtUp: d.Status.ObservedGeneration >= d.Generation,
		}, nil
	case "StatefulSet":
		s, err := r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return workloadState{}, err
		}
		desired := int32(1)
		if s.Spec.Replicas != nil {
			desired = *s.Spec.Replicas
		}
		return workloadState{
			desired:            desired,
			updated:            s.Status.UpdatedReplicas,
			ready:              s.Status.ReadyReplicas,
			available:          s.Status.ReadyReplicas,
			generationCaughtUp: s.Status.ObservedGeneration >= s.Generation,
		}, nil
	case "DaemonSet":
		d, err := r.clientset.AppsV1().DaemonSets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return workloadState{}, err
		}
		return workloadState{
			desired:            d.Status.DesiredNumberScheduled,
			updated:            d.Status.UpdatedNumberScheduled,
			ready:              d.Status.NumberReady,
			available:          d.Status.NumberAvailable,
			generationCaughtUp: d.Status.ObservedGeneration >= d.Generation,
		}, nil
	}
	return workloadState{}, fmt.Errorf("unsupported kind %s", a.TargetKind)
}

func (st workloadState) rolledOut() bool {
	return st.generationCaughtUp && st.updated == st.desired && st.available == st.desired
}

func (r *Runner) workloadPods(ctx context.Context, a Action) ([]corev1.Pod, error) {
	var sel *metav1.LabelSelector
	switch a.TargetKind {
	case "Deployment":
		d, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		sel = d.Spec.Selector
	case "StatefulSet":
		s, err := r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		sel = s.Spec.Selector
	case "DaemonSet":
		d, err := r.clientset.AppsV1().DaemonSets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		sel = d.Spec.Selector
	default:
		return nil, fmt.Errorf("no selector for kind %s", a.TargetKind)
	}
	ls, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return nil, err
	}
	list, err := r.clientset.CoreV1().Pods(a.TargetNamespace).List(ctx, metav1.ListOptions{LabelSelector: ls.String()})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func isPodReady(p *corev1.Pod) bool {
	if p.DeletionTimestamp != nil {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// podsSummary zählt den Ist-Zustand: ready / terminating / gesamt.
type podsSummary struct {
	total, ready, terminating int
}

func summarize(pods []corev1.Pod) podsSummary {
	var s podsSummary
	for i := range pods {
		p := &pods[i]
		s.total++
		if p.DeletionTimestamp != nil {
			s.terminating++
			continue
		}
		if isPodReady(p) {
			s.ready++
		}
	}
	return s
}

func (s podsSummary) String() string {
	out := fmt.Sprintf("%d/%d pods ready", s.ready, s.total-s.terminating)
	if s.terminating > 0 {
		out += fmt.Sprintf(" · %d terminating", s.terminating)
	}
	return out
}

// podStuckReason erkennt ENDGÜLTIG feststeckende Pods — Zustände, aus denen
// Warten allein nie herausführt. Statt den Action-Timeout stumpf auszusitzen,
// schlägt der Observer sofort mit dem ECHTEN Grund fehl (fail fast: der Grund
// ist die Diagnose). CrashLoopBackOff erst ab dem 2. Restart — ein einzelner
// Crash direkt nach dem Start (z.B. Warten auf die DB) darf sich fangen.
func podStuckReason(p *corev1.Pod) string {
	for i := range p.Status.ContainerStatuses {
		cs := &p.Status.ContainerStatuses[i]
		w := cs.State.Waiting
		if w == nil {
			continue
		}
		decisive := false
		switch w.Reason {
		case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError", "RunContainerError":
			decisive = true
		case "CrashLoopBackOff":
			decisive = cs.RestartCount >= 2
		}
		if !decisive {
			continue
		}
		msg := fmt.Sprintf("%s: container %s stuck in %s", p.Name, cs.Name, w.Reason)
		if lt := cs.LastTerminationState.Terminated; lt != nil {
			msg += fmt.Sprintf(" (last exit %d %s)", lt.ExitCode, lt.Reason)
		}
		if w.Message != "" {
			m := w.Message
			if len(m) > 160 {
				m = m[:160] + "…"
			}
			msg += " — " + m
		}
		return msg
	}
	return ""
}

// report meldet Zwischenstand oder Endergebnis; die Antwort trägt den
// CANCEL-Wunsch des Users zurück (outbound-only-Rückkanal). Endergebnisse
// nutzen einen Kontext OHNE die Action-Deadline — auch ein Timeout/Cancel
// muss seinen Endstatus noch loswerden.
func (r *Runner) report(ctx context.Context, actionID, status, result, progress string, steps []stepState) bool {
	return r.reportWithRevert(ctx, actionID, status, result, progress, steps, nil)
}

// reportWithRevert: wie report, liefert bei Endergebnissen zusätzlich die
// inverse Katalog-Action (Before-Snapshot) für den Revert-Button der Runs-Seite.
func (r *Runner) reportWithRevert(ctx context.Context, actionID, status, result, progress string, steps []stepState, revert json.RawMessage) bool {
	return r.reportFull(ctx, actionID, status, result, progress, steps, revert, nil)
}

func (r *Runner) reportFull(ctx context.Context, actionID, status, result, progress string, steps []stepState, revert, snapshot json.RawMessage) bool {
	body := map[string]any{
		"status":   status,
		"result":   result,
		"progress": progress,
		"steps":    steps,
	}
	if len(revert) > 0 {
		body["revert"] = revert
	}
	if len(snapshot) > 0 {
		body["snapshot"] = snapshot
	}
	payload, _ := json.Marshal(body)
	attempts := 1
	if status != "running" { // Endergebnisse sind wichtig → Retry + frischer ctx
		attempts = 2
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
	}
	for i := 0; i < attempts; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			r.baseURL+"/api/agent/actions/"+actionID+"/result", strings.NewReader(string(payload)))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+r.token)
		resp, err := r.http.Do(req)
		if err == nil {
			var body struct {
				Cancel bool `json:"cancel"`
			}
			_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<14)).Decode(&body)
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return body.Cancel
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
	log.Printf("actions: failed to report %s for %s", status, actionID)
	return false
}
