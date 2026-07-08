package actions

// readgeneric.go — generische READ-Actions nach dem investigate.go-Muster:
//   get_resource      → volles Objekt-YAML (managedFields/status gestrippt)
//   describe_resource → Status + Conditions + Events (kubectl-describe-artig)
//   get_secret        → Keys + Länge + sha256 (NIE Klartext — weder LLM noch DB)
//   helm_releases     → sh.helm.release.v1-Secrets als Release-Liste
//   exec_readonly     → EIN whitelisted argv-Kommando im Container (kein Shell),
//                       Output gedeckelt — der Weg zu Datei-Logs im Container.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/yaml"
)

const (
	execTimeout = 15 * time.Second
	// Output-Caps: die Ergebnisse reisen im steps-JSON des Action-Reports
	// (CP-Limit 64 KiB gesamt) — deutlich darunter bleiben.
	execOutputCap = 40 * 1024
	readOutputCap = 40 * 1024
)

/* ── get_resource ───────────────────────────────────────────────────────── */

// resolveTarget holt das Zielobjekt — Standard-Kinds über die statische Karte,
// ALLES ANDERE (CRDs: Certificates, ArgoCD Applications, CNPG Clusters, Istio
// VirtualServices, …) über params.apiVersion + params.resource (Plural).
func (r *Runner) resolveTarget(ctx context.Context, a Action) (*unstructured.Unstructured, error) {
	var p struct {
		APIVersion string `json:"apiVersion"`
		Resource   string `json:"resource"`
	}
	_ = json.Unmarshal(a.Params, &p)
	if p.APIVersion == "" && p.Resource == "" {
		return r.getUnstructured(ctx, a.TargetKind, a.TargetNamespace, a.TargetName)
	}
	if r.dyn == nil {
		return nil, fmt.Errorf("generic access unavailable (no dynamic client)")
	}
	gvr, err := rawGVR(p.APIVersion, a.TargetKind, p.Resource)
	if err != nil {
		return nil, err
	}
	if a.TargetNamespace == "-" || a.TargetNamespace == "" {
		return r.dyn.Resource(gvr).Get(ctx, a.TargetName, metav1.GetOptions{})
	}
	return r.dyn.Resource(gvr).Namespace(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
}

func (r *Runner) planGetResource(a Action) []step {
	return []step{
		{name: "read", run: func(ctx context.Context, _ func(string)) (string, error) {
			u, err := r.resolveTarget(ctx, a)
			if err != nil {
				return "", err
			}
			obj := stripForSnapshot(u)
			if a.TargetKind == "Secret" {
				redactSecretData(obj)
			}
			y, err := yaml.Marshal(obj)
			if err != nil {
				return "", err
			}
			out := string(y)
			if len(out) > readOutputCap {
				out = out[:readOutputCap] + "\n# …truncated"
			}
			return out, nil
		}},
	}
}

/* ── describe_resource ──────────────────────────────────────────────────── */

func (r *Runner) planDescribeResource(a Action) []step {
	return []step{
		{name: "status", run: func(ctx context.Context, _ func(string)) (string, error) {
			u, err := r.resolveTarget(ctx, a)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s %s/%s\n", a.TargetKind, orDash(a.TargetNamespace), a.TargetName)
			if labels := u.GetLabels(); len(labels) > 0 {
				fmt.Fprintf(&b, "labels: %s\n", kvJoin(labels))
			}
			status, ok := u.Object["status"].(map[string]any)
			if !ok {
				b.WriteString("status: (none)")
				return b.String(), nil
			}
			// Conditions sind die Diagnose-Essenz.
			if conds, ok := status["conditions"].([]any); ok && len(conds) > 0 {
				b.WriteString("conditions:\n")
				for _, cAny := range conds {
					c, _ := cAny.(map[string]any)
					if c == nil {
						continue
					}
					glyph := "·"
					if s, _ := c["status"].(string); s == "False" {
						glyph = "▲"
					}
					fmt.Fprintf(&b, "  %s %v=%v %v %v\n", glyph, c["type"], c["status"], c["reason"], c["message"])
				}
			}
			rest := map[string]any{}
			for k, v := range status {
				if k != "conditions" {
					rest[k] = v
				}
			}
			if len(rest) > 0 {
				j, _ := json.Marshal(rest)
				if len(j) > 4000 {
					j = append(j[:4000], []byte("…")...)
				}
				fmt.Fprintf(&b, "status: %s", j)
			}
			return strings.TrimRight(b.String(), "\n"), nil
		}},
		{name: "events", run: func(ctx context.Context, _ func(string)) (string, error) {
			ns := a.TargetNamespace
			if ns == "-" {
				ns = "" // cluster-scoped: Events stehen im "default"-Bucket je Involved-Objekt — alle Namespaces scannen
			}
			return r.gatherEvents(ctx, ns, []string{a.TargetName})
		}},
	}
}

/* ── get_secret ─────────────────────────────────────────────────────────── */

// redactSecretData ersetzt Secret-Werte durch {len, sha256} — auf dem
// unstructured-Objekt (Snapshot/get_resource-Pfad).
func redactSecretData(obj map[string]any) {
	for _, field := range []string{"data", "stringData"} {
		if data, ok := obj[field].(map[string]any); ok {
			for k, v := range data {
				s, _ := v.(string)
				sum := sha256.Sum256([]byte(s))
				data[k] = fmt.Sprintf("REDACTED len=%d sha256=%s", len(s), hex.EncodeToString(sum[:8]))
			}
		}
	}
}

func (r *Runner) planGetSecret(a Action) []step {
	return []step{
		{name: "inspect", run: func(ctx context.Context, _ func(string)) (string, error) {
			sec, err := r.clientset.CoreV1().Secrets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			keys := make([]string, 0, len(sec.Data))
			for k := range sec.Data {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var b strings.Builder
			fmt.Fprintf(&b, "secret %s/%s type=%s keys=%d\n", a.TargetNamespace, a.TargetName, sec.Type, len(keys))
			for _, k := range keys {
				v := sec.Data[k] // base64-DEKODIERTE Bytes
				sum := sha256.Sum256(v)
				fmt.Fprintf(&b, "  %s: len=%d sha256=%s\n", k, len(v), hex.EncodeToString(sum[:8]))
			}
			return strings.TrimRight(b.String(), "\n"), nil
		}},
	}
}

/* ── helm_releases ──────────────────────────────────────────────────────── */

func (r *Runner) planHelmReleases(a Action) []step {
	return []step{
		{name: "releases", run: func(ctx context.Context, _ func(string)) (string, error) {
			// TargetName trägt den Namespace-Filter ("-" = alle).
			ns := ""
			if a.TargetName != "-" && a.TargetName != "" {
				ns = a.TargetName
			}
			list, err := r.clientset.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{
				FieldSelector: "type=helm.sh/release.v1",
				Limit:         500,
			})
			if err != nil {
				return "", err
			}
			// je Release nur die höchste Revision zeigen
			type rel struct {
				ns, name, status, chart string
				version                 int
			}
			latest := map[string]rel{}
			for i := range list.Items {
				s := &list.Items[i]
				lb := s.Labels
				name := lb["name"]
				if name == "" {
					continue
				}
				ver := 0
				fmt.Sscanf(lb["version"], "%d", &ver)
				key := s.Namespace + "/" + name
				if cur, ok := latest[key]; ok && cur.version >= ver {
					continue
				}
				latest[key] = rel{ns: s.Namespace, name: name, status: lb["status"], version: ver, chart: s.Annotations["meta.helm.sh/chart"]}
			}
			if len(latest) == 0 {
				return "no helm releases found", nil
			}
			keys := make([]string, 0, len(latest))
			for k := range latest {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var b strings.Builder
			for _, k := range keys {
				rl := latest[k]
				fmt.Fprintf(&b, "%s/%s rev=%d status=%s\n", rl.ns, rl.name, rl.version, rl.status)
			}
			return strings.TrimRight(b.String(), "\n"), nil
		}},
	}
}

/* ── exec_readonly ──────────────────────────────────────────────────────── */

type execParams struct {
	Command   []string `json:"command"`
	Container string   `json:"container"`
	// Retry: bis zum Action-Timeout alle ~1.5s erneut versuchen — fängt das
	// kurze Lauffenster eines CrashLoop-Pods ab („exec scheitert, Pod crasht
	// zu schnell" aus echten Incidents).
	Retry bool `json:"retry"`
}

// execAllowed spiegelt die CP-Whitelist (Defense-in-Depth: der Agent verlässt
// sich nie allein auf die Validierung der Control-Plane).
var execAllowed = map[string]bool{
	"cat": true, "ls": true, "head": true, "tail": true, "df": true, "du": true,
	"stat": true, "wc": true, "env": true, "id": true, "uname": true, "find": true,
}

func validExecArgv(argv []string) error {
	if len(argv) == 0 || !execAllowed[argv[0]] {
		return fmt.Errorf("command not in read-only whitelist")
	}
	for _, arg := range argv {
		if strings.ContainsAny(arg, "|;&$><`\n\r") {
			return fmt.Errorf("argument contains shell metacharacters")
		}
	}
	if argv[0] == "find" {
		for _, arg := range argv[1:] {
			switch arg {
			case "-exec", "-execdir", "-delete", "-ok", "-okdir":
				return fmt.Errorf("find must not use -exec/-delete")
			}
		}
	}
	return nil
}

func (r *Runner) planExecReadonly(a Action) []step {
	return []step{
		{name: "exec", run: func(ctx context.Context, report func(string)) (string, error) {
			if r.restCfg == nil {
				return "", fmt.Errorf("exec unavailable (no rest config)")
			}
			var p execParams
			if err := json.Unmarshal(a.Params, &p); err != nil || len(p.Command) == 0 {
				return "", fmt.Errorf("exec_readonly requires params.command")
			}
			if err := validExecArgv(p.Command); err != nil {
				return "", err
			}
			pod, err := r.clientset.CoreV1().Pods(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			container := p.Container
			if container == "" && len(pod.Spec.Containers) > 0 {
				container = pod.Spec.Containers[0].Name
			}
			report(fmt.Sprintf("%s (container %s)", strings.Join(p.Command, " "), container))

			execReq := r.clientset.CoreV1().RESTClient().Post().
				Resource("pods").Namespace(a.TargetNamespace).Name(a.TargetName).
				SubResource("exec").
				VersionedParams(&corev1.PodExecOptions{
					Container: container,
					Command:   p.Command,
					Stdout:    true,
					Stderr:    true,
				}, scheme.ParameterCodec)

			exec, err := remotecommand.NewSPDYExecutor(r.restCfg, "POST", execReq.URL())
			if err != nil {
				return "", err
			}
			attempt := func() (string, error) {
				ectx, cancel := context.WithTimeout(ctx, execTimeout)
				defer cancel()
				var stdout, stderr bytes.Buffer
				streamErr := exec.StreamWithContext(ectx, remotecommand.StreamOptions{
					Stdout: limitWriter{&stdout, execOutputCap},
					Stderr: limitWriter{&stderr, 16 * 1024},
				})
				out := strings.TrimRight(stdout.String(), "\n")
				if stderr.Len() > 0 {
					out += "\n[stderr] " + strings.TrimRight(stderr.String(), "\n")
				}
				if streamErr != nil && out == "" {
					return "", streamErr
				}
				if streamErr != nil {
					out += "\n[exit] " + streamErr.Error()
				}
				if out == "" {
					out = "(no output)"
				}
				return out, nil
			}
			if !p.Retry {
				return attempt()
			}
			// Retry-Modus: den Crash-Zyklus abpassen, bis der Action-Timeout greift.
			tries := 0
			for {
				out, err := attempt()
				if err == nil {
					if tries > 0 {
						out = fmt.Sprintf("[caught after %d retries]\n%s", tries, out)
					}
					return out, nil
				}
				tries++
				report(fmt.Sprintf("attempt %d failed (%v) — retrying", tries, err))
				select {
				case <-ctx.Done():
					return "", fmt.Errorf("could not catch a running container in %d attempts — last: %v", tries, err)
				case <-time.After(1500 * time.Millisecond):
				}
			}
		}},
	}
}

// limitWriter deckelt den gesammelten Output hart (Rest wird verworfen).
type limitWriter struct {
	buf *bytes.Buffer
	max int
}

func (w limitWriter) Write(p []byte) (int, error) {
	if w.buf.Len() >= w.max {
		return len(p), nil // weiter konsumieren, nichts mehr speichern
	}
	if w.buf.Len()+len(p) > w.max {
		p = p[:w.max-w.buf.Len()]
	}
	w.buf.Write(p)
	return len(p), nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func kvJoin(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}
