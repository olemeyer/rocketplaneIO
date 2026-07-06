package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// traces.go — Queries auf die vom OTel-Collector (clickhouse-exporter) gefüllte
// otel_traces-Tabelle (Beyla-eBPF-Spans). Liefert die Trace-Liste und die
// RED-Aggregation (Rate / Errors / Duration) je Service für die UI.
//
// Hinweis Tenancy: otel_traces trägt (noch) keine ClusterId — die Autorisierung
// läuft org→cluster über die API-Schicht; die Datenebene wird mit dem eigenen
// Ingest-Pfad tenant-getrennt (Roadmap logs-ingest v2).

// TraceRow ist eine Zeile der Trace-Liste: der ROOT-Span eines Traces (dash0-
// Muster: ein Trace = eine Zeile) plus Span-Count/Fehler über den ganzen Trace.
type TraceRow struct {
	Ts          string  `json:"ts"`
	TraceID     string  `json:"traceId"`
	ServiceName string  `json:"serviceName"`
	SpanName    string  `json:"spanName"`
	DurationMs  float64 `json:"durationMs"`
	StatusCode  string  `json:"statusCode"`
	HTTPStatus  string  `json:"httpStatus"`
	Namespace   string  `json:"namespace"`
	SpanCount   uint64  `json:"spanCount"`
	ErrorCount  uint64  `json:"errorCount"`
}

// TraceSpan ist ein Span des Trace-Details (Waterfall im Side-Panel), inklusive
// der vollen Attribute für die Detail-Ansicht (dash0-Muster).
type TraceSpan struct {
	SpanID       string            `json:"spanId"`
	ParentSpanID string            `json:"parentSpanId"`
	ServiceName  string            `json:"serviceName"`
	SpanName     string            `json:"spanName"`
	Kind         string            `json:"kind"`
	StartUnixNs  int64             `json:"startUnixNs"`
	DurationMs   float64           `json:"durationMs"`
	StatusCode   string            `json:"statusCode"`
	HTTPStatus   string            `json:"httpStatus"`
	Namespace    string            `json:"namespace"`
	Attributes   map[string]string `json:"attributes"`
	Resource     map[string]string `json:"resource"`
}

// SpanStats vergleicht einen Span mit allen Spans derselben Operation im
// Zeitfenster: Quantile + Verteilungs-Histogramm (die dash0-„Triage-Bar").
type SpanStats struct {
	Count     uint64      `json:"count"`
	P50       float64     `json:"p50"`
	P75       float64     `json:"p75"`
	P90       float64     `json:"p90"`
	P95       float64     `json:"p95"`
	P99       float64     `json:"p99"`
	Histogram [][]float64 `json:"histogram"` // [lo, hi, height] je Bucket (ms)
}

// REDRow ist die RED-Aggregation eines Services im Zeitfenster.
type REDRow struct {
	ServiceName string  `json:"serviceName"`
	Namespace   string  `json:"namespace"`
	Requests    uint64  `json:"requests"`
	RatePerMin  float64 `json:"ratePerMin"`
	ErrorRatio  float64 `json:"errorRatio"`
	P50Ms       float64 `json:"p50Ms"`
	P95Ms       float64 `json:"p95Ms"`
	P99Ms       float64 `json:"p99Ms"`
}

// TracesQuery sind die Filter des Traces-Explorers.
type TracesQuery struct {
	Namespace string
	Service   string
	OnlyError bool
	Since     time.Time
	Until     time.Time
	Limit     int
}

// QueryTraces liest die jüngsten TRACES (ein Eintrag je TraceId, aggregiert):
// Root-Repräsentant = längster Span des Traces (bei Beyla trägt der Server-
// Root die Gesamtzeit), plus Span-/Fehler-Count über den Trace.
func (s *Store) QueryTraces(ctx context.Context, q TracesQuery) ([]TraceRow, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	sql := fmt.Sprintf(`
		SELECT toString(min(Timestamp)) AS ts,
		       TraceId,
		       argMax(ServiceName, Duration) AS svc,
		       argMax(SpanName, Duration) AS name,
		       max(Duration) / 1e6 AS durMs,
		       argMax(StatusCode, Duration) AS status,
		       argMax(SpanAttributes['http.response.status_code'], Duration) AS httpStatus,
		       argMax(ResourceAttributes['k8s.namespace.name'], Duration) AS ns,
		       count() AS spanCount,
		       countIf(StatusCode = 'Error' OR SpanAttributes['http.response.status_code'] >= '500') AS errCount
		FROM %s.otel_traces
		WHERE Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		  %s %s
		GROUP BY TraceId
		%s
		ORDER BY ts DESC
		LIMIT %d FORMAT JSONEachRow`,
		s.db,
		cond(q.Namespace != "", "AND ResourceAttributes['k8s.namespace.name'] = {ns:String}"),
		cond(q.Service != "", "AND ServiceName = {svc:String}"),
		cond(q.OnlyError, "HAVING errCount > 0"),
		q.Limit)

	params := url.Values{"query": {sql}}
	params.Set("param_since", nanoToCH(q.Since.UnixNano()))
	params.Set("param_until", nanoToCH(q.Until.UnixNano()))
	if q.Namespace != "" {
		params.Set("param_ns", q.Namespace)
	}
	if q.Service != "" {
		params.Set("param_svc", q.Service)
	}

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []TraceRow{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			Ts         string  `json:"ts"`
			TraceId    string  `json:"TraceId"`
			Svc        string  `json:"svc"`
			Name       string  `json:"name"`
			DurMs      float64 `json:"durMs"`
			Status     string  `json:"status"`
			HTTPStatus string  `json:"httpStatus"`
			Ns         string  `json:"ns"`
			SpanCount  uint64  `json:"spanCount"`
			ErrCount   uint64  `json:"errCount"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode trace row: %w", err)
		}
		out = append(out, TraceRow{
			Ts: raw.Ts, TraceID: raw.TraceId, ServiceName: raw.Svc,
			SpanName: raw.Name, DurationMs: raw.DurMs, StatusCode: raw.Status,
			HTTPStatus: raw.HTTPStatus, Namespace: raw.Ns,
			SpanCount: raw.SpanCount, ErrorCount: raw.ErrCount,
		})
	}
	return out, nil
}

// QueryTraceSpans liefert ALLE Spans eines Traces (für den Waterfall),
// aufsteigend nach Startzeit.
func (s *Store) QueryTraceSpans(ctx context.Context, traceID string) ([]TraceSpan, error) {
	sql := fmt.Sprintf(`
		SELECT SpanId, ParentSpanId, ServiceName, SpanName, SpanKind,
		       toUnixTimestamp64Nano(Timestamp) AS startNs,
		       Duration / 1e6 AS durMs, StatusCode,
		       SpanAttributes['http.response.status_code'] AS httpStatus,
		       ResourceAttributes['k8s.namespace.name'] AS ns,
		       SpanAttributes AS attrs,
		       ResourceAttributes AS res
		FROM %s.otel_traces
		WHERE TraceId = {tid:String}
		ORDER BY Timestamp ASC
		LIMIT 500 FORMAT JSONEachRow`, s.db)

	params := url.Values{"query": {sql}}
	params.Set("param_tid", traceID)

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []TraceSpan{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			SpanId       string            `json:"SpanId"`
			ParentSpanId string            `json:"ParentSpanId"`
			ServiceName  string            `json:"ServiceName"`
			SpanName     string            `json:"SpanName"`
			SpanKind     string            `json:"SpanKind"`
			StartNs      int64             `json:"startNs"`
			DurMs        float64           `json:"durMs"`
			StatusCode   string            `json:"StatusCode"`
			HTTPStatus   string            `json:"httpStatus"`
			Ns           string            `json:"ns"`
			Attrs        map[string]string `json:"attrs"`
			Res          map[string]string `json:"res"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode trace span: %w", err)
		}
		out = append(out, TraceSpan{
			SpanID: raw.SpanId, ParentSpanID: raw.ParentSpanId,
			ServiceName: raw.ServiceName, SpanName: raw.SpanName, Kind: raw.SpanKind,
			StartUnixNs: raw.StartNs, DurationMs: raw.DurMs, StatusCode: raw.StatusCode,
			HTTPStatus: raw.HTTPStatus, Namespace: raw.Ns,
			Attributes: raw.Attrs, Resource: raw.Res,
		})
	}
	return out, nil
}

// QueryTraceHistogram liefert Zeit-Buckets (Traces gesamt + Fehler-Traces) fürs
// brushbare Volumen-Chart — dasselbe Muster wie das Log-Histogramm.
func (s *Store) QueryTraceHistogram(ctx context.Context, q TracesQuery, buckets int) ([]LogBucket, error) {
	if buckets <= 0 || buckets > 240 {
		buckets = 60
	}
	stepSec := int64(q.Until.Sub(q.Since).Seconds()) / int64(buckets)
	if stepSec < 1 {
		stepSec = 1
	}
	sql := fmt.Sprintf(`
		SELECT toString(toStartOfInterval(Timestamp, INTERVAL %d SECOND)) AS ts,
		       uniq(TraceId) AS c,
		       uniqIf(TraceId, StatusCode = 'Error' OR SpanAttributes['http.response.status_code'] >= '500') AS e,
		       uniqIf(TraceId, SpanAttributes['http.response.status_code'] >= '400' AND SpanAttributes['http.response.status_code'] < '500') AS w
		FROM %s.otel_traces
		WHERE Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		  %s %s
		GROUP BY ts ORDER BY ts FORMAT JSONEachRow`,
		stepSec, s.db,
		cond(q.Namespace != "", "AND ResourceAttributes['k8s.namespace.name'] = {ns:String}"),
		cond(q.Service != "", "AND ServiceName = {svc:String}"))

	params := url.Values{"query": {sql}}
	params.Set("param_since", nanoToCH(q.Since.UnixNano()))
	params.Set("param_until", nanoToCH(q.Until.UnixNano()))
	if q.Namespace != "" {
		params.Set("param_ns", q.Namespace)
	}
	if q.Service != "" {
		params.Set("param_svc", q.Service)
	}

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []LogBucket{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			Ts string `json:"ts"`
			C  uint64 `json:"c"`
			E  uint64 `json:"e"`
			W  uint64 `json:"w"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode trace bucket: %w", err)
		}
		out = append(out, LogBucket{Ts: raw.Ts, Count: raw.C, Errors: raw.E, Warns: raw.W})
	}
	return out, nil
}

// QuerySpanStats liefert die Duration-Verteilung aller Spans derselben Operation
// (ServiceName + SpanName) im Zeitfenster — für den „wie normal ist dieser
// Span?"-Vergleich im Detail-Panel.
func (s *Store) QuerySpanStats(ctx context.Context, service, span string, since, until time.Time) (*SpanStats, error) {
	sql := fmt.Sprintf(`
		SELECT count() AS c,
		       quantiles(0.5, 0.75, 0.9, 0.95, 0.99)(Duration / 1e6) AS qs,
		       histogram(24)(Duration / 1e6) AS h
		FROM %s.otel_traces
		WHERE ServiceName = {svc:String} AND SpanName = {span:String}
		  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		FORMAT JSONEachRow`, s.db)

	params := url.Values{"query": {sql}}
	params.Set("param_svc", service)
	params.Set("param_span", span)
	params.Set("param_since", nanoToCH(since.UnixNano()))
	params.Set("param_until", nanoToCH(until.UnixNano()))

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	var raw struct {
		C  uint64      `json:"c"`
		Qs []float64   `json:"qs"`
		H  [][]float64 `json:"h"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if dec.More() {
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode span stats: %w", err)
		}
	}
	st := &SpanStats{Count: raw.C, Histogram: raw.H}
	if len(raw.Qs) == 5 {
		st.P50, st.P75, st.P90, st.P95, st.P99 = raw.Qs[0], raw.Qs[1], raw.Qs[2], raw.Qs[3], raw.Qs[4]
	}
	if st.Histogram == nil {
		st.Histogram = [][]float64{}
	}
	return st, nil
}

// QueryRED aggregiert Rate/Errors/Duration je Service im Fenster.
func (s *Store) QueryRED(ctx context.Context, q TracesQuery) ([]REDRow, error) {
	windowMin := q.Until.Sub(q.Since).Minutes()
	if windowMin <= 0 {
		windowMin = 1
	}
	sql := fmt.Sprintf(`
		SELECT ServiceName,
		       anyLast(ResourceAttributes['k8s.namespace.name']) AS ns,
		       count() AS reqs,
		       countIf(StatusCode = 'Error' OR SpanAttributes['http.response.status_code'] >= '500') AS errs,
		       quantile(0.5)(Duration) / 1e6 AS p50,
		       quantile(0.95)(Duration) / 1e6 AS p95,
		       quantile(0.99)(Duration) / 1e6 AS p99
		FROM %s.otel_traces
		WHERE Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		  %s
		GROUP BY ServiceName ORDER BY reqs DESC
		FORMAT JSONEachRow`,
		s.db,
		cond(q.Namespace != "", "AND ResourceAttributes['k8s.namespace.name'] = {ns:String}"))

	params := url.Values{"query": {sql}}
	params.Set("param_since", nanoToCH(q.Since.UnixNano()))
	params.Set("param_until", nanoToCH(q.Until.UnixNano()))
	if q.Namespace != "" {
		params.Set("param_ns", q.Namespace)
	}

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []REDRow{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			ServiceName string  `json:"ServiceName"`
			Ns          string  `json:"ns"`
			Reqs        uint64  `json:"reqs"`
			Errs        uint64  `json:"errs"`
			P50         float64 `json:"p50"`
			P95         float64 `json:"p95"`
			P99         float64 `json:"p99"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode red row: %w", err)
		}
		errRatio := 0.0
		if raw.Reqs > 0 {
			errRatio = float64(raw.Errs) / float64(raw.Reqs)
		}
		out = append(out, REDRow{
			ServiceName: raw.ServiceName, Namespace: raw.Ns, Requests: raw.Reqs,
			RatePerMin: float64(raw.Reqs) / windowMin, ErrorRatio: errRatio,
			P50Ms: raw.P50, P95Ms: raw.P95, P99Ms: raw.P99,
		})
	}
	return out, nil
}
