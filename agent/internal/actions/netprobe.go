package actions

// netprobe.go — net_probe: Erreichbarkeits-/TLS-/DNS-Diagnose DIREKT vom
// Agenten (er sitzt im Cluster-Netz) — millisekundenschnell, kein Probe-Pod,
// kein Image-Pull. Die vier Modi decken die Klassiker ab:
//   http → GET/HEAD auf eine in-cluster URL (Status, Latenz, Body-Schnipsel)
//   tcp  → Port offen? (DB erreichbar?)
//   dns  → Name auflösbar? (Service-Discovery kaputt?)
//   tls  → Handshake + Zertifikatskette (LÄUFT DAS CERT AB? Top-Incident-Klasse)

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// netProbeParams carries a probe request; the net_probe primitive (script_host.go)
// builds it from the .star args and dispatches to the probe* helpers below.
type netProbeParams struct {
	Mode   string `json:"mode"`   // http | tcp | dns | tls
	Target string `json:"target"` // URL (http) bzw. host:port (tcp/tls) bzw. name (dns)
	Method string `json:"method"` // http: GET (default) | HEAD
}

func probeHTTP(ctx context.Context, p netProbeParams) (string, error) {
	u, err := url.Parse(p.Target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("http probe needs an http(s):// URL")
	}
	method := http.MethodGet
	if strings.EqualFold(p.Method, "HEAD") {
		method = http.MethodHead
	}
	req, err := http.NewRequestWithContext(ctx, method, p.Target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "rocketplane-agent/net_probe")
	client := &http.Client{
		// Selbstsignierte in-cluster Certs nicht am Probe hindern — für die
		// Zertifikatsprüfung ist der tls-Modus da.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("unreachable: %v", err)
	}
	defer resp.Body.Close()
	lat := time.Since(t0)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 300 {
		snippet = snippet[:300] + "…"
	}
	out := fmt.Sprintf("%s %s → %d %s in %s (%d bytes read)", method, p.Target, resp.StatusCode, http.StatusText(resp.StatusCode), lat.Round(time.Millisecond), len(body))
	if snippet != "" {
		out += "\nbody: " + snippet
	}
	return out, nil
}

func probeTCP(target string) (string, error) {
	if !strings.Contains(target, ":") {
		return "", fmt.Errorf("tcp probe needs host:port")
	}
	t0 := time.Now()
	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("connect failed: %v", err)
	}
	defer conn.Close()
	return fmt.Sprintf("tcp %s → open in %s", target, time.Since(t0).Round(time.Millisecond)), nil
}

func probeDNS(ctx context.Context, name string) (string, error) {
	t0 := time.Now()
	ips, err := net.DefaultResolver.LookupHost(ctx, name)
	if err != nil {
		return "", fmt.Errorf("resolve failed: %v", err)
	}
	return fmt.Sprintf("dns %s → %s in %s", name, strings.Join(ips, ", "), time.Since(t0).Round(time.Millisecond)), nil
}

func probeTLS(target string) (string, error) {
	if !strings.Contains(target, ":") {
		target += ":443"
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", target, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return "", fmt.Errorf("tls handshake failed: %v", err)
	}
	defer conn.Close()
	state := conn.ConnectionState()
	var b strings.Builder
	fmt.Fprintf(&b, "tls %s → %s\n", target, tls.VersionName(state.Version))
	now := time.Now()
	for i, cert := range state.PeerCertificates {
		days := int(cert.NotAfter.Sub(now).Hours() / 24)
		glyph := "·"
		if days < 0 {
			glyph = "▲ EXPIRED"
		} else if days < 14 {
			glyph = "▲ expiring"
		}
		fmt.Fprintf(&b, "%s [%d] CN=%s issuer=%s notAfter=%s (%d days left)\n",
			glyph, i, cert.Subject.CommonName, cert.Issuer.CommonName, cert.NotAfter.Format("2006-01-02"), days)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
