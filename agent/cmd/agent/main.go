// Command agent is the rocketplane in-cluster Kubernetes agent.
//
// It runs as a Pod in a target cluster and talks OUTBOUND-only to the
// rocketplane control-plane. Lifecycle (see docs/architecture.md §6):
//
//  1. Build a Kubernetes client (in-cluster, or kubeconfig fallback for local dev).
//  2. Read the kube-system namespace UID → the cluster identity.
//  3. Enroll with the control-plane (Enroll-Token → Agent-Token), retrying until up.
//  4. Watch namespaces (informer) and push the full list on change (debounced).
//  5. Heartbeat every 30s.
//  6. Shut down gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/rocketplaneio/rocketplane/agent/internal/actions"
	"github.com/rocketplaneio/rocketplane/agent/internal/enroll"
	"github.com/rocketplaneio/rocketplane/agent/internal/k8s"
	agentsync "github.com/rocketplaneio/rocketplane/agent/internal/sync"
)

// version wird im Release-Build via -ldflags "-X main.version=vX.Y.Z" gesetzt;
// RP_AGENT_VERSION (Helm) hat Vorrang, damit Chart-appVersion die Wahrheit bleibt.
var version = "dev"

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetPrefix("[agent] ")

	controlplaneURL := os.Getenv("RP_CONTROLPLANE_URL")
	enrollToken := os.Getenv("RP_ENROLL_TOKEN")
	clusterName := envOr("RP_CLUSTER_NAME", hostname())
	agentVersion := envOr("RP_AGENT_VERSION", version)

	if controlplaneURL == "" {
		log.Fatalf("RP_CONTROLPLANE_URL is required")
	}
	if enrollToken == "" {
		log.Fatalf("RP_ENROLL_TOKEN is required")
	}

	// Cancel on SIGINT/SIGTERM for a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 1. Kubernetes client.
	cfg, inCluster, err := k8s.BuildConfig()
	if err != nil {
		log.Fatalf("kubernetes config: %v", err)
	}
	if inCluster {
		log.Printf("using in-cluster config")
	} else {
		log.Printf("using kubeconfig fallback (local dev)")
	}
	clientset, err := k8s.NewClientset(cfg)
	if err != nil {
		log.Fatalf("kubernetes clientset: %v", err)
	}

	// 2. Cluster identity = kube-system namespace UID.
	k8sUID, err := k8s.KubeSystemUID(ctx, clientset)
	if err != nil {
		log.Fatalf("read cluster identity: %v", err)
	}
	log.Printf("cluster identity (kube-system uid)=%s name=%q version=%s", k8sUID, clusterName, agentVersion)

	// 3. Enroll (retries with backoff until the control-plane accepts us).
	resp, err := enroll.Enroll(ctx, controlplaneURL, enroll.Request{
		EnrollToken:  enrollToken,
		K8sUID:       k8sUID,
		ClusterName:  clusterName,
		AgentVersion: agentVersion,
	})
	if err != nil {
		log.Fatalf("enroll: %v", err)
	}
	log.Printf("enrolled: clusterId=%s", resp.ClusterID)

	syncer := agentsync.New(controlplaneURL, resp.AgentToken, agentVersion, clientset)

	// Rolle bestimmt, WELCHE Loops laufen:
	//   full (default) — ein Deployment, EINZIGER Writer: Namespace-Sync, Heartbeat,
	//                    Topologie, Inventar, Actions (+ no-op Logs ohne Mount).
	//   logs           — ein DaemonSet je Node, das AUSSCHLIESSLICH die node-lokalen
	//                    Container-Logs tailt (/var/log/pods). Kein Topologie-Push,
	//                    keine Actions → keine Doppel-Writer/Doppelausführung.
	// Log-Collection ist bewusst von den conntrack-Flow-Privilegien entkoppelt: ein
	// Log-DaemonSet braucht weder hostNetwork noch NET_ADMIN, nur den read-only Mount.
	role := envOr("RP_AGENT_ROLE", "full")
	if role == "logs" {
		log.Printf("role=logs — node-local log collection only (no topology/actions)")
		go func() {
			if err := syncer.RunPodInformer(ctx); err != nil && ctx.Err() == nil {
				log.Printf("logs: pod informer: %v", err)
			}
		}()
		if err := syncer.RunLogs(ctx); err != nil && ctx.Err() == nil {
			log.Fatalf("logs: %v", err)
		}
		log.Printf("shutting down")
		return
	}

	// role=full: der einzige Writer — alle Loops laufen zusammen bis runCtx endet.
	runFull := func(runCtx context.Context) {
		go func() {
			if err := syncer.RunTopology(runCtx); err != nil && runCtx.Err() == nil {
				log.Printf("topology: %v", err)
			}
		}()
		// Zero-config Log-Collection (no-op ohne /var/log/pods-Mount — auf Clustern mit
		// dem logs-DaemonSet übernimmt dieser die Logs; hier bleibt es ein stiller No-Op).
		go func() {
			if err := syncer.RunLogs(runCtx); err != nil && runCtx.Err() == nil {
				log.Printf("logs: %v", err)
			}
		}()
		// K8s-Inventar (Services/Ingress/Config/Batch/… als kompakte Zusammenfassung)
		// für die Resources-Seite + das list_resources-Tool des Copilots.
		go func() {
			if err := syncer.RunInventory(runCtx); err != nil && runCtx.Err() == nil {
				log.Printf("inventory: %v", err)
			}
		}()
		// Safe-Actions: dispatch/cancel kommen live über den outbound SSE-Stream
		// (Fallback: seltener Poll), ausgeführt wird im Cluster — outbound-only.
		// Nach jeder Aktion Topologie-Burst — die UI sieht den Rollout live.
		go func() {
			runner := actions.New(controlplaneURL, resp.AgentToken, clientset, syncer.TriggerTopologySync)
			if err := runner.Run(runCtx); err != nil && runCtx.Err() == nil {
				log.Printf("actions: %v", err)
			}
		}()
		if err := syncer.Run(runCtx); err != nil && runCtx.Err() == nil {
			log.Fatalf("sync: %v", err)
		}
	}

	// HA: mit replicas>1 darf genau EIN Agent schreiben/handeln (Topologie-
	// Full-Sync + Action-Ausführung vertragen keine zwei Writer). Eine
	// Kubernetes-Lease wählt den Leader; Standbys warten heiß. Verliert der
	// Leader die Lease, beendet er sich hart — der Neustart ist der sauberste
	// Weg, halb laufende Loops sicher loszuwerden (kein Split-Brain-Rest).
	if envOr("RP_AGENT_LEADER_ELECT", "true") != "true" {
		log.Printf("role=full — leader election disabled (RP_AGENT_LEADER_ELECT=false)")
		runFull(ctx)
		log.Printf("shutting down")
		return
	}
	identity := envOr("POD_NAME", hostname())
	leaseNs := envOr("POD_NAMESPACE", "rocketplane")
	log.Printf("role=full — campaigning for lease %s/rocketplane-agent-leader (id=%s)", leaseNs, identity)
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: &resourcelock.LeaseLock{
			LeaseMeta:  metav1.ObjectMeta{Name: "rocketplane-agent-leader", Namespace: leaseNs},
			Client:     clientset.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{Identity: identity},
		},
		ReleaseOnCancel: true, // SIGTERM gibt die Lease sofort frei → schneller Failover
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leadCtx context.Context) {
				log.Printf("leader: acquired — topology, inventory, actions, namespaces")
				runFull(leadCtx)
			},
			OnStoppedLeading: func() {
				if ctx.Err() != nil {
					return // regulärer Shutdown
				}
				log.Fatalf("leader: lease lost — exiting for a clean restart")
			},
		},
	})

	log.Printf("shutting down")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func hostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown-cluster"
}
