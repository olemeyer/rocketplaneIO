package actions

// script_host.go — the HOST-capability primitives on the snapshot surface. Some
// built-in kinds need more than the k8s object API: an exec into a container, a
// log stream, a PDB-aware eviction, a network probe from the agent's vantage
// point. These are generic reusable primitives (like k8s.patch/k8s.scale), so
// the per-kind logic still lives in the visible .star — shown == executed, with
// no native plan() behind it. Reads only; the only mutations (evict) are final
// by design (@reversible none), so no capture is taken.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"

	"go.starlark.net/starlark"
)

// hostExec backs k8s.exec — run ONE read-only whitelisted argv in a container and
// return its output. No shell, output hard-capped. retry catches the short run
// window of a CrashLooping pod (retries until the action timeout).
func (r *Runner) hostExec(ctx context.Context, rep func(string)) *starlark.Builtin {
	return starlark.NewBuiltin("exec", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name, container string
		var command *starlark.List
		retry := false
		if err := starlark.UnpackArgs("exec", args, kwargs, "namespace", &ns, "name", &name, "command", &command, "container?", &container, "retry?", &retry); err != nil {
			return nil, err
		}
		if r.restCfg == nil {
			return nil, fmt.Errorf("exec unavailable (no rest config)")
		}
		argv := make([]string, 0, command.Len())
		for i := 0; i < command.Len(); i++ {
			s, ok := command.Index(i).(starlark.String)
			if !ok {
				return nil, fmt.Errorf("exec: command must be a list of strings")
			}
			argv = append(argv, string(s))
		}
		if err := validExecArgv(argv); err != nil {
			return nil, err
		}
		pod, err := r.clientset.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if container == "" && len(pod.Spec.Containers) > 0 {
			container = pod.Spec.Containers[0].Name
		}
		rep(fmt.Sprintf("%s (container %s)", strings.Join(argv, " "), container))

		execReq := r.clientset.CoreV1().RESTClient().Post().
			Resource("pods").Namespace(ns).Name(name).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: container,
				Command:   argv,
				Stdout:    true,
				Stderr:    true,
			}, scheme.ParameterCodec)
		executor, err := remotecommand.NewSPDYExecutor(r.restCfg, "POST", execReq.URL())
		if err != nil {
			return nil, err
		}
		attempt := func() (string, error) {
			ectx, cancel := context.WithTimeout(ctx, execTimeout)
			defer cancel()
			var stdout, stderr bytes.Buffer
			streamErr := executor.StreamWithContext(ectx, remotecommand.StreamOptions{
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
		if !retry {
			out, err := attempt()
			if err != nil {
				return nil, err
			}
			return starlark.String(out), nil
		}
		tries := 0
		for {
			out, err := attempt()
			if err == nil {
				if tries > 0 {
					out = fmt.Sprintf("[caught after %d retries]\n%s", tries, out)
				}
				return starlark.String(out), nil
			}
			tries++
			rep(fmt.Sprintf("attempt %d failed (%v) — retrying", tries, err))
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("could not catch a running container in %d attempts — last: %v", tries, err)
			case <-time.After(1500 * time.Millisecond):
			}
		}
	})
}

// hostLogs backs k8s.logs — stream a pod's log (optionally the previous, crashed
// container) straight from the kubelet, independent of the log pipeline. Capped.
func (r *Runner) hostLogs(ctx context.Context) *starlark.Builtin {
	return starlark.NewBuiltin("logs", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name, container string
		previous := false
		tail := 200
		if err := starlark.UnpackArgs("logs", args, kwargs, "namespace", &ns, "name", &name, "container?", &container, "previous?", &previous, "tail?", &tail); err != nil {
			return nil, err
		}
		if tail <= 0 || tail > 500 {
			tail = 200
		}
		tl := int64(tail)
		opts := &corev1.PodLogOptions{Container: container, Previous: previous, TailLines: &tl}
		stream, err := r.clientset.CoreV1().Pods(ns).GetLogs(name, opts).Stream(ctx)
		if err != nil {
			if previous {
				return nil, fmt.Errorf("previous container log unavailable: %v", err)
			}
			return nil, err
		}
		defer stream.Close()
		b, err := io.ReadAll(io.LimitReader(stream, readOutputCap))
		if err != nil {
			return nil, err
		}
		out := strings.TrimRight(string(b), "\n")
		if out == "" {
			out = "(no output)"
		}
		return starlark.String(out), nil
	})
}

// hostEvict backs k8s.evict — the PDB-aware Eviction API for ONE pod. Returns a
// status string ("evicted"|"notfound"|"blocked") rather than raising on a PDB
// block, so a script (drain) can branch and retry — Starlark has no exceptions.
// Other API errors still raise.
func (r *Runner) hostEvict(ctx context.Context, rep func(string)) *starlark.Builtin {
	return starlark.NewBuiltin("evict", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		graceSeconds := -1
		if err := starlark.UnpackArgs("evict", args, kwargs, "namespace", &ns, "name", &name, "grace_seconds?", &graceSeconds); err != nil {
			return nil, err
		}
		ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
		if graceSeconds >= 0 {
			g := int64(graceSeconds)
			ev.DeleteOptions = &metav1.DeleteOptions{GracePeriodSeconds: &g}
		}
		err := r.clientset.PolicyV1().Evictions(ns).Evict(ctx, ev)
		switch {
		case err == nil:
			rep(fmt.Sprintf("evicted %s/%s (graceful, PDB-aware)", ns, name))
			return starlark.String("evicted"), nil
		case apierrors.IsNotFound(err):
			return starlark.String("notfound"), nil
		case apierrors.IsTooManyRequests(err) || strings.Contains(err.Error(), "Cannot evict") || strings.Contains(err.Error(), "disruption"):
			return starlark.String("blocked"), nil
		default:
			return nil, err
		}
	})
}

// hostNodePods backs k8s.node_pods — the drainable pods on a node (DaemonSet,
// mirror and already-terminating pods excluded), each as {namespace, name}.
func (r *Runner) hostNodePods(ctx context.Context) *starlark.Builtin {
	return starlark.NewBuiltin("node_pods", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var node string
		if err := starlark.UnpackArgs("node_pods", args, kwargs, "node", &node); err != nil {
			return nil, err
		}
		pods, err := r.drainablePods(ctx, node)
		if err != nil {
			return nil, err
		}
		out := make([]starlark.Value, 0, len(pods))
		for i := range pods {
			d := starlark.NewDict(2)
			_ = d.SetKey(starlark.String("namespace"), starlark.String(pods[i].Namespace))
			_ = d.SetKey(starlark.String("name"), starlark.String(pods[i].Name))
			out = append(out, d)
		}
		return starlark.NewList(out), nil
	})
}

// hostPodStatus backs k8s.pod_status — a single pod's run status
// {phase, terminated, exit_code, reason, stuck}. Lets a script (run_debug_pod)
// poll for completion without needing the full object's stripped status.
func (r *Runner) hostPodStatus(ctx context.Context) *starlark.Builtin {
	return starlark.NewBuiltin("pod_status", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("pod_status", args, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		pod, err := r.clientset.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return starlark.None, nil
		}
		if err != nil {
			return nil, err
		}
		d := starlark.NewDict(5)
		_ = d.SetKey(starlark.String("phase"), starlark.String(string(pod.Status.Phase)))
		terminated := false
		exitCode := 0
		reason := ""
		if len(pod.Status.ContainerStatuses) > 0 {
			if term := pod.Status.ContainerStatuses[0].State.Terminated; term != nil {
				terminated = true
				exitCode = int(term.ExitCode)
				reason = term.Reason
			}
		}
		_ = d.SetKey(starlark.String("terminated"), starlark.Bool(terminated))
		_ = d.SetKey(starlark.String("exit_code"), starlark.MakeInt(exitCode))
		_ = d.SetKey(starlark.String("reason"), starlark.String(reason))
		_ = d.SetKey(starlark.String("stuck"), starlark.String(podStuckReason(pod)))
		return d, nil
	})
}

// hostNetProbe backs net_probe — reachability/TLS/DNS diagnostics from the
// agent's in-cluster vantage point (http|tcp|dns|tls). Read-only, no k8s call.
func (r *Runner) hostNetProbe(ctx context.Context) *starlark.Builtin {
	return starlark.NewBuiltin("net_probe", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var mode, target, method string
		if err := starlark.UnpackArgs("net_probe", args, kwargs, "mode", &mode, "target", &target, "method?", &method); err != nil {
			return nil, err
		}
		if target == "" {
			return nil, fmt.Errorf("net_probe requires mode and target")
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		p := netProbeParams{Mode: mode, Target: target, Method: method}
		var out string
		var err error
		switch mode {
		case "http":
			out, err = probeHTTP(pctx, p)
		case "tcp":
			out, err = probeTCP(target)
		case "dns":
			out, err = probeDNS(pctx, target)
		case "tls":
			out, err = probeTLS(target)
		default:
			return nil, fmt.Errorf("net_probe mode must be http, tcp, dns or tls")
		}
		if err != nil {
			return nil, err
		}
		return starlark.String(out), nil
	})
}
