package telemetry

// metrics.go — die Metrik-Ebene: Infra-Zeitreihen (Nodes/Workloads, vom CP
// beim Topology-Sync geschrieben) + Serien-Queries für die Metrics-Seite und
// den Alert-Evaluator. RED-Serien kommen DIREKT aus den Beyla-Spans
// (otel_traces) — eine Quelle für Liste, Charts und Alerts, kein Drift.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EnsureInfraSchema legt die Infra-Zeitreihen-Tabelle idempotent an.
func (s *Store) EnsureInfraSchema(ctx context.Context) error {
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.infra_metrics (
			Ts        DateTime64(3) CODEC(Delta, ZSTD(1)),
			ClusterId String        CODEC(ZSTD(1)),
			Scope     LowCardinality(String),  -- node | workload
			Name      String        CODEC(ZSTD(1)),
			Metric    LowCardinality(String),  -- cpu_pct | mem_pct | disk_pct | ready | desired | unready
			Value     Float64       CODEC(ZSTD(1))
		) ENGINE = MergeTree
		ORDER BY (ClusterId, Scope, Metric, Name, Ts)
		TTL toDateTime(Ts) + INTERVAL 14 DAY`, s.db)
	return s.exec(ctx, url.Values{"query": {ddl}}, nil)
}

// InfraPoint ist ein Messpunkt (Ts wird beim Insert gestempelt).
type InfraPoint struct {
	Scope  string
	Name   string
	Metric string
	Value  float64
}

// InsertInfraPoints schreibt einen Batch Messpunkte (JSONEachRow).
func (s *Store) InsertInfraPoints(ctx context.Context, clusterID uuid.UUID, pts []InfraPoint) error {
	if len(pts) == 0 {
		return nil
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, p := range pts {
		_ = enc.Encode(map[string]any{
			"Ts": now, "ClusterId": clusterID.String(),
			"Scope": p.Scope, "Name": p.Name, "Metric": p.Metric, "Value": p.Value,
		})
	}
	q := url.Values{"query": {fmt.Sprintf("INSERT INTO %s.infra_metrics FORMAT JSONEachRow", s.db)}}
	return s.exec(ctx, q, &buf)
}

/* ── Serien-Queries ─────────────────────────────────────────────────────── */

// SeriesPoint / Series: das Chart- und Evaluator-Format.
type SeriesPoint struct {
	TsMs  int64   `json:"t"`
	Value float64 `json:"v"`
}
type Series struct {
	Name   string        `json:"name"`
	Points []SeriesPoint `json:"points"`
}

type seriesRow struct {
	Ts string  `json:"ts"`
	G  string  `json:"g"`
	V  float64 `json:"v"`
}

func (s *Store) querySeries(ctx context.Context, sql string, params url.Values) ([]Series, error) {
	params.Set("query", sql)
	params.Set("output_format_json_quote_64bit_integers", "0")
	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Series{}
	order := []string{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var r seriesRow
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("decode series row: %w", err)
		}
		t, err := time.Parse("2006-01-02 15:04:05", r.Ts)
		if err != nil {
			continue
		}
		sr := byName[r.G]
		if sr == nil {
			sr = &Series{Name: r.G}
			byName[r.G] = sr
			order = append(order, r.G)
		}
		sr.Points = append(sr.Points, SeriesPoint{TsMs: t.UnixMilli(), Value: r.V})
	}
	out := make([]Series, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

// stepSeconds wählt eine lesbare Auflösung (~120 Punkte).
func stepSeconds(since, until time.Time) int64 {
	step := int64(until.Sub(since).Seconds()) / 120
	if step < 5 {
		step = 5
	}
	return step
}

// QueryREDSeries: rate (req/min) | error_ratio (%) | p50/p95/p99 (ms) je
// Service aus den Server-Spans.
func (s *Store) QueryREDSeries(ctx context.Context, clusterID uuid.UUID, metric, service, namespace string, since, until time.Time) ([]Series, error) {
	step := stepSeconds(since, until)
	var expr string
	switch metric {
	case "rate":
		expr = fmt.Sprintf("count() * 60.0 / %d", step)
	case "error_ratio":
		expr = "countIf(StatusCode = 'Error' OR SpanAttributes['http.response.status_code'] >= '500') * 100.0 / count()"
	case "p50":
		expr = "quantile(0.5)(Duration) / 1e6"
	case "p95":
		expr = "quantile(0.95)(Duration) / 1e6"
	case "p99":
		expr = "quantile(0.99)(Duration) / 1e6"
	default:
		return nil, fmt.Errorf("unknown red metric %q", metric)
	}
	sql := fmt.Sprintf(`
		SELECT toString(toStartOfInterval(Timestamp, INTERVAL %d SECOND)) AS ts,
		       ServiceName AS g, %s AS v
		FROM %s.otel_traces
		WHERE SpanKind = 'Server'
		  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		  %s %s
		GROUP BY ts, g ORDER BY g, ts FORMAT JSONEachRow`,
		step, expr, s.db,
		cond(service != "", "AND ServiceName = {svc:String}"),
		cond(namespace != "", "AND ResourceAttributes['k8s.namespace.name'] = {ns:String}"))
	params := url.Values{
		"param_since": {since.UTC().Format("2006-01-02 15:04:05.000000000")},
		"param_until": {until.UTC().Format("2006-01-02 15:04:05.000000000")},
	}
	if service != "" {
		params.Set("param_svc", service)
	}
	if namespace != "" {
		params.Set("param_ns", namespace)
	}
	return s.querySeries(ctx, sql, params)
}

// QueryLogRateSeries: Zeilen/min gestapelt nach Severity-Klasse.
func (s *Store) QueryLogRateSeries(ctx context.Context, clusterID uuid.UUID, namespace, workload string, since, until time.Time) ([]Series, error) {
	step := stepSeconds(since, until)
	sql := fmt.Sprintf(`
		SELECT toString(toStartOfInterval(Timestamp, INTERVAL %d SECOND)) AS ts,
		       multiIf(SeverityNumber >= 17, 'error', SeverityNumber >= 13, 'warn', 'info') AS g,
		       count() * 60.0 / %d AS v
		FROM %s.otel_logs
		WHERE ClusterId = {cid:String}
		  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		  %s %s
		GROUP BY ts, g ORDER BY g, ts FORMAT JSONEachRow`,
		step, step, s.db,
		cond(namespace != "", "AND Namespace = {ns:String}"),
		cond(workload != "", "AND WorkloadName = {wl:String}"))
	params := url.Values{
		"param_cid":   {clusterID.String()},
		"param_since": {since.UTC().Format("2006-01-02 15:04:05.000000000")},
		"param_until": {until.UTC().Format("2006-01-02 15:04:05.000000000")},
	}
	if namespace != "" {
		params.Set("param_ns", namespace)
	}
	if workload != "" {
		params.Set("param_wl", workload)
	}
	return s.querySeries(ctx, sql, params)
}

// QueryInfraSeries: Infra-Zeitreihe (avg je Bucket) je Objekt.
func (s *Store) QueryInfraSeries(ctx context.Context, clusterID uuid.UUID, scope, metric, name string, since, until time.Time) ([]Series, error) {
	step := stepSeconds(since, until)
	sql := fmt.Sprintf(`
		SELECT toString(toStartOfInterval(Ts, INTERVAL %d SECOND)) AS ts,
		       Name AS g, avg(Value) AS v
		FROM %s.infra_metrics
		WHERE ClusterId = {cid:String} AND Scope = {scope:String} AND Metric = {metric:String}
		  AND Ts >= {since:DateTime64(3)} AND Ts < {until:DateTime64(3)}
		  %s
		GROUP BY ts, g ORDER BY g, ts FORMAT JSONEachRow`,
		step, s.db,
		cond(name != "", "AND Name = {name:String}"))
	params := url.Values{
		"param_cid":    {clusterID.String()},
		"param_scope":  {scope},
		"param_metric": {metric},
		"param_since":  {since.UTC().Format("2006-01-02 15:04:05.000")},
		"param_until":  {until.UTC().Format("2006-01-02 15:04:05.000")},
	}
	if name != "" {
		params.Set("param_name", name)
	}
	return s.querySeries(ctx, sql, params)
}

// EvalScalar führt eine Alert-Bedingung aus und liefert den aktuellen Wert
// über das Fenster (eine Zahl — die State-Machine vergleicht mit Threshold).
func (s *Store) EvalScalar(ctx context.Context, clusterID uuid.UUID, kind string, p map[string]string, window time.Duration) (float64, error) {
	until := time.Now().UTC()
	since := until.Add(-window)
	params := url.Values{
		"param_cid":   {clusterID.String()},
		"param_since": {since.Format("2006-01-02 15:04:05.000000000")},
		"param_until": {until.Format("2006-01-02 15:04:05.000000000")},
		"output_format_json_quote_64bit_integers": {"0"},
	}
	var sql string
	switch kind {
	case "log_errors":
		sql = fmt.Sprintf(`SELECT count() AS v FROM %s.otel_logs
			WHERE ClusterId = {cid:String} AND SeverityNumber >= 17
			  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)} %s %s FORMAT JSONEachRow`,
			s.db, cond(p["namespace"] != "", "AND Namespace = {ns:String}"),
			cond(p["workload"] != "", "AND WorkloadName = {wl:String}"))
	case "trace_error_ratio":
		sql = fmt.Sprintf(`SELECT countIf(StatusCode = 'Error' OR SpanAttributes['http.response.status_code'] >= '500') * 100.0 / greatest(count(), 1) AS v
			FROM %s.otel_traces WHERE SpanKind = 'Server'
			  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)} %s FORMAT JSONEachRow`,
			s.db, cond(p["service"] != "", "AND ServiceName = {svc:String}"))
	case "trace_p95_ms":
		sql = fmt.Sprintf(`SELECT quantile(0.95)(Duration) / 1e6 AS v
			FROM %s.otel_traces WHERE SpanKind = 'Server'
			  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)} %s FORMAT JSONEachRow`,
			s.db, cond(p["service"] != "", "AND ServiceName = {svc:String}"))
	case "node_cpu_pct", "node_mem_pct", "node_disk_pct":
		metric := map[string]string{"node_cpu_pct": "cpu_pct", "node_mem_pct": "mem_pct", "node_disk_pct": "disk_pct"}[kind]
		params.Set("param_metric", metric)
		sql = fmt.Sprintf(`SELECT max(v) AS v FROM (
			SELECT Name, avg(Value) AS v FROM %s.infra_metrics
			WHERE ClusterId = {cid:String} AND Scope = 'node' AND Metric = {metric:String}
			  AND Ts >= {since:DateTime64(3)} AND Ts < {until:DateTime64(3)} %s
			GROUP BY Name) FORMAT JSONEachRow`,
			s.db, cond(p["node"] != "", "AND Name = {node:String}"))
		if p["node"] != "" {
			params.Set("param_node", p["node"])
		}
	case "workload_unready":
		sql = fmt.Sprintf(`SELECT max(v) AS v FROM (
			SELECT Name, avg(Value) AS v FROM %s.infra_metrics
			WHERE ClusterId = {cid:String} AND Scope = 'workload' AND Metric = 'unready'
			  AND Ts >= {since:DateTime64(3)} AND Ts < {until:DateTime64(3)} %s
			GROUP BY Name) FORMAT JSONEachRow`,
			s.db, cond(p["workload"] != "", "AND Name = {name:String}"))
		if p["workload"] != "" {
			params.Set("param_name", p["workload"])
		}
	default:
		return 0, fmt.Errorf("unknown rule kind %q", kind)
	}
	if p["namespace"] != "" {
		params.Set("param_ns", p["namespace"])
	}
	if p["workload"] != "" && kind == "log_errors" {
		params.Set("param_wl", p["workload"])
	}
	if p["service"] != "" {
		params.Set("param_svc", p["service"])
	}
	params.Set("query", sql)
	body, err := s.get(ctx, params)
	if err != nil {
		return 0, err
	}
	var row struct {
		V float64 `json:"v"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if dec.More() {
		if err := dec.Decode(&row); err != nil {
			return 0, err
		}
	}
	return row.V, nil
}

/* ── Derived Metrics: Logs/Spans → benannte Zeitreihe ───────────────────── */

// DerivedDef ist die Query-Sicht einer MetricDefinition (entkoppelt vom model).
type DerivedDef struct {
	Source    string // logs | spans
	Namespace string
	Workload  string
	Search    string
	ValueMode string // count | regex | duration
	Pattern   string
	Agg       string // rate | avg | p50 | p95 | p99 | max | sum
}

// derivedExprs baut (bucket-Ausdruck, Fenster-Skalar-Ausdruck) je Agg/Mode.
func derivedExprs(d DerivedDef, step int64) (string, string, error) {
	valueOf := ""
	switch d.ValueMode {
	case "count":
		if d.Agg == "rate" {
			return fmt.Sprintf("count() * 60.0 / %d", step), "count()", nil
		}
		return "count()", "count()", nil
	case "regex":
		valueOf = "toFloat64OrNull(extract(Body, {pattern:String}))"
	case "duration":
		valueOf = "Duration / 1e6"
	default:
		return "", "", fmt.Errorf("unknown value mode %q", d.ValueMode)
	}
	aggFn := map[string]string{
		"avg": "avg(%s)", "sum": "sum(%s)", "max": "max(%s)",
		"p50": "quantile(0.5)(%s)", "p95": "quantile(0.95)(%s)", "p99": "quantile(0.99)(%s)",
	}[d.Agg]
	if aggFn == "" {
		return "", "", fmt.Errorf("agg %q not valid for value mode %q", d.Agg, d.ValueMode)
	}
	e := fmt.Sprintf(aggFn, valueOf)
	return e, e, nil
}

func (s *Store) derivedFromWhere(clusterID uuid.UUID, d DerivedDef, params url.Values) (string, string, error) {
	params.Set("param_cid", clusterID.String())
	if d.Pattern != "" {
		params.Set("param_pattern", d.Pattern)
	}
	switch d.Source {
	case "logs":
		where := "ClusterId = {cid:String}"
		if d.Namespace != "" {
			where += " AND Namespace = {ns:String}"
			params.Set("param_ns", d.Namespace)
		}
		if d.Workload != "" {
			where += " AND WorkloadName = {wl:String}"
			params.Set("param_wl", d.Workload)
		}
		if d.Search != "" {
			where += " AND positionCaseInsensitive(Body, {search:String}) > 0"
			params.Set("param_search", d.Search)
		}
		if d.ValueMode == "regex" {
			where += " AND match(Body, {pattern:String})"
		}
		return s.db + ".otel_logs", where, nil
	case "spans":
		where := "SpanKind = 'Server'"
		if d.Workload != "" {
			where += " AND ServiceName = {wl:String}"
			params.Set("param_wl", d.Workload)
		}
		if d.Search != "" {
			where += " AND positionCaseInsensitive(SpanName, {search:String}) > 0"
			params.Set("param_search", d.Search)
		}
		return s.db + ".otel_traces", where, nil
	}
	return "", "", fmt.Errorf("unknown source %q", d.Source)
}

// QueryDerivedSeries liefert die Zeitreihe einer Derived Metric.
func (s *Store) QueryDerivedSeries(ctx context.Context, clusterID uuid.UUID, d DerivedDef, since, until time.Time) ([]Series, error) {
	step := stepSeconds(since, until)
	bucketExpr, _, err := derivedExprs(d, step)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	table, where, err := s.derivedFromWhere(clusterID, d, params)
	if err != nil {
		return nil, err
	}
	sql := fmt.Sprintf(`
		SELECT toString(toStartOfInterval(Timestamp, INTERVAL %d SECOND)) AS ts, 'value' AS g, %s AS v
		FROM %s WHERE %s
		  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		GROUP BY ts ORDER BY ts FORMAT JSONEachRow`, step, bucketExpr, table, where)
	params.Set("param_since", since.UTC().Format("2006-01-02 15:04:05.000000000"))
	params.Set("param_until", until.UTC().Format("2006-01-02 15:04:05.000000000"))
	return s.querySeries(ctx, sql, params)
}

// EvalDerivedScalar: EIN Wert über das Fenster (Alert-Bedingung 'derived').
func (s *Store) EvalDerivedScalar(ctx context.Context, clusterID uuid.UUID, d DerivedDef, window time.Duration) (float64, error) {
	until := time.Now().UTC()
	since := until.Add(-window)
	_, scalarExpr, err := derivedExprs(d, int64(window.Seconds()))
	if err != nil {
		return 0, err
	}
	params := url.Values{"output_format_json_quote_64bit_integers": {"0"}}
	table, where, err := s.derivedFromWhere(clusterID, d, params)
	if err != nil {
		return 0, err
	}
	sql := fmt.Sprintf(`SELECT %s AS v FROM %s WHERE %s
		AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)} FORMAT JSONEachRow`,
		scalarExpr, table, where)
	params.Set("param_since", since.Format("2006-01-02 15:04:05.000000000"))
	params.Set("param_until", until.Format("2006-01-02 15:04:05.000000000"))
	params.Set("query", sql)
	body, err := s.get(ctx, params)
	if err != nil {
		return 0, err
	}
	var row struct {
		V float64 `json:"v"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if dec.More() {
		if err := dec.Decode(&row); err != nil {
			return 0, err
		}
	}
	return row.V, nil
}

// DerivedSamples: Beispiel-Treffer für die Editor-Vorschau (logs only).
func (s *Store) DerivedSamples(ctx context.Context, clusterID uuid.UUID, d DerivedDef, limit int) ([]map[string]string, error) {
	if d.Source != "logs" {
		return nil, nil
	}
	if limit <= 0 || limit > 5 {
		limit = 3
	}
	params := url.Values{}
	table, where, err := s.derivedFromWhere(clusterID, d, params)
	if err != nil {
		return nil, err
	}
	valueSel := "''"
	if d.ValueMode == "regex" {
		valueSel = "extract(Body, {pattern:String})"
	}
	sql := fmt.Sprintf(`SELECT Body AS body, %s AS value FROM %s WHERE %s
		AND Timestamp > now() - INTERVAL 15 MINUTE
		ORDER BY Timestamp DESC LIMIT %d FORMAT JSONEachRow`, valueSel, table, where, limit)
	params.Set("query", sql)
	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []map[string]string{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var r struct {
			Body  string `json:"body"`
			Value string `json:"value"`
		}
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		out = append(out, map[string]string{"body": r.Body, "value": r.Value})
	}
	return out, nil
}

// RawQuery: parametrisierte Query mit rohem JSONEachRow-Ergebnis (PromQL-
// Querier). Tabellennamen werden mit dem konfigurierten DB-Präfix ergänzt.
func (s *Store) RawQuery(ctx context.Context, sql string, params url.Values) ([]byte, error) {
	for _, t := range []string{"otel_metrics_gauge", "otel_metrics_sum", "otel_metrics_histogram", "infra_metrics"} {
		sql = strings.ReplaceAll(sql, " "+t, " "+s.db+"."+t)
		sql = strings.ReplaceAll(sql, "FROM "+t, "FROM "+s.db+"."+t)
	}
	params.Set("query", sql)
	params.Set("output_format_json_quote_64bit_integers", "0")
	return s.get(ctx, params)
}
