package actions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/rocketplaneio/rocketplane/agent/internal/actions/recipe"
)

func int32p(i int32) *int32 { return &i }

func deploy(ns, name string, spec, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.DeploymentSpec{Replicas: int32p(spec)},
		Status: appsv1.DeploymentStatus{
			Replicas: ready, ReadyReplicas: ready, AvailableReplicas: ready, UpdatedReplicas: ready,
		},
	}
}

func scaleReq(ns, name string, to int32) Action {
	p, _ := json.Marshal(map[string]int32{"replicas": to})
	return Action{ID: "test-action", Kind: "scale", TargetNamespace: ns, TargetKind: "Deployment", TargetName: name, Params: p}
}

func replicasOf(t *testing.T, r *Runner, ns, name string) int32 {
	t.Helper()
	d, err := r.clientset.AppsV1().Deployments(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if d.Spec.Replicas == nil {
		return 1
	}
	return *d.Spec.Replicas
}

// stubCP records every action result the agent POSTs, so a test can assert what
// was reported and WHEN (running vs terminal).
type stubCP struct {
	*httptest.Server
	mu      sync.Mutex
	reports []reportBody
}

type reportBody struct {
	Status   string          `json:"status"`
	Result   string          `json:"result"`
	Progress string          `json:"progress"`
	Revert   json.RawMessage `json:"revert"`
}

func newStubCP() *stubCP {
	s := &stubCP{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b reportBody
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = json.Unmarshal(body, &b)
		s.mu.Lock()
		s.reports = append(s.reports, b)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cancel":false}`))
	}))
	return s
}

func (s *stubCP) snapshot() []reportBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]reportBody(nil), s.reports...)
}

func manifestRunner(t *testing.T, baseURL string, objs ...runtime.Object) *Runner {
	t.Helper()
	return New(baseURL, "tok", k8sfake.NewSimpleClientset(objs...), nil, nil)
}

// THE CRUX for the kill-revert: executeManifest reports the durable compensation
// on a RUNNING tick, the instant it is armed — so an agent crash mid-run leaves
// a revertible row on the CP. Here scale 3→0 succeeds; we assert a running
// report already carried the inverse (restore to 3) before the terminal report.
func TestExecuteManifestReportsDurableRevertOnRunning(t *testing.T) {
	cp := newStubCP()
	defer cp.Close()
	r := manifestRunner(t, cp.URL, deploy("shop", "worker", 3, 0))
	m, ok, err := recipe.Builtin("scale")
	if err != nil || !ok {
		t.Fatalf("scale manifest: ok=%v err=%v", ok, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r.executeManifest(ctx, scaleReq("shop", "worker", 0), m)

	if got := replicasOf(t, r, "shop", "worker"); got != 0 {
		t.Fatalf("after scale, replicas = %d, want 0", got)
	}

	reports := cp.snapshot()
	var runningRevert, terminalOK bool
	for _, rep := range reports {
		if rep.Status == "running" && hasReplicas(rep.Revert, 3) {
			runningRevert = true
		}
		if rep.Status == "succeeded" {
			terminalOK = true
		}
	}
	if !runningRevert {
		t.Fatalf("no running report carried the durable revert (restore to 3); reports=%+v", reports)
	}
	if !terminalOK {
		t.Fatalf("no terminal succeeded report")
	}
}

func hasReplicas(revert json.RawMessage, want int) bool {
	if len(revert) == 0 {
		return false
	}
	var spec struct {
		Kind   string `json:"kind"`
		Params struct {
			Replicas int `json:"replicas"`
		} `json:"params"`
	}
	if json.Unmarshal(revert, &spec) != nil {
		return false
	}
	return spec.Kind == "scale" && spec.Params.Replicas == want
}

// Drift guard: the scale manifest's steps must faithfully describe the audited
// pipeline the agent actually runs (plan("scale")). If plan() changes, this
// fails until the manifest is updated — the declaration cannot silently diverge.
func TestScaleManifestMatchesPlan(t *testing.T) {
	m, _, _ := recipe.Builtin("scale")
	var manifestSteps []string
	for _, s := range m.Steps {
		manifestSteps = append(manifestSteps, s.Name)
	}
	r := manifestRunner(t, "http://localhost:0", deploy("shop", "checkout", 2, 2))
	var planSteps []string
	for _, s := range r.plan(scaleReq("shop", "checkout", 1)) {
		planSteps = append(planSteps, s.name)
	}
	if !eqStr(manifestSteps, planSteps) {
		t.Fatalf("manifest steps %v != plan steps %v (drift)", manifestSteps, planSteps)
	}
}

func eqStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
