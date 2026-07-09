package enroll

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Reaching the control-plane from inside a cluster is the one step that trips up
// quick local trials: the "Connect cluster" command often carries a URL the pod
// can't actually reach — `localhost`/`127.0.0.1` is the POD itself, and
// `host.minikube.internal` doesn't resolve on every setup. So instead of failing,
// the agent DISCOVERS a reachable control-plane: it keeps the configured URL as
// first choice (an explicit, reachable URL always wins) and, when that URL looks
// local, also tries the host aliases a pod can reach — host.docker.internal
// (Docker Desktop / kind), host.minikube.internal (minikube) and the pod's
// default gateway (the route to the host in Docker-based clusters). The first
// candidate whose /healthz answers is used. This makes `--set
// controlplane.url=http://localhost:8090` just work everywhere.

const probeTimeout = 3 * time.Second

// candidateURLs returns the control-plane base URLs to try, in priority order.
// The configured URL is always first. Local-intent URLs (loopback or a known
// host alias) additionally get the reachable host aliases appended; a concrete
// remote host/IP is respected as-is (no alternatives) so we never hijack a real
// remote control-plane that is merely temporarily down.
func candidateURLs(configured string) []string {
	out := []string{configured}
	u, err := url.Parse(strings.TrimRight(configured, "/"))
	if err != nil || u.Host == "" {
		return out
	}
	if !isLocalIntent(u.Hostname()) {
		return out
	}
	seen := map[string]bool{configured: true}
	port := u.Port()
	add := func(host string) {
		if host == "" {
			return
		}
		v := *u
		if port != "" {
			v.Host = net.JoinHostPort(host, port)
		} else {
			v.Host = host
		}
		if s := v.String(); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add("host.docker.internal")
	add("host.minikube.internal")
	add(defaultGateway())
	return out
}

// isLocalIntent reports whether host signals "the control-plane is on this
// machine" — loopback or one of the docker/minikube host aliases.
func isLocalIntent(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "host.docker.internal", "host.minikube.internal":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// defaultGateway reads the pod's default route (/proc/net/route). In Docker-based
// local clusters this gateway IS the host (minikube ≈ 192.168.49.1, kind /
// Docker Desktop the bridge gateway). Empty when unavailable (non-Linux, no
// default route).
func defaultGateway() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // skip header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[1] != "00000000" { // Destination 0.0.0.0 = default
			continue
		}
		gw, err := strconv.ParseUint(fields[2], 16, 32) // little-endian hex
		if err != nil {
			continue
		}
		return net.IPv4(byte(gw), byte(gw>>8), byte(gw>>16), byte(gw>>24)).String()
	}
	return ""
}

// pickReachable probes each candidate's /healthz (short timeout) and returns the
// first that answers 2xx. Returns "" if none answer right now (e.g. the
// control-plane is still starting) — the caller then keeps retrying.
func pickReachable(ctx context.Context, candidates []string) string {
	client := &http.Client{Timeout: probeTimeout}
	for _, base := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/healthz", nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return base
		}
	}
	return ""
}
