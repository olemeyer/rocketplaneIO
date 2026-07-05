package alerts

import (
	"testing"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
)

func svc(name string, errRatio, p95 float64) model.Service {
	return model.Service{Name: name, ErrorRatio: errRatio, LatencyMs: model.Latency{P95: p95}}
}

func TestEvaluate(t *testing.T) {
	services := []model.Service{
		svc("checkout-api", 0.042, 842),   // error > 0.02 crit, p95 > 800 crit -> beide feuern
		svc("payment-gateway", 0.001, 90), // unter allen Schwellen -> ok
		svc("cart-service", 0.001, 96),    // ok
		svc("inventory", 0.0, 54),         // ok
	}
	res := Evaluate(services, DefaultRules())

	if res.Total != len(DefaultRules()) {
		t.Fatalf("total = %d, want %d", res.Total, len(DefaultRules()))
	}
	if res.Firing != 2 {
		t.Errorf("firing = %d, want 2 (checkout error + latency)", res.Firing)
	}
	// Feuernde zuerst, kritisch vor Warnung.
	if !res.Alerts[0].Firing || res.Alerts[0].Service != "checkout-api" {
		t.Errorf("first alert should be a firing checkout-api alert: %+v", res.Alerts[0])
	}
	for i := 1; i < len(res.Alerts); i++ {
		if res.Alerts[i].Firing && !res.Alerts[i-1].Firing {
			t.Errorf("firing alert after non-firing at %d", i)
		}
	}
}

func TestEvaluateUnknownServiceSkipped(t *testing.T) {
	res := Evaluate([]model.Service{svc("checkout-api", 0.0, 10)}, DefaultRules())
	for _, a := range res.Alerts {
		if a.Service != "checkout-api" {
			t.Errorf("rule for absent service was not skipped: %+v", a)
		}
	}
}
