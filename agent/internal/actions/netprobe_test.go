package actions
import "testing"
func TestNetProbeValidation(t *testing.T) {
  r := testRunner(t)
  for _, mode := range []string{"http","tcp","dns","tls"} {
    steps := r.planNetProbe(Action{Params: []byte(`{"mode":"`+mode+`","target":"example:443"}`)})
    if len(steps) != 1 { t.Fatalf("%s: expected 1 step", mode) }
  }
}
func TestDeniedPatchGroup(t *testing.T) {
  for _, g := range []string{"rbac.authorization.k8s.io","admissionregistration.k8s.io","apiextensions.k8s.io"} {
    if !deniedPatchGroup(g) { t.Errorf("%s must be denied", g) }
  }
  for _, g := range []string{"apps","networking.k8s.io","cert-manager.io","postgresql.cnpg.io"} {
    if deniedPatchGroup(g) { t.Errorf("%s must be allowed", g) }
  }
}
