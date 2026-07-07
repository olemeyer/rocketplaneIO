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

	// 4+5. Namespace-Sync + Heartbeat + Topologie-Sync (Pods/Services für die
	// Service-Map), alle bis zum Shutdown. Topologie läuft parallel zum Namespace-Sync.
	syncer := agentsync.New(controlplaneURL, resp.AgentToken, agentVersion, clientset)
	go func() {
		if err := syncer.RunTopology(ctx); err != nil && ctx.Err() == nil {
			log.Printf("topology: %v", err)
		}
	}()
	// Zero-config Log-Collection (no-op ohne /var/log/pods-Mount).
	go func() {
		if err := syncer.RunLogs(ctx); err != nil && ctx.Err() == nil {
			log.Printf("logs: %v", err)
		}
	}()
	// K8s-Inventar (Services/Ingress/Config/Batch/… als kompakte Zusammenfassung)
	// für die Resources-Seite + das list_resources-Tool des Copilots.
	go func() {
		if err := syncer.RunInventory(ctx); err != nil && ctx.Err() == nil {
			log.Printf("inventory: %v", err)
		}
	}()
	// Safe-Actions: dispatch/cancel kommen live über den outbound SSE-Stream
	// (Fallback: seltener Poll), ausgeführt wird im Cluster — outbound-only.
	// Nach jeder Aktion Topologie-Burst — die UI sieht den Rollout live.
	go func() {
		runner := actions.New(controlplaneURL, resp.AgentToken, clientset, syncer.TriggerTopologySync)
		if err := runner.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("actions: %v", err)
		}
	}()
	if err := syncer.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("sync: %v", err)
	}

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
