// Package alerts wertet einfache Schwellwert-Regeln gegen den aktuellen RED-
// Zustand der Services aus. Bewusst store-agnostisch: der API-Handler holt die
// Services aus dem Store und ruft Evaluate — so gelten dieselben Regeln für Seed
// und ClickHouse.
package alerts

import (
	"sort"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
)

// Rule ist eine Schwellwert-Regel für eine Metrik eines Service.
type Rule struct {
	ID        string
	Name      string
	Service   string
	Metric    string // "errorRatio" | "p95"
	Threshold float64
	Severity  string // "warning" | "critical"
}

// DefaultRules liefert das eingebaute Regelwerk (fleet-weit + service-spezifisch).
func DefaultRules() []Rule {
	return []Rule{
		{"err-crit-checkout", "checkout-api error rate high", "checkout-api", "errorRatio", 0.02, "critical"},
		{"err-warn-payment", "payment-gateway errors elevated", "payment-gateway", "errorRatio", 0.005, "warning"},
		{"lat-crit-checkout", "checkout-api p95 latency high", "checkout-api", "p95", 800, "critical"},
		{"lat-warn-payment", "payment-gateway p95 latency", "payment-gateway", "p95", 300, "warning"},
		{"err-warn-cart", "cart-service errors elevated", "cart-service", "errorRatio", 0.005, "warning"},
		{"lat-warn-inventory", "inventory p95 latency", "inventory", "p95", 100, "warning"},
	}
}

// Evaluate wertet die Regeln gegen die Services aus; feuernde zuerst, kritische vor Warnungen.
func Evaluate(services []model.Service, rules []Rule) model.AlertList {
	byName := make(map[string]model.Service, len(services))
	for _, s := range services {
		byName[s.Name] = s
	}

	out := make([]model.Alert, 0, len(rules))
	firing := 0
	for _, r := range rules {
		svc, ok := byName[r.Service]
		if !ok {
			continue // Regel für unbekannten Service überspringen
		}
		value := metricValue(svc, r.Metric)
		fired := value > r.Threshold
		if fired {
			firing++
		}
		out = append(out, model.Alert{
			ID: r.ID, Name: r.Name, Service: r.Service, Metric: r.Metric,
			Threshold: r.Threshold, Severity: r.Severity, Value: value, Firing: fired,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Firing != out[j].Firing {
			return out[i].Firing // feuernde zuerst
		}
		return sevRank(out[i].Severity) > sevRank(out[j].Severity)
	})

	return model.AlertList{Alerts: out, Firing: firing, Total: len(out)}
}

func metricValue(s model.Service, metric string) float64 {
	switch metric {
	case "p95":
		return s.LatencyMs.P95
	default: // errorRatio
		return s.ErrorRatio
	}
}

func sevRank(sev string) int {
	if sev == "critical" {
		return 2
	}
	return 1
}
