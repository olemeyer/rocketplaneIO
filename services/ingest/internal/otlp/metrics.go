package otlp

import (
	"github.com/rocketplaneio/rocketplane/services/ingest/internal/chsink"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// metricsToRows extrahiert GAUGE- und SUM-NumberDataPoints aus ResourceMetrics
// -> ScopeMetrics -> Metrics und baut otel_metrics_gauge/-_sum-Zeilen. Andere
// Metrik-Typen (Histogram, ...) werden in dieser Scheibe übersprungen.
func metricsToRows(resourceMetrics []*metricspb.ResourceMetrics) ([]chsink.MetricRow, []chsink.MetricRow) {
	var gauges, sums []chsink.MetricRow
	for _, rm := range resourceMetrics {
		if rm == nil {
			continue
		}
		var resAttrs map[string]string
		if rm.GetResource() != nil {
			resAttrs = attrsToMap(rm.GetResource().GetAttributes())
		} else {
			resAttrs = map[string]string{}
		}
		serviceName := resAttrs["service.name"]

		for _, sm := range rm.GetScopeMetrics() {
			if sm == nil {
				continue
			}
			scopeName, scopeVersion := "", ""
			if sc := sm.GetScope(); sc != nil {
				scopeName = sc.GetName()
				scopeVersion = sc.GetVersion()
			}
			for _, m := range sm.GetMetrics() {
				if m == nil {
					continue
				}
				base := func(dp *metricspb.NumberDataPoint) chsink.MetricRow {
					return chsink.MetricRow{
						ResourceAttributes: resAttrs,
						ScopeName:          scopeName,
						ScopeVersion:       scopeVersion,
						ServiceName:        serviceName,
						MetricName:         m.GetName(),
						MetricDescription:  m.GetDescription(),
						MetricUnit:         m.GetUnit(),
						Attributes:         attrsToMap(dp.GetAttributes()),
						StartTimeUnix:      nanoToCH(dp.GetStartTimeUnixNano()),
						TimeUnix:           nanoToCH(dp.GetTimeUnixNano()),
						Value:              numberValue(dp),
						Flags:              dp.GetFlags(),
					}
				}
				switch data := m.GetData().(type) {
				case *metricspb.Metric_Gauge:
					for _, dp := range data.Gauge.GetDataPoints() {
						gauges = append(gauges, base(dp))
					}
				case *metricspb.Metric_Sum:
					for _, dp := range data.Sum.GetDataPoints() {
						row := base(dp)
						row.AggregationTemporality = int32(data.Sum.GetAggregationTemporality())
						row.IsMonotonic = data.Sum.GetIsMonotonic()
						sums = append(sums, row)
					}
				default:
					// Histogram / ExponentialHistogram / Summary: übersprungen.
				}
			}
		}
	}
	return gauges, sums
}

// numberValue liefert den Wert eines NumberDataPoint als float64 (Int -> Cast),
// analog zum Collector-clickhouse-exporter getValue().
func numberValue(dp *metricspb.NumberDataPoint) float64 {
	switch v := dp.GetValue().(type) {
	case *metricspb.NumberDataPoint_AsDouble:
		return v.AsDouble
	case *metricspb.NumberDataPoint_AsInt:
		return float64(v.AsInt)
	default:
		return 0
	}
}
