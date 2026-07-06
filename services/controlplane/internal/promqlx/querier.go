// Package promqlx bettet die ORIGINALE Prometheus-PromQL-Engine ein und
// implementiert nur deren Storage-Interface gegen ClickHouse — 100% echte
// PromQL-Semantik (rate, histogram_quantile, Aggregationen, Operatoren) auf
// unseren OTel-Tabellen, kein Nachbau.
//
// Namens-Mapping (OTel → Prometheus-Konvention):
//
//	http.server.request.duration (histogram) →
//	  http_server_request_duration_bucket{le=…} · _sum · _count
//	gauge/sum → Punkte zu Unterstrichen
//	infra_metrics → node_cpu_pct{node=…} · workload_unready{workload=…} …
//
// Labels = DataPoint-Attributes (Keys sanitized) + service_name/_namespace.
// Beyla liefert CUMULATIVE Temporality — Counter sind Engine-ready.
package promqlx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/chunks"
	"github.com/prometheus/prometheus/util/annotations"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/telemetry"
)

// Engine kapselt PromQL-Engine + Queryable-Factory je Cluster.
type Engine struct {
	eng  *promql.Engine
	tele *telemetry.Store
}

func New(tele *telemetry.Store) *Engine {
	return &Engine{
		eng: promql.NewEngine(promql.EngineOpts{
			MaxSamples:           50_000_000,
			Timeout:              30 * time.Second,
			LookbackDelta:        5 * time.Minute,
			EnableAtModifier:     true,
			EnableNegativeOffset: true,
		}),
		tele: tele,
	}
}

// QueryRange führt eine Range-Query aus (Prometheus /api/v1/query_range).
func (e *Engine) QueryRange(ctx context.Context, clusterID uuid.UUID, q string, start, end time.Time, step time.Duration) (*promql.Result, func(), error) {
	qry, err := e.eng.NewRangeQuery(ctx, &queryable{tele: e.tele, clusterID: clusterID}, promql.NewPrometheusQueryOpts(false, 0), q, start, end, step)
	if err != nil {
		return nil, nil, err
	}
	res := qry.Exec(ctx)
	return res, qry.Close, res.Err
}

// QueryInstant (Prometheus /api/v1/query).
func (e *Engine) QueryInstant(ctx context.Context, clusterID uuid.UUID, q string, ts time.Time) (*promql.Result, func(), error) {
	qry, err := e.eng.NewInstantQuery(ctx, &queryable{tele: e.tele, clusterID: clusterID}, promql.NewPrometheusQueryOpts(false, 0), q, ts)
	if err != nil {
		return nil, nil, err
	}
	res := qry.Exec(ctx)
	return res, qry.Close, res.Err
}

/* ── storage.Queryable auf ClickHouse ───────────────────────────────────── */

type queryable struct {
	tele      *telemetry.Store
	clusterID uuid.UUID
}

func (q *queryable) Querier(mint, maxt int64) (storage.Querier, error) {
	return &querier{tele: q.tele, clusterID: q.clusterID, mint: mint, maxt: maxt}, nil
}

type querier struct {
	tele      *telemetry.Store
	clusterID uuid.UUID
	mint      int64
	maxt      int64
}

func (q *querier) Close() error { return nil }

// SanitizeName: OTel-Metrikname → Prometheus-Name.
func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == ':' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func sanitizeLabel(s string) string {
	var b strings.Builder
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (r >= '0' && r <= '9' && i > 0)
		if ok {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// MetricNames liefert alle exponierten Prometheus-Namen (Editor-Autocomplete).
func (e *Engine) MetricNames(ctx context.Context, clusterID uuid.UUID) ([]string, error) {
	rows, err := e.tele.RawQuery(ctx, `
		SELECT DISTINCT MetricName, 'gauge' AS t FROM otel_metrics_gauge WHERE TimeUnix > now() - INTERVAL 1 DAY
		UNION ALL SELECT DISTINCT MetricName, 'sum' FROM otel_metrics_sum WHERE TimeUnix > now() - INTERVAL 1 DAY
		UNION ALL SELECT DISTINCT MetricName, 'histogram' FROM otel_metrics_histogram WHERE TimeUnix > now() - INTERVAL 1 DAY
		FORMAT JSONEachRow`, url.Values{})
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(rows))
	for dec.More() {
		var r struct {
			MetricName string `json:"MetricName"`
			T          string `json:"t"`
		}
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		base := SanitizeName(r.MetricName)
		if r.T == "histogram" {
			names[base+"_bucket"] = true
			names[base+"_sum"] = true
			names[base+"_count"] = true
		} else {
			names[base] = true
		}
	}
	// Infra-Metriken (Postgres-agnostisch: feste Liste + dynamische aus CH)
	irows, err := e.tele.RawQuery(ctx, `
		SELECT DISTINCT Scope, Metric FROM infra_metrics
		WHERE ClusterId = {cid:String} AND Ts > now() - INTERVAL 1 DAY FORMAT JSONEachRow`,
		url.Values{"param_cid": {clusterID.String()}})
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(irows))
		for dec.More() {
			var r struct {
				Scope  string `json:"Scope"`
				Metric string `json:"Metric"`
			}
			if err := dec.Decode(&r); err != nil {
				break
			}
			names[SanitizeName(r.Scope+"_"+r.Metric)] = true
		}
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

/* ── Select: Serien mit Samples laden ───────────────────────────────────── */

type fSample struct {
	t int64
	v float64
}

func (s fSample) T() int64                      { return s.t }
func (s fSample) F() float64                    { return s.v }
func (s fSample) H() *histogram.Histogram       { return nil }
func (s fSample) FH() *histogram.FloatHistogram { return nil }
func (s fSample) Type() chunkenc.ValueType      { return chunkenc.ValFloat }

func (q *querier) Select(ctx context.Context, sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	// __name__ muss (für den MVP) ein Equality-Matcher sein.
	var name string
	rest := []*labels.Matcher{}
	for _, m := range matchers {
		if m.Name == labels.MetricName && m.Type == labels.MatchEqual {
			name = m.Value
		} else {
			rest = append(rest, m)
		}
	}
	if name == "" {
		return storage.ErrSeriesSet(fmt.Errorf("promql on clickhouse: a literal metric name is required (e.g. http_server_request_duration_count)"))
	}

	series, err := q.loadSeries(ctx, name)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	// Rest-Matcher im Speicher anwenden (Label-Filter).
	out := []storage.Series{}
	for lset, samples := range series {
		ls := lset.lset
		match := true
		for _, m := range rest {
			if !m.Matches(ls.Get(m.Name)) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i].T() < samples[j].T() })
		out = append(out, storage.NewListSeries(ls, samples))
	}
	if sortSeries {
		sort.Slice(out, func(i, j int) bool { return labels.Compare(out[i].Labels(), out[j].Labels()) < 0 })
	}
	return newSliceSeriesSet(out)
}

type keyedLabels struct {
	key  string
	lset labels.Labels
}

// loadSeries lädt alle Serien eines Prometheus-Namens aus der passenden Quelle.
func (q *querier) loadSeries(ctx context.Context, name string) (map[*keyedLabels][]chunkSample, error) {
	since := time.UnixMilli(q.mint).UTC().Format("2006-01-02 15:04:05.000")
	until := time.UnixMilli(q.maxt).UTC().Format("2006-01-02 15:04:05.000")
	params := url.Values{"param_since": {since}, "param_until": {until}}

	// 1) Infra? (scope_metric-Muster)
	for _, scope := range []string{"node", "workload"} {
		if strings.HasPrefix(name, scope+"_") {
			metric := strings.TrimPrefix(name, scope+"_")
			params.Set("param_cid", q.clusterID.String())
			params.Set("param_scope", scope)
			params.Set("param_metric", metric)
			rows, err := q.tele.RawQuery(ctx, `
				SELECT toUnixTimestamp64Milli(Ts) AS t, Name AS n, Value AS v FROM infra_metrics
				WHERE ClusterId={cid:String} AND Scope={scope:String} AND Metric={metric:String}
				  AND Ts >= parseDateTime64BestEffort({since:String},3) AND Ts <= parseDateTime64BestEffort({until:String},3)
				ORDER BY t FORMAT JSONEachRow`, params)
			if err != nil {
				return nil, err
			}
			return decodeSimple(rows, name, scope)
		}
	}

	// 2) Histogram-Ableitungen _bucket/_sum/_count
	base, kind := name, "plain"
	for _, suf := range []string{"_bucket", "_sum", "_count"} {
		if strings.HasSuffix(name, suf) {
			base, kind = strings.TrimSuffix(name, suf), strings.TrimPrefix(suf, "_")
			break
		}
	}

	// OTel-Namen mit passendem Sanitized-Namen finden (Punkte unbekannt →
	// Reverse-Lookup über DISTINCT).
	otelName, table, err := q.resolveOtelName(ctx, base, kind)
	if err != nil {
		return nil, err
	}
	if otelName == "" && kind != "plain" {
		// vielleicht ist der volle Name eine plain-Metrik (z.B. foo_count als gauge)
		otelName, table, err = q.resolveOtelName(ctx, name, "plain")
		if err != nil {
			return nil, err
		}
		kind = "plain"
	}
	if otelName == "" {
		return map[*keyedLabels][]chunkSample{}, nil
	}
	params.Set("param_name", otelName)

	switch {
	case table == "otel_metrics_histogram" && kind == "bucket":
		rows, err := q.tele.RawQuery(ctx, `
			SELECT toUnixTimestamp64Milli(TimeUnix) AS t, Attributes AS a,
			       ResourceAttributes['service.name'] AS svc, ResourceAttributes['service.namespace'] AS ns,
			       BucketCounts AS bc, ExplicitBounds AS eb
			FROM otel_metrics_histogram
			WHERE MetricName={name:String}
			  AND TimeUnix >= parseDateTime64BestEffort({since:String},3) AND TimeUnix <= parseDateTime64BestEffort({until:String},3)
			ORDER BY t FORMAT JSONEachRow`, params)
		if err != nil {
			return nil, err
		}
		return decodeBuckets(rows, name)
	case table == "otel_metrics_histogram":
		col := "Sum"
		if kind == "count" {
			col = "toFloat64(Count)"
		}
		rows, err := q.tele.RawQuery(ctx, fmt.Sprintf(`
			SELECT toUnixTimestamp64Milli(TimeUnix) AS t, Attributes AS a,
			       ResourceAttributes['service.name'] AS svc, ResourceAttributes['service.namespace'] AS ns,
			       %s AS v
			FROM otel_metrics_histogram
			WHERE MetricName={name:String}
			  AND TimeUnix >= parseDateTime64BestEffort({since:String},3) AND TimeUnix <= parseDateTime64BestEffort({until:String},3)
			ORDER BY t FORMAT JSONEachRow`, col), params)
		if err != nil {
			return nil, err
		}
		return decodeAttr(rows, name)
	default: // gauge/sum
		rows, err := q.tele.RawQuery(ctx, fmt.Sprintf(`
			SELECT toUnixTimestamp64Milli(TimeUnix) AS t, Attributes AS a,
			       ResourceAttributes['service.name'] AS svc, ResourceAttributes['service.namespace'] AS ns,
			       Value AS v
			FROM %s
			WHERE MetricName={name:String}
			  AND TimeUnix >= parseDateTime64BestEffort({since:String},3) AND TimeUnix <= parseDateTime64BestEffort({until:String},3)
			ORDER BY t FORMAT JSONEachRow`, table), params)
		if err != nil {
			return nil, err
		}
		return decodeAttr(rows, name)
	}
}

// resolveOtelName: Prometheus-Basisname → (OTel-Name, Tabelle).
func (q *querier) resolveOtelName(ctx context.Context, base, kind string) (string, string, error) {
	tables := []string{"otel_metrics_histogram"}
	if kind == "plain" {
		tables = []string{"otel_metrics_gauge", "otel_metrics_sum", "otel_metrics_histogram"}
	}
	for _, t := range tables {
		rows, err := q.tele.RawQuery(ctx, fmt.Sprintf(
			`SELECT DISTINCT MetricName FROM %s WHERE TimeUnix > now() - INTERVAL 1 DAY FORMAT JSONEachRow`, t), url.Values{})
		if err != nil {
			return "", "", err
		}
		dec := json.NewDecoder(bytes.NewReader(rows))
		for dec.More() {
			var r struct {
				MetricName string `json:"MetricName"`
			}
			if err := dec.Decode(&r); err != nil {
				break
			}
			if SanitizeName(r.MetricName) == base {
				return r.MetricName, t, nil
			}
		}
	}
	return "", "", nil
}

type chunkSample = chunks.Sample

func buildLabels(name string, attrs map[string]string, svc, ns string, extra ...string) labels.Labels {
	b := labels.NewBuilder(labels.EmptyLabels())
	b.Set(labels.MetricName, name)
	for k, v := range attrs {
		b.Set(sanitizeLabel(k), v)
	}
	if svc != "" {
		b.Set("service_name", svc)
	}
	if ns != "" {
		b.Set("service_namespace", ns)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		b.Set(extra[i], extra[i+1])
	}
	return b.Labels()
}

func decodeAttr(rows []byte, name string) (map[*keyedLabels][]chunkSample, error) {
	out := map[string]*keyedLabels{}
	samples := map[*keyedLabels][]chunkSample{}
	dec := json.NewDecoder(bytes.NewReader(rows))
	for dec.More() {
		var r struct {
			T   int64             `json:"t"`
			A   map[string]string `json:"a"`
			Svc string            `json:"svc"`
			Ns  string            `json:"ns"`
			V   float64           `json:"v"`
		}
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		ls := buildLabels(name, r.A, r.Svc, r.Ns)
		key := ls.String()
		kl := out[key]
		if kl == nil {
			kl = &keyedLabels{key: key, lset: ls}
			out[key] = kl
		}
		samples[kl] = append(samples[kl], fSample{t: r.T, v: r.V})
	}
	return samples, nil
}

func decodeBuckets(rows []byte, name string) (map[*keyedLabels][]chunkSample, error) {
	out := map[string]*keyedLabels{}
	samples := map[*keyedLabels][]chunkSample{}
	dec := json.NewDecoder(bytes.NewReader(rows))
	for dec.More() {
		var r struct {
			T   int64             `json:"t"`
			A   map[string]string `json:"a"`
			Svc string            `json:"svc"`
			Ns  string            `json:"ns"`
			BC  []uint64          `json:"bc"`
			EB  []float64         `json:"eb"`
		}
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		// kumulative Buckets im Prometheus-Stil: le=bound … le=+Inf
		cum := uint64(0)
		for i, c := range r.BC {
			cum += c
			le := "+Inf"
			if i < len(r.EB) {
				le = strconv.FormatFloat(r.EB[i], 'g', -1, 64)
			}
			ls := buildLabels(name, r.A, r.Svc, r.Ns, "le", le)
			key := ls.String()
			kl := out[key]
			if kl == nil {
				kl = &keyedLabels{key: key, lset: ls}
				out[key] = kl
			}
			samples[kl] = append(samples[kl], fSample{t: r.T, v: float64(cum)})
		}
	}
	return samples, nil
}

func decodeSimple(rows []byte, name, scopeLabel string) (map[*keyedLabels][]chunkSample, error) {
	out := map[string]*keyedLabels{}
	samples := map[*keyedLabels][]chunkSample{}
	dec := json.NewDecoder(bytes.NewReader(rows))
	for dec.More() {
		var r struct {
			T int64   `json:"t"`
			N string  `json:"n"`
			V float64 `json:"v"`
		}
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		ls := buildLabels(name, nil, "", "", scopeLabel, r.N)
		key := ls.String()
		kl := out[key]
		if kl == nil {
			kl = &keyedLabels{key: key, lset: ls}
			out[key] = kl
		}
		samples[kl] = append(samples[kl], fSample{t: r.T, v: r.V})
	}
	return samples, nil
}

/* ── SeriesSet + Pflicht-Interfaces ─────────────────────────────────────── */

type sliceSeriesSet struct {
	series []storage.Series
	idx    int
}

func newSliceSeriesSet(s []storage.Series) *sliceSeriesSet {
	return &sliceSeriesSet{series: s, idx: -1}
}
func (s *sliceSeriesSet) Next() bool {
	s.idx++
	return s.idx < len(s.series)
}
func (s *sliceSeriesSet) At() storage.Series                { return s.series[s.idx] }
func (s *sliceSeriesSet) Err() error                        { return nil }
func (s *sliceSeriesSet) Warnings() annotations.Annotations { return nil }

func (q *querier) LabelValues(ctx context.Context, name string, hints *storage.LabelHints, matchers ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	return nil, nil, nil
}
func (q *querier) LabelNames(ctx context.Context, hints *storage.LabelHints, matchers ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	return nil, nil, nil
}

// Check parst einen PromQL-Ausdruck (Save-Zeit-Verifikation).
func Check(q string) error {
	_, err := parser.ParseExpr(q)
	return err
}
