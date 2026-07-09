package enroll

import (
	"strings"
	"testing"
)

func TestIsLocalIntent(t *testing.T) {
	local := []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "host.docker.internal", "host.minikube.internal", "Host.Minikube.Internal"}
	for _, h := range local {
		if !isLocalIntent(h) {
			t.Errorf("isLocalIntent(%q) = false, want true", h)
		}
	}
	remote := []string{"rocketplane.example.com", "10.0.0.5", "192.168.1.12", "cp.internal"}
	for _, h := range remote {
		if isLocalIntent(h) {
			t.Errorf("isLocalIntent(%q) = true, want false", h)
		}
	}
}

func TestCandidateURLs_LocalAddsAliases(t *testing.T) {
	got := candidateURLs("http://localhost:8090")
	if got[0] != "http://localhost:8090" {
		t.Fatalf("configured URL must be first, got %v", got)
	}
	// the reachable host aliases must be offered as alternatives, port preserved
	for _, want := range []string{"http://host.docker.internal:8090", "http://host.minikube.internal:8090"} {
		if !contains(got, want) {
			t.Errorf("candidateURLs(localhost) missing %q; got %v", want, got)
		}
	}
}

func TestCandidateURLs_RemoteStaysStrict(t *testing.T) {
	got := candidateURLs("https://rocketplane.example.com")
	if len(got) != 1 || got[0] != "https://rocketplane.example.com" {
		t.Errorf("a concrete remote URL must not get local alternatives; got %v", got)
	}
}

func TestCandidateURLs_NoDuplicates(t *testing.T) {
	// configured already IS a host alias → must not appear twice
	got := candidateURLs("http://host.minikube.internal:8090")
	seen := map[string]int{}
	for _, u := range got {
		seen[u]++
		if seen[u] > 1 {
			t.Errorf("duplicate candidate %q in %v", u, got)
		}
	}
	if got[0] != "http://host.minikube.internal:8090" {
		t.Errorf("configured URL must stay first, got %v", got)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
