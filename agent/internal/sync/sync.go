// Package sync watches cluster namespaces and pushes state to the control-plane.
//
// It runs three things after enrollment (all authenticated with the agent
// Bearer token):
//
//   - a shared Namespace informer; on Add/Update/Delete it debounces (~2s) and
//     then pushes the FULL namespace list (a full-sync — the control-plane
//     upserts present namespaces and removes vanished ones).
//   - the initial full-sync once the informer cache is warm.
//   - a periodic heartbeat (30s) so the control-plane can track last_seen_at.
package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listersappsv1 "k8s.io/client-go/listers/apps/v1"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	debounceDelay     = 2 * time.Second
	heartbeatInterval = 30 * time.Second
	informerResync    = 10 * time.Minute
	requestTimeout    = 15 * time.Second
	pushMaxAttempts   = 4
	pushRetryBackoff  = 2 * time.Second
)

// Syncer pushes namespace state and heartbeats to the control-plane.
type Syncer struct {
	baseURL      string
	token        string
	agentVersion string
	clientset    kubernetes.Interface
	http         *http.Client

	// podLister wird von RunTopology gesetzt (nach Cache-Sync) und vom Log-
	// Collector für das Pod→Workload-Mapping mitgenutzt. podReady signalisiert
	// die Verfügbarkeit (einmalig geschlossen).
	podLister listersv1.PodLister
	podReady  chan struct{}

	// Workload-Lister (Deployments/StatefulSets/DaemonSets) — von RunTopology
	// gesetzt; liefern desired/ready direkt vom Objekt (auch bei 0 Pods).
	depLister listersappsv1.DeploymentLister
	stsLister listersappsv1.StatefulSetLister
	dsLister  listersappsv1.DaemonSetLister

	// Infrastruktur (Nodes/PVCs) + kubelet-Stats-Cache.
	nodeLister listersv1.NodeLister
	pvcLister  listersv1.PersistentVolumeClaimLister
	statsCache map[string]nodeStats

	// topoTrigger stößt einen sofortigen Topologie-Push + Burst an (z.B. nach
	// einer ausgeführten Safe-Action, damit die UI den Rollout live sieht).
	topoTrigger chan struct{}
}

// TriggerTopologySync fordert einen sofortigen Topologie-Push an (coalesced).
func (s *Syncer) TriggerTopologySync() {
	select {
	case s.topoTrigger <- struct{}{}:
	default:
	}
}

// New builds a Syncer. baseURL is the control-plane root, token is the agent
// Bearer token obtained during enrollment.
func New(baseURL, token, agentVersion string, clientset kubernetes.Interface) *Syncer {
	return &Syncer{
		baseURL:      strings.TrimRight(baseURL, "/"),
		token:        token,
		agentVersion: agentVersion,
		clientset:    clientset,
		http:         &http.Client{Timeout: requestTimeout},
		podReady:     make(chan struct{}),
		topoTrigger:  make(chan struct{}, 1),
	}
}

// nsItem is one entry of the namespaces full-sync payload.
type nsItem struct {
	Name   string            `json:"name"`
	UID    string            `json:"uid"`
	Phase  string            `json:"phase"`
	Labels map[string]string `json:"labels"`
}

// Run starts the informer, the debounced push loop and the heartbeat loop, and
// blocks until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) error {
	factory := informers.NewSharedInformerFactory(s.clientset, informerResync)
	nsInformer := factory.Core().V1().Namespaces()
	informer := nsInformer.Informer()
	lister := nsInformer.Lister()

	// Buffered trigger; coalesces bursts of events into a single pending push.
	trigger := make(chan struct{}, 1)
	notify := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}

	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { notify() },
		UpdateFunc: func(oldObj, newObj interface{}) { notify() },
		DeleteFunc: func(obj interface{}) { notify() },
	}); err != nil {
		return fmt.Errorf("add informer event handler: %w", err)
	}

	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("namespace informer cache sync failed")
	}
	log.Printf("sync: namespace informer synced")

	// Initial full-sync once the cache is warm.
	notify()

	go s.debounceLoop(ctx, trigger, lister)
	go s.heartbeatLoop(ctx)

	<-ctx.Done()
	return nil
}

// debounceLoop waits for a trigger, then coalesces further triggers for
// debounceDelay before pushing the full namespace list.
func (s *Syncer) debounceLoop(ctx context.Context, trigger <-chan struct{}, lister listersv1.NamespaceLister) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-trigger:
		}

		timer := time.NewTimer(debounceDelay)
	drain:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-trigger:
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(debounceDelay)
			case <-timer.C:
				break drain
			}
		}

		s.pushNamespaces(ctx, lister)
	}
}

// pushNamespaces lists all namespaces from the informer cache and POSTs the
// full list, retrying a few times on transient failure.
func (s *Syncer) pushNamespaces(ctx context.Context, lister listersv1.NamespaceLister) {
	nsList, err := lister.List(labels.Everything())
	if err != nil {
		log.Printf("sync: list namespaces from cache failed: %v", err)
		return
	}

	items := make([]nsItem, 0, len(nsList))
	for _, ns := range nsList {
		lbls := ns.Labels
		if lbls == nil {
			lbls = map[string]string{}
		}
		items = append(items, nsItem{
			Name:   ns.Name,
			UID:    string(ns.UID),
			Phase:  string(ns.Status.Phase),
			Labels: lbls,
		})
	}

	body := map[string]any{"namespaces": items}
	for attempt := 1; attempt <= pushMaxAttempts; attempt++ {
		if err := s.postJSON(ctx, "/api/agent/namespaces", body); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("sync: push namespaces attempt %d/%d failed: %v", attempt, pushMaxAttempts, err)
			if attempt < pushMaxAttempts {
				select {
				case <-ctx.Done():
					return
				case <-time.After(pushRetryBackoff):
				}
			}
			continue
		}
		log.Printf("sync: pushed %d namespaces", len(items))
		return
	}
	log.Printf("sync: giving up pushing namespaces after %d attempts (will retry on next change)", pushMaxAttempts)
}

// heartbeatLoop sends a heartbeat immediately and then every heartbeatInterval.
func (s *Syncer) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	s.heartbeat(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.heartbeat(ctx)
		}
	}
}

func (s *Syncer) heartbeat(ctx context.Context) {
	body := map[string]string{"agentVersion": s.agentVersion}
	if err := s.postJSON(ctx, "/api/agent/heartbeat", body); err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("sync: heartbeat failed: %v", err)
	}
}

// postJSON POSTs body as JSON to path with the agent Bearer token.
func (s *Syncer) postJSON(ctx context.Context, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("control-plane returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return nil
}
