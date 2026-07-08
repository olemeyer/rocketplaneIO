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
	"k8s.io/apimachinery/pkg/types"
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

// step ist ein Glied der Ablauf-Kette. run meldet Zwischenstände über report
// und kehrt erst zurück, wenn der Schritt abgeschlossen (oder gescheitert) ist.
type step struct {
	name string
	run  func(ctx context.Context, report func(detail string)) (string, error)
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

// execute fährt die Step-Kette einer Aktion und meldet den Ablauf live.
func (r *Runner) execute(ctx context.Context, a Action) {
	// Custom-Workflows (Starlark) erzeugen ihre Steps dynamisch.
	if a.Kind == "script" {
		r.executeScript(ctx, a)
		return
	}
	// timeoutSeconds: JEDE Action akzeptiert einen eigenen Timeout (Copilot/UI
	// steuern ihn je Situation), hart geclampt — die Obergrenze gehört dem Agenten.
	timeout := monitorTimeout
	var tp struct {
		TimeoutSeconds int `json:"timeoutSeconds"`
	}
	if len(a.Params) > 0 && json.Unmarshal(a.Params, &tp) == nil && tp.TimeoutSeconds > 0 {
		timeout = time.Duration(tp.TimeoutSeconds) * time.Second
		if timeout < 10*time.Second {
			timeout = 10 * time.Second
		}
		if timeout > 30*time.Minute {
			timeout = 30 * time.Minute
		}
	}
	ctx, cancelFn := context.WithTimeout(ctx, timeout)
	defer cancelFn()

	// Cancel kommt auf zwei Wegen: SOFORT als SSE-Signal (Cancel-Registry,
	// andere Goroutine — daher atomic) oder als Antwort auf Progress-Reports
	// (Fallback ohne Stream). Das Kompensations-Wissen wird VOR der Mutation
	// gesnapshottet (prevReplicas, Vorher-Image, HPA-Grenzen …) — prepareUndo
	// kennt je Kind das Gegenteil.
	var cancelled atomic.Bool
	requestCancel := func() {
		if cancelled.CompareAndSwap(false, true) {
			cancelFn() // laufende Observer brechen ab
		}
	}
	r.registerCancel(a.ID, requestCancel)
	defer r.unregisterCancel(a.ID)
	undoDesc, undoFn := r.prepareUndo(ctx, a)
	revertSpec := r.prepareRevert(ctx, a)  // inverse Action mit VOR-Zustand (für Revert-später)
	snapshot := r.prepareSnapshot(ctx, a) // generischer Before-Snapshot (Audit + restore)

	steps := r.plan(a)
	states := make([]stepState, len(steps))
	for i, s := range steps {
		states[i] = stepState{Name: s.name, Status: "pending"}
	}

	lastSent := time.Time{}
	push := func(progress string, force bool) {
		if !force && time.Since(lastSent) < progressMinGap {
			return
		}
		lastSent = time.Now()
		if r.report(ctx, a.ID, "running", "", progress, states) {
			requestCancel()
		}
		if r.onExecuted != nil {
			r.onExecuted() // Topologie-Burst: die UI sieht Pods kommen/gehen
		}
	}

	finish := func(status, result string) {
		log.Printf("actions: %s %s/%s → %s (%s)", a.Kind, a.TargetNamespace, a.TargetName, status, result)
		var rev json.RawMessage
		if status == "succeeded" {
			rev = revertSpec // nur ein ERFOLG hat einen sinnvollen Revert
		}
		r.reportFull(ctx, a.ID, status, result, "", states, rev, snapshot)
		if r.onExecuted != nil {
			r.onExecuted()
		}
	}

	// rollback: kompensiert nach Cancel/Timeout über den vor der Mutation
	// vorbereiteten undoFn (LIFO ist hier trivial — native Aktionen mutieren
	// genau einen Handgriff; die Mehrschritt-Kompensation lebt in script.go).
	rollback := func(reason string) {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer rcancel()
		if undoFn == nil {
			finish("cancelled", reason+" — nothing to undo")
			return
		}
		states = append(states, stepState{Name: "rollback", Status: "running", Detail: undoDesc})
		idx := len(states) - 1
		if err := undoFn(rctx); err != nil {
			states[idx].Status = "failed"
			states[idx].Detail = undoDesc + " — " + err.Error()
			finish("cancelled", reason+" — rollback FAILED: "+err.Error())
			return
		}
		states[idx].Status = "ok"
		finish("cancelled", reason+" — "+undoDesc)
	}

	var finalResult string
	for i, s := range steps {
		if cancelled.Load() {
			rollback("cancelled by user")
			return
		}
		states[i].Status = "running"
		push(s.name+"…", true)
		detail, err := s.run(ctx, func(d string) {
			states[i].Detail = d
			push(fmt.Sprintf("%s: %s", s.name, d), false)
		})
		if err != nil {
			if cancelled.Load() {
				states[i].Status = "failed"
				states[i].Detail = "cancelled"
				rollback("cancelled by user")
				return
			}
			if ctx.Err() == context.DeadlineExceeded {
				states[i].Status = "failed"
				states[i].Detail = "timeout"
				rollback("timeout after " + timeout.String())
				return
			}
			states[i].Status = "failed"
			if states[i].Detail == "" {
				states[i].Detail = err.Error()
			}
			finish("failed", fmt.Sprintf("%s failed: %v", s.name, err))
			return
		}
		states[i].Status = "ok"
		if detail != "" {
			states[i].Detail = detail
		}
		finalResult = states[i].Detail
		push(s.name+" ✓", true)
	}
	if cancelled.Load() {
		rollback("cancelled by user")
		return
	}

	finish("succeeded", finalResult)
}

// plan baut die PIPELINE je Action-Kind. Vollwertig heißt: fertig ist eine
// Aktion erst, wenn die Wirklichkeit auf POD-EBENE stimmt — alte Pods restlos
// weg, jeder neue Pod ready, und das Ganze stabil. Der Workload-Status allein
// (kubectl rollout status) lügt gern: available springt, während Terminating-
// Pods noch abgebaut werden.
func (r *Runner) plan(a Action) []step {
	switch a.Kind {
	case "rollout_restart":
		// Snapshot der alten Pod-UIDs wird zwischen den Steps geteilt.
		var oldPods map[string]string // uid → name
		return []step{
			{name: "snapshot", run: func(ctx context.Context, _ func(string)) (string, error) {
				pods, err := r.workloadPods(ctx, a)
				if err != nil {
					return "", err
				}
				oldPods = make(map[string]string, len(pods))
				for _, p := range pods {
					oldPods[string(p.UID)] = p.Name
				}
				return fmt.Sprintf("%d pods before restart", len(oldPods)), nil
			}},
			{name: "trigger", run: func(ctx context.Context, _ func(string)) (string, error) {
				return r.triggerRestart(ctx, a)
			}},
			{name: "rollout", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.observeRollout(ctx, a, report)
			}},
			{name: "drain old", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.observeOldGone(ctx, a, oldPods, report)
			}},
			{name: "pods ready", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.observeAllPodsReady(ctx, a, report)
			}},
			{name: "verify", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.verifyStable(ctx, a, report)
			}},
		}
	case "scale":
		return []step{
			{name: "trigger", run: func(ctx context.Context, _ func(string)) (string, error) {
				return r.triggerScale(ctx, a)
			}},
			{name: "scale", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.observeScale(ctx, a, report)
			}},
			{name: "pods settled", run: func(ctx context.Context, report func(string)) (string, error) {
				// exakt desired Pods, alle ready, keiner fährt mehr raus
				return r.observeAllPodsReady(ctx, a, report)
			}},
			{name: "verify", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.verifyStable(ctx, a, report)
			}},
		}
	case "cordon":
		return r.planCordon(a, true)
	case "uncordon":
		return r.planCordon(a, false)
	case "drain":
		return r.planDrain(a)
	case "rollout_undo":
		return r.planRolloutUndo(a)
	case "set_image":
		return r.planSetImage(a)
	case "rollout_pause":
		return r.planPause(a, true)
	case "rollout_resume":
		return r.planPause(a, false)
	case "hpa_set":
		return r.planHPASet(a)
	case "cronjob_trigger":
		return r.planCronjobTrigger(a)
	case "cronjob_suspend":
		return r.planCronjobSuspend(a, true)
	case "cronjob_resume":
		return r.planCronjobSuspend(a, false)
	case "cleanup_pods":
		return r.planCleanupPods(a)
	case "node_taint":
		return r.planTaint(a, false)
	case "node_untaint":
		return r.planTaint(a, true)
	case "pod_events":
		return r.planPodEvents(a)
	case "debug_bundle":
		return r.planDebugBundle(a)
	case "annotate":
		return r.planMetaSet(a, "annotations")
	case "set_label":
		return r.planMetaSet(a, "labels")
	case "patch_configmap":
		return r.planPatchConfigmap(a)
	case "set_env":
		return r.planSetEnv(a)
	case "set_resources":
		return r.planSetResources(a)
	case "statefulset_partition":
		return r.planStatefulsetPartition(a)
	case "hpa_toggle":
		return r.planHPAToggle(a)
	case "rollout_to_revision":
		return r.planRolloutToRevision(a)
	case "evict_pod":
		return r.planEvictPod(a)
	case "cleanup_jobs":
		return r.planCleanupJobs(a)
	case "rollout_history":
		return r.planRolloutHistory(a)
	case "drain_preview":
		return r.planDrainPreview(a)
	case "get_resource":
		return r.planGetResource(a)
	case "describe_resource":
		return r.planDescribeResource(a)
	case "get_secret":
		return r.planGetSecret(a)
	case "helm_releases":
		return r.planHelmReleases(a)
	case "exec_readonly":
		return r.planExecReadonly(a)
	case "patch_secret":
		return r.planPatchSecret(a)
	case "create_configmap":
		return r.planCreateConfigmap(a)
	case "delete_configmap":
		return r.planDeleteConfigmap(a)
	case "pdb_set":
		return r.planPDBSet(a)
	case "patch_resource":
		return r.planPatchResource(a)
	case "pvc_expand":
		return r.planPVCExpand(a)
	case "delete_job":
		return r.planDeleteJob(a)
	case "restore_resource":
		return r.planRestoreResource(a)
	case "pod_logs":
		return r.planPodLogs(a)
	case "run_debug_pod":
		return r.planRunDebugPod(a)
	case "list_events":
		return r.planListEvents(a)
	case "delete_pod":
		// Alte Pod-UID zwischen den Steps teilen: bei StatefulSets heißt der
		// Ersatz-Pod GENAUSO (clickhouse-0) — nur die UID unterscheidet alt/neu.
		var oldUID string
		return []step{
			{name: "delete", run: func(ctx context.Context, _ func(string)) (string, error) {
				if p, err := r.clientset.CoreV1().Pods(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{}); err == nil {
					oldUID = string(p.UID)
				}
				err := r.clientset.CoreV1().Pods(a.TargetNamespace).Delete(ctx, a.TargetName, metav1.DeleteOptions{})
				if err != nil {
					return "", err
				}
				return "pod deletion requested", nil
			}},
			{name: "drain", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.observePodGone(ctx, a, report)
			}},
			{name: "recreate", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.observeReplacement(ctx, a, oldUID, report)
			}},
			{name: "verify", run: func(ctx context.Context, report func(string)) (string, error) {
				return r.verifySiblingsStable(ctx, a, report)
			}},
		}
	default:
		return []step{{name: "reject", run: func(context.Context, func(string)) (string, error) {
			return "", fmt.Errorf("unknown action kind %q", a.Kind)
		}}}
	}
}

/* ── Undo-Vorbereitung (Snapshot VOR der Mutation) ──────────────────────── */

// prepareUndo snapshottet den Kompensations-Handgriff, BEVOR die Aktion
// mutiert. Rückgabe ("", nil) heißt: nicht rückrollbar (z. B. rollout_undo,
// delete_pod, cleanup_pods — deren Wirkung ist selbst schon final). Jeder neue
// mutierende Kind, der ein sauberes Gegenteil hat, gehört hier registriert.
func (r *Runner) prepareUndo(ctx context.Context, a Action) (string, func(context.Context) error) {
	switch a.Kind {
	case "scale":
		st, err := r.readState(ctx, a)
		if err != nil {
			return "", nil
		}
		prev := st.desired
		return fmt.Sprintf("rolled back to %d replicas", prev), func(rctx context.Context) error {
			params, _ := json.Marshal(map[string]int32{"replicas": prev})
			if _, err := r.triggerScale(rctx, Action{TargetNamespace: a.TargetNamespace, TargetKind: a.TargetKind, TargetName: a.TargetName, Params: params}); err != nil {
				return err
			}
			_, err := r.observeScale(rctx, a, func(string) {})
			return err
		}
	case "cordon", "drain":
		return "node uncordoned", func(rctx context.Context) error {
			_, err := r.setUnschedulable(rctx, a.TargetName, false)
			return err
		}
	case "set_image":
		prev, err := r.currentImages(ctx, a)
		if err != nil || len(prev) == 0 {
			return "", nil
		}
		return "restored previous image", func(rctx context.Context) error {
			return r.applyImages(rctx, a, prev)
		}
	case "hpa_set":
		min, max, err := r.currentHPABounds(ctx, a)
		if err != nil {
			return "", nil
		}
		return fmt.Sprintf("restored HPA bounds %d–%d", min, max), func(rctx context.Context) error {
			return r.patchHPABounds(rctx, a.TargetNamespace, a.TargetName, min, max)
		}
	case "rollout_pause":
		return "rollout resumed", func(rctx context.Context) error {
			return r.setPaused(rctx, a, false)
		}
	case "cronjob_suspend":
		return "cronjob resumed", func(rctx context.Context) error {
			return r.setCronjobSuspend(rctx, a, false)
		}
	case "cronjob_resume":
		return "cronjob suspended", func(rctx context.Context) error {
			return r.setCronjobSuspend(rctx, a, true)
		}
	case "node_taint":
		return "taint removed", func(rctx context.Context) error {
			return r.applyTaint(rctx, a, true)
		}
	case "annotate":
		return r.undoMetaSet(ctx, a, "annotations")
	case "set_label":
		return r.undoMetaSet(ctx, a, "labels")
	case "patch_configmap":
		return r.undoPatchConfigmap(ctx, a)
	case "set_env":
		return r.undoContainerTemplate(ctx, a, "env")
	case "set_resources":
		return r.undoContainerTemplate(ctx, a, "resources")
	case "statefulset_partition":
		return r.undoPartition(ctx, a)
	case "rollout_to_revision":
		return r.undoTemplateReplace(ctx, a)
	case "patch_secret":
		return r.undoPatchSecret(ctx, a)
	case "create_configmap":
		return "created configmap deleted again", func(rctx context.Context) error {
			return r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Delete(rctx, a.TargetName, metav1.DeleteOptions{})
		}
	case "delete_configmap":
		cm, err := r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return "", nil
		}
		saved := cm.DeepCopy()
		saved.ObjectMeta = metav1.ObjectMeta{Name: cm.Name, Namespace: cm.Namespace, Labels: cm.Labels, Annotations: cm.Annotations}
		return "configmap recreated from before-state", func(rctx context.Context) error {
			_, err := r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Create(rctx, saved, metav1.CreateOptions{FieldManager: "rocketplane-agent"})
			return err
		}
	case "pdb_set":
		return r.undoPDBSet(ctx, a)
	case "patch_resource", "restore_resource":
		// Generisch: Before-Objekt sichern und bei Fehlschlag per SSA re-applyen.
		return r.undoGenericApply(ctx, a)
	case "run_debug_pod":
		// Cancel/Timeout überspringt den cleanup-Step — der Undo räumt auf.
		name := debugPodName(a)
		return "probe pod removed", func(rctx context.Context) error {
			err := r.clientset.CoreV1().Pods(a.TargetNamespace).Delete(rctx, name, metav1.DeleteOptions{})
			if err != nil && strings.Contains(err.Error(), "not found") {
				return nil
			}
			return err
		}
	}
	return "", nil
}

// undoPatchSecret sichert den Vorher-Wert des EINEN Keys (Key-Level-Inverse).
func (r *Runner) undoPatchSecret(ctx context.Context, a Action) (string, func(context.Context) error) {
	var p patchSecretParams
	if err := json.Unmarshal(a.Params, &p); err != nil || p.Key == "" {
		return "", nil
	}
	sec, err := r.clientset.CoreV1().Secrets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
	if err != nil {
		return "", nil
	}
	prev, existed := sec.Data[p.Key]
	prevCopy := append([]byte(nil), prev...)
	return fmt.Sprintf("restored previous state of key %q", p.Key), func(rctx context.Context) error {
		cur, err := r.clientset.CoreV1().Secrets(a.TargetNamespace).Get(rctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if cur.Data == nil {
			cur.Data = map[string][]byte{}
		}
		if existed {
			cur.Data[p.Key] = prevCopy
		} else {
			delete(cur.Data, p.Key)
		}
		_, err = r.clientset.CoreV1().Secrets(a.TargetNamespace).Update(rctx, cur, metav1.UpdateOptions{FieldManager: "rocketplane-agent"})
		return err
	}
}

// undoPDBSet sichert die Vorher-Grenzen des PDB.
func (r *Runner) undoPDBSet(ctx context.Context, a Action) (string, func(context.Context) error) {
	pdb, err := r.clientset.PolicyV1().PodDisruptionBudgets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
	if err != nil {
		return "", nil
	}
	prevMin := pdb.Spec.MinAvailable
	prevMax := pdb.Spec.MaxUnavailable
	return "restored previous PDB bounds", func(rctx context.Context) error {
		cur, err := r.clientset.PolicyV1().PodDisruptionBudgets(a.TargetNamespace).Get(rctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		cur.Spec.MinAvailable = prevMin
		cur.Spec.MaxUnavailable = prevMax
		_, err = r.clientset.PolicyV1().PodDisruptionBudgets(a.TargetNamespace).Update(rctx, cur, metav1.UpdateOptions{FieldManager: "rocketplane-agent"})
		return err
	}
}

// undoGenericApply sichert das Before-Objekt generisch und stellt es per
// Server-Side-Apply wieder her (patch_resource/restore_resource-Rollback).
func (r *Runner) undoGenericApply(ctx context.Context, a Action) (string, func(context.Context) error) {
	if r.dyn == nil {
		return "", nil
	}
	u, err := r.getUnstructured(ctx, a.TargetKind, a.TargetNamespace, a.TargetName)
	if err != nil {
		return "", nil
	}
	before, err := json.Marshal(stripForSnapshot(u))
	if err != nil {
		return "", nil
	}
	gvr := kindGVR[a.TargetKind]
	return "before-state re-applied", func(rctx context.Context) error {
		_, err := r.dyn.Resource(gvr).Namespace(a.TargetNamespace).Patch(rctx, a.TargetName, types.ApplyPatchType, before, metav1.PatchOptions{FieldManager: "rocketplane-agent", Force: boolPtr(true)})
		return err
	}
}

/* ── Trigger ────────────────────────────────────────────────────────────── */

func (r *Runner) triggerRestart(ctx context.Context, a Action) (string, error) {
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		time.Now().UTC().Format(time.RFC3339)))
	opts := metav1.PatchOptions{FieldManager: "rocketplane-agent"}
	var err error
	switch a.TargetKind {
	case "Deployment":
		_, err = r.clientset.AppsV1().Deployments(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opts)
	case "StatefulSet":
		_, err = r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opts)
	case "DaemonSet":
		_, err = r.clientset.AppsV1().DaemonSets(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opts)
	default:
		return "", fmt.Errorf("rollout_restart unsupported for %s", a.TargetKind)
	}
	if err != nil {
		return "", err
	}
	return "restart annotation patched", nil
}

func (r *Runner) triggerScale(ctx context.Context, a Action) (string, error) {
	var p struct {
		Replicas *int32 `json:"replicas"`
	}
	if err := json.Unmarshal(a.Params, &p); err != nil || p.Replicas == nil {
		return "", fmt.Errorf("missing params.replicas")
	}
	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, *p.Replicas))
	opts := metav1.PatchOptions{FieldManager: "rocketplane-agent"}
	var err error
	switch a.TargetKind {
	case "Deployment":
		_, err = r.clientset.AppsV1().Deployments(a.TargetNamespace).Patch(ctx, a.TargetName, types.MergePatchType, patch, opts)
	case "StatefulSet":
		_, err = r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Patch(ctx, a.TargetName, types.MergePatchType, patch, opts)
	default:
		return "", fmt.Errorf("scale unsupported for %s", a.TargetKind)
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("replicas set to %d", *p.Replicas), nil
}

/* ── Observe: Zielzustand je Workload-Art ───────────────────────────────── */

// workloadState liest den Rollout-Zustand des Ziel-Workloads.
type workloadState struct {
	desired, updated, ready, available int32
	generationCaughtUp                 bool
}

func (r *Runner) readState(ctx context.Context, a Action) (workloadState, error) {
	return r.readStateOf(ctx, a.TargetNamespace, a.TargetKind, a.TargetName)
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

func (st workloadState) String() string {
	return fmt.Sprintf("%d/%d updated · %d/%d available", st.updated, st.desired, st.available, st.desired)
}

// observeRollout wartet, bis die neue Generation vollständig ausgerollt ist
// (das ist `kubectl rollout status`) — alte Pods raus, neue Pods ready.
func (r *Runner) observeRollout(ctx context.Context, a Action, report func(string)) (string, error) {
	return r.observe(ctx, a, report, func(st workloadState) bool { return st.rolledOut() })
}

// observeScale wartet, bis ready == desired (hoch UND runter).
func (r *Runner) observeScale(ctx context.Context, a Action, report func(string)) (string, error) {
	return r.observe(ctx, a, report, func(st workloadState) bool {
		return st.generationCaughtUp && st.ready == st.desired && st.available == st.desired
	})
}

func (r *Runner) observe(ctx context.Context, a Action, report func(string), done func(workloadState) bool) (string, error) {
	t := time.NewTicker(observeEvery)
	defer t.Stop()
	var last workloadState
	for {
		st, err := r.readState(ctx, a)
		if err == nil {
			last = st
			report(st.String())
			if done(st) {
				return st.String(), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for target state (last: %s)", last.String())
		case <-t.C:
		}
	}
}

/* ── Pod-Ebene: die Wirklichkeit hinter dem Workload-Status ─────────────── */

// workloadPods listet die Pods des Ziel-Workloads über dessen Label-Selector
// (die kubectl-Wahrheit — Owner-unabhängig, überlebt ReplicaSet-Wechsel).
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

// observeOldGone wartet, bis ALLE Pods aus dem Vorher-Snapshot restlos weg
// sind — nicht nur Terminating, sondern verschwunden.
func (r *Runner) observeOldGone(ctx context.Context, a Action, oldPods map[string]string, report func(string)) (string, error) {
	if len(oldPods) == 0 {
		return "no old pods to drain", nil
	}
	t := time.NewTicker(observeEvery)
	defer t.Stop()
	for {
		pods, err := r.workloadPods(ctx, a)
		if err == nil {
			remaining := []string{}
			for i := range pods {
				if name, isOld := oldPods[string(pods[i].UID)]; isOld {
					remaining = append(remaining, name)
				}
			}
			if len(remaining) == 0 {
				return fmt.Sprintf("all %d old pods gone", len(oldPods)), nil
			}
			report(fmt.Sprintf("%d/%d old pods still terminating (%s)", len(remaining), len(oldPods), strings.Join(remaining, ", ")))
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout draining old pods")
		case <-t.C:
		}
	}
}

// observeAllPodsReady wartet, bis exakt desired Pods existieren, JEDER ready
// ist und keiner mehr rausfährt.
func (r *Runner) observeAllPodsReady(ctx context.Context, a Action, report func(string)) (string, error) {
	t := time.NewTicker(observeEvery)
	defer t.Stop()
	var last podsSummary
	for {
		st, errSt := r.readState(ctx, a)
		pods, errPods := r.workloadPods(ctx, a)
		if errSt == nil && errPods == nil {
			last = summarize(pods)
			report(last.String())
			if last.terminating == 0 && int32(last.total) == st.desired && int32(last.ready) == st.desired {
				return last.String(), nil
			}
			// Fail fast: ein endgültig feststeckender Pod wird nie ready —
			// der Grund ist die Antwort, nicht der Timeout.
			for i := range pods {
				if stuck := podStuckReason(&pods[i]); stuck != "" {
					return "", fmt.Errorf("pods will not become ready — %s", stuck)
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for pods (last: %s)", last.String())
		case <-t.C:
		}
	}
}

// verifyStable prüft, dass Workload-Status UND Pod-Ebene settleFor lang
// stabil stehen — erst dann gilt der Ablauf als abgeschlossen.
func (r *Runner) verifyStable(ctx context.Context, a Action, report func(string)) (string, error) {
	deadline := time.Now().Add(settleFor)
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		st, err := r.readState(ctx, a)
		if err != nil {
			return "", err
		}
		pods, err := r.workloadPods(ctx, a)
		if err != nil {
			return "", err
		}
		sum := summarize(pods)
		statusOK := st.generationCaughtUp && st.ready == st.desired && st.available == st.desired
		podsOK := sum.terminating == 0 && int32(sum.total) == st.desired && int32(sum.ready) == st.desired
		if !statusOK || !podsOK {
			// Zustand kippte zurück → Stabilitätsfenster neu beginnen.
			deadline = time.Now().Add(settleFor)
			report(fmt.Sprintf("unstable: %s · %s", st.String(), sum.String()))
		} else {
			report(fmt.Sprintf("stable · %s", sum.String()))
			if time.Now().After(deadline) {
				return fmt.Sprintf("verified: %s, %s", st.String(), sum.String()), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout verifying stability")
		case <-t.C:
		}
	}
}

// verifySiblingsStable — verify für delete_pod (kein Workload-Objekt bekannt):
// alle Pods mit dem ReplicaSet-Präfix des Ziels müssen ready sein, keiner
// terminating, stabil über settleFor.
func (r *Runner) verifySiblingsStable(ctx context.Context, a Action, report func(string)) (string, error) {
	prefix := a.TargetName
	if i := strings.LastIndex(prefix, "-"); i > 0 {
		prefix = prefix[:i+1]
	}
	deadline := time.Now().Add(settleFor)
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		list, err := r.clientset.CoreV1().Pods(a.TargetNamespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", err
		}
		sib := []corev1.Pod{}
		for i := range list.Items {
			if strings.HasPrefix(list.Items[i].Name, prefix) {
				sib = append(sib, list.Items[i])
			}
		}
		sum := summarize(sib)
		if sum.terminating > 0 || sum.ready != sum.total-sum.terminating || sum.total == 0 {
			deadline = time.Now().Add(settleFor)
			report("unstable: " + sum.String())
		} else {
			report("stable · " + sum.String())
			if time.Now().After(deadline) {
				return "verified: " + sum.String(), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout verifying stability")
		case <-t.C:
		}
	}
}

/* ── delete_pod: drain + replacement ────────────────────────────────────── */

func (r *Runner) observePodGone(ctx context.Context, a Action, report func(string)) (string, error) {
	t := time.NewTicker(observeEvery)
	defer t.Stop()
	for {
		p, err := r.clientset.CoreV1().Pods(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			// NotFound = weg — genau das wollen wir.
			return "old pod terminated", nil
		}
		if p.DeletionTimestamp != nil {
			report("terminating…")
		} else {
			report("waiting for termination")
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for pod termination")
		case <-t.C:
		}
	}
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

// observeReplacement wartet auf einen NEUEN ready-Pod desselben Workloads.
// Deployment/ReplicaSet: neuer Name mit demselben Präfix. StatefulSet: der
// GLEICHE Name mit neuer UID (oldUID unterscheidet alt/neu). Steckt der
// Ersatz endgültig fest (CrashLoop/ImagePull), schlägt der Step sofort mit
// dem Grund fehl statt den Timeout auszusitzen.
func (r *Runner) observeReplacement(ctx context.Context, a Action, oldUID string, report func(string)) (string, error) {
	prefix := a.TargetName
	if i := strings.LastIndex(prefix, "-"); i > 0 {
		prefix = prefix[:i+1]
	}
	t := time.NewTicker(observeEvery)
	defer t.Stop()
	for {
		pods, err := r.clientset.CoreV1().Pods(a.TargetNamespace).List(ctx, metav1.ListOptions{})
		if err == nil {
			for i := range pods.Items {
				p := &pods.Items[i]
				isSameNameNew := p.Name == a.TargetName && string(p.UID) != oldUID
				if (!isSameNameNew && p.Name == a.TargetName) || !strings.HasPrefix(p.Name, prefix) || p.DeletionTimestamp != nil {
					continue
				}
				ready := false
				for _, c := range p.Status.Conditions {
					if c.Type == "Ready" && c.Status == "True" {
						ready = true
					}
				}
				if ready {
					return fmt.Sprintf("replacement %s ready", p.Name), nil
				}
				if stuck := podStuckReason(p); stuck != "" {
					return "", fmt.Errorf("replacement will not become ready — %s", stuck)
				}
				report(fmt.Sprintf("replacement %s starting…", p.Name))
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for replacement pod")
		case <-t.C:
		}
	}
}

/* ── Report ─────────────────────────────────────────────────────────────── */

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
