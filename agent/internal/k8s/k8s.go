// Package k8s builds the Kubernetes client configuration and clientset.
//
// It supports two modes:
//   - in-cluster: rest.InClusterConfig() when running as a Pod (the production path).
//   - fallback:   a kubeconfig on disk (KUBECONFIG or ~/.kube/config) so the agent
//     can be run locally against e.g. minikube for development.
package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// BuildConfig returns a *rest.Config, preferring the in-cluster service-account
// config and falling back to a local kubeconfig. The bool return value is true
// when the in-cluster config was used.
func BuildConfig() (*rest.Config, bool, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, true, nil
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, false, fmt.Errorf("no in-cluster config and cannot locate home dir for kubeconfig: %w", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, false, fmt.Errorf("build kubeconfig from %q: %w", kubeconfig, err)
	}
	return cfg, false, nil
}

// NewClientset builds a typed clientset from the given rest config.
func NewClientset(cfg *rest.Config) (*kubernetes.Clientset, error) {
	return kubernetes.NewForConfig(cfg)
}

// KubeSystemUID reads the UID of the kube-system namespace. This UID is the
// de-facto cluster identity used by rocketplane (Kubernetes has no official
// cluster ID).
func KubeSystemUID(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get kube-system namespace: %w", err)
	}
	uid := string(ns.UID)
	if uid == "" {
		return "", fmt.Errorf("kube-system namespace has empty UID")
	}
	return uid, nil
}
