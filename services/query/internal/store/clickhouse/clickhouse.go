// Package clickhouse ist der Drop-in-Store gegen ClickHouse über das HTTP-
// Interface (:8123, FORMAT JSONEachRow, parametrisierte {name:Type}-Queries).
// Er liest aus der otel_traces-Tabelle, die der OTel-Collector-clickhouse-
// exporter (v0.116) anlegt: Duration in NANOSEKUNDEN (UInt64), Root-Spans mit
// ParentSpanId=”, SpanKind PascalCase (Server/Client/...), StatusCode
// Unset/Ok/Error.
package clickhouse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
)

// Config beschreibt die Verbindung zum ClickHouse-HTTP-Interface.
type Config struct {
	URL      string // z.B. http://localhost:8123
	Database string // z.B. otel
	User     string
	Password string
	Timeout  time.Duration
}

// Store implementiert store.Store gegen ClickHouse.
type Store struct {
	cfg    Config
	client *http.Client
}

var _ store.Store = (*Store)(nil)

// New erstellt einen ClickHouse-Store. client darf nil sein (Default mit Timeout).
func New(cfg Config, client *http.Client) *Store {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Store{cfg: cfg, client: client}
}

// Ping prüft die Erreichbarkeit über den /ping-Endpunkt von ClickHouse.
func (s *Store) Ping(ctx context.Context) error {
	endpoint := strings.TrimRight(s.cfg.URL, "/") + "/ping"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("clickhouse ping: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clickhouse ping: status %d", resp.StatusCode)
	}
	return nil
}

func (s *Store) Close() error { return nil }

// --- Services (RED) ---------------------------------------------------------

func (s *Store) Services(ctx context.Context, q store.ServicesQuery) (model.ServicesResult, error) {
	end := q.End
	if end.IsZero() {
		end = time.Now()
	}
	start := q.Start
	if start.IsZero() {
		start = end.Add(-15 * time.Minute)
	}
	step := q.Step
	if step <= 0 {
		step = end.Sub(start) / 8
	}
	windowSec := end.Sub(start).Seconds()
	if windowSec <= 0 {
		windowSec = 1
	}

	params := map[string]string{
		"start":  chTime(start),
		"end":    chTime(end),
		"winSec": fmt.Sprintf("%f", windowSec),
		"env":    q.Env,
	}

	const redSQL = `
SELECT ServiceName AS name,
       count()                                        AS spanCount,
       countIf(StatusCode='Error')                    AS errorCount,
       countIf(StatusCode='Error')/count()            AS errorRatio,
       count()/{winSec:Float64}                       AS rate,
       quantile(0.50)(Duration)/1e6                   AS p50,
       quantile(0.95)(Duration)/1e6                   AS p95,
       quantile(0.99)(Duration)/1e6                   AS p99
FROM otel_traces
WHERE Timestamp >= {start:DateTime64(9)} AND Timestamp < {end:DateTime64(9)}
  AND SpanKind IN ('Server','Consumer')
  AND ({env:String}='' OR ResourceAttributes['deployment.environment']={env:String})
GROUP BY ServiceName`

	var reds []struct {
		Name       string  `json:"name"`
		SpanCount  int64   `json:"spanCount"`
		ErrorCount int64   `json:"errorCount"`
		ErrorRatio float64 `json:"errorRatio"`
		Rate       float64 `json:"rate"`
		P50        float64 `json:"p50"`
		P95        float64 `json:"p95"`
		P99        float64 `json:"p99"`
	}
	if err := s.query(ctx, redSQL, params, &reds); err != nil {
		return model.ServicesResult{}, err
	}

	// Sparkline: Rate je Zeit-Bucket und Service.
	sparks, err := s.sparklines(ctx, start, end, step, q.Env)
	if err != nil {
		return model.ServicesResult{}, err
	}

	// SLO-Referenz je Service (p95-Ziel) für die zentrale Health-Ableitung.
	slo := map[string]float64{"checkout-api": 400, "payment-gateway": 300, "cart-service": 150, "inventory": 100}

	services := make([]model.Service, 0, len(reds))
	for _, r := range reds {
		sloP95 := slo[r.Name]
		if sloP95 == 0 {
			sloP95 = 300
		}
		services = append(services, model.Service{
			Name:       r.Name,
			Status:     model.DeriveHealth(r.ErrorRatio, r.P95, sloP95),
			Rate:       round1(r.Rate),
			ErrorRatio: r.ErrorRatio,
			LatencyMs:  model.Latency{P50: round1(r.P50), P95: round1(r.P95), P99: round1(r.P99)},
			SpanCount:  r.SpanCount,
			ErrorCount: r.ErrorCount,
			Sparkline:  model.Sparkline{Metric: "rate", Values: sparks[r.Name]},
		})
	}
	sortServices(services, q.Sort)

	return model.ServicesResult{
		Window:   model.Window{Start: start.Unix(), End: end.Unix(), Step: int64(step.Seconds())},
		Services: services,
	}, nil
}

func (s *Store) sparklines(ctx context.Context, start, end time.Time, step time.Duration, env string) (map[string][]float64, error) {
	stepSec := int64(step.Seconds())
	if stepSec < 1 {
		stepSec = 1
	}
	params := map[string]string{
		"start":   chTime(start),
		"end":     chTime(end),
		"stepSec": fmt.Sprintf("%d", stepSec),
		"env":     env,
	}
	const sql = `
SELECT ServiceName AS name,
       toUnixTimestamp(toStartOfInterval(Timestamp, INTERVAL {stepSec:UInt32} SECOND)) AS bucket,
       count()/{stepSec:UInt32} AS rate
FROM otel_traces
WHERE Timestamp >= {start:DateTime64(9)} AND Timestamp < {end:DateTime64(9)}
  AND SpanKind IN ('Server','Consumer')
  AND ({env:String}='' OR ResourceAttributes['deployment.environment']={env:String})
GROUP BY ServiceName, bucket
ORDER BY bucket ASC`

	var rows []struct {
		Name   string  `json:"name"`
		Bucket int64   `json:"bucket"`
		Rate   float64 `json:"rate"`
	}
	if err := s.query(ctx, sql, params, &rows); err != nil {
		return nil, err
	}
	out := map[string][]float64{}
	for _, r := range rows {
		out[r.Name] = append(out[r.Name], round1(r.Rate))
	}
	return out, nil
}

func sortServices(s []model.Service, by string) {
	sort.SliceStable(s, func(i, j int) bool {
		switch by {
		case "rate":
			return s[i].Rate > s[j].Rate
		case "p95":
			return s[i].LatencyMs.P95 > s[j].LatencyMs.P95
		default:
			return s[i].ErrorRatio > s[j].ErrorRatio
		}
	})
}

// --- Logs -------------------------------------------------------------------

func (s *Store) Logs(ctx context.Context, q store.LogsQuery) (model.LogList, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := decodeCursor(q.Cursor)

	end := q.End
	if end.IsZero() {
		end = time.Now()
	}
	start := q.Start
	if start.IsZero() {
		start = end.Add(-1 * time.Hour)
	}

	params := map[string]string{
		"start":   chTime(start),
		"end":     chTime(end),
		"service": q.Service,
		"minSev":  fmt.Sprintf("%d", q.MinSeverity),
		"search":  q.Search,
		"trace":   q.TraceID,
		"limit":   fmt.Sprintf("%d", limit+1),
		"offset":  fmt.Sprintf("%d", offset),
	}

	const sql = `
SELECT toUnixTimestamp64Milli(Timestamp) AS timestamp,
       ServiceName AS serviceName,
       SeverityText AS severity,
       toInt32(SeverityNumber) AS severityNumber,
       Body AS body,
       TraceId AS traceId,
       SpanId AS spanId,
       LogAttributes AS attributes
FROM otel_logs
WHERE Timestamp >= {start:DateTime64(9)} AND Timestamp < {end:DateTime64(9)}
  AND ({service:String}='' OR ServiceName={service:String})
  AND ({minSev:UInt8}=0 OR SeverityNumber >= {minSev:UInt8})
  AND ({trace:String}='' OR TraceId={trace:String})
  AND ({search:String}='' OR positionCaseInsensitive(Body, {search:String}) > 0)
ORDER BY Timestamp DESC
LIMIT {limit:UInt32} OFFSET {offset:UInt32}`

	var rows []struct {
		Timestamp      int64             `json:"timestamp"`
		ServiceName    string            `json:"serviceName"`
		Severity       string            `json:"severity"`
		SeverityNumber int32             `json:"severityNumber"`
		Body           string            `json:"body"`
		TraceID        string            `json:"traceId"`
		SpanID         string            `json:"spanId"`
		Attributes     map[string]string `json:"attributes"`
	}
	if err := s.query(ctx, sql, params, &rows); err != nil {
		return model.LogList{}, err
	}

	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeCursor(offset + limit)
	}

	logs := make([]model.LogRecord, 0, len(rows))
	for _, r := range rows {
		logs = append(logs, model.LogRecord{
			Timestamp: r.Timestamp, ServiceName: r.ServiceName, Severity: r.Severity,
			SeverityNumber: r.SeverityNumber, Body: r.Body, TraceID: r.TraceID, SpanID: r.SpanID,
			Attributes: r.Attributes,
		})
	}
	return model.LogList{Logs: logs, NextCursor: next}, nil
}

// --- Traces -----------------------------------------------------------------

func (s *Store) Traces(ctx context.Context, q store.TracesQuery) (model.TraceList, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := decodeCursor(q.Cursor)

	end := q.End
	if end.IsZero() {
		end = time.Now()
	}
	start := q.Start
	if start.IsZero() {
		start = end.Add(-1 * time.Hour)
	}

	order := "r.Timestamp DESC"
	if q.Sort == "duration" {
		order = "r.Duration DESC"
	}

	params := map[string]string{
		"start":   chTime(start),
		"end":     chTime(end),
		"service": q.Service,
		"status":  q.Status,
		"minDur":  fmt.Sprintf("%f", q.MinDurationMs),
		"maxDur":  fmt.Sprintf("%f", q.MaxDurationMs),
		"limit":   fmt.Sprintf("%d", limit+1), // +1 zum Erkennen der nächsten Seite
		"offset":  fmt.Sprintf("%d", offset),
	}

	sql := `
SELECT r.TraceId AS traceId, r.SpanName AS rootName, r.ServiceName AS rootService,
       toUnixTimestamp64Milli(r.Timestamp) AS startTimeUnixMs,
       r.Duration/1e6 AS durationMs,
       agg.spanCount AS spanCount, agg.errorCount AS errorCount,
       if(agg.errorCount>0,'error','ok') AS status
FROM otel_traces AS r
INNER JOIN (
  SELECT TraceId, count() AS spanCount, countIf(StatusCode='Error') AS errorCount
  FROM otel_traces
  WHERE Timestamp >= {start:DateTime64(9)} AND Timestamp < {end:DateTime64(9)}
  GROUP BY TraceId
) AS agg ON agg.TraceId = r.TraceId
WHERE r.ParentSpanId = ''
  AND r.Timestamp >= {start:DateTime64(9)} AND r.Timestamp < {end:DateTime64(9)}
  AND ({service:String}='' OR r.ServiceName={service:String})
  AND ({status:String}='' OR if(agg.errorCount>0,'error','ok')={status:String})
  AND ({minDur:Float64}=0 OR r.Duration/1e6 >= {minDur:Float64})
  AND ({maxDur:Float64}=0 OR r.Duration/1e6 <= {maxDur:Float64})
ORDER BY ` + order + `
LIMIT {limit:UInt32} OFFSET {offset:UInt32}`

	var rows []struct {
		TraceID         string  `json:"traceId"`
		RootName        string  `json:"rootName"`
		RootService     string  `json:"rootService"`
		StartTimeUnixMs int64   `json:"startTimeUnixMs"`
		DurationMs      float64 `json:"durationMs"`
		SpanCount       int     `json:"spanCount"`
		ErrorCount      int     `json:"errorCount"`
		Status          string  `json:"status"`
	}
	if err := s.query(ctx, sql, params, &rows); err != nil {
		return model.TraceList{}, err
	}

	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeCursor(offset + limit)
	}

	traces := make([]model.TraceSummary, 0, len(rows))
	for _, r := range rows {
		traces = append(traces, model.TraceSummary{
			TraceID: r.TraceID, RootName: r.RootName, RootService: r.RootService,
			StartTimeUnixMs: r.StartTimeUnixMs, DurationMs: round1(r.DurationMs),
			SpanCount: r.SpanCount, ErrorCount: r.ErrorCount, Status: model.TraceStatus(r.Status),
		})
	}
	return model.TraceList{Traces: traces, NextCursor: next}, nil
}

// --- Trace-Detail -----------------------------------------------------------

func (s *Store) Trace(ctx context.Context, traceID string) (model.TraceDetail, error) {
	const sql = `
SELECT SpanId AS spanId, ParentSpanId AS parentSpanId, SpanName AS name,
       ServiceName AS service, lower(SpanKind) AS kind,
       (toUnixTimestamp64Nano(Timestamp) - min(toUnixTimestamp64Nano(Timestamp)) OVER ())/1e6 AS startOffsetMs,
       Duration/1e6 AS durationMs,
       toUnixTimestamp64Milli(Timestamp) AS startMs,
       StatusCode AS statusCode, StatusMessage AS statusMessage
FROM otel_traces
WHERE TraceId = {trace:String}
ORDER BY Timestamp ASC`

	var rows []struct {
		SpanID        string  `json:"spanId"`
		ParentSpanID  string  `json:"parentSpanId"`
		Name          string  `json:"name"`
		Service       string  `json:"service"`
		Kind          string  `json:"kind"`
		StartOffsetMs float64 `json:"startOffsetMs"`
		DurationMs    float64 `json:"durationMs"`
		StartMs       int64   `json:"startMs"`
		StatusCode    string  `json:"statusCode"`
		StatusMessage string  `json:"statusMessage"`
	}
	if err := s.query(ctx, sql, map[string]string{"trace": traceID}, &rows); err != nil {
		return model.TraceDetail{}, err
	}
	if len(rows) == 0 {
		return model.TraceDetail{}, store.ErrNotFound
	}

	// Spans + Tiefe aus ParentSpanId (ClickHouse liefert flach); depth-first sortieren.
	type node struct {
		span     model.Span
		children []string
	}
	nodes := map[string]*node{}
	var rootIDs []string
	var startMs int64 = rows[0].StartMs
	var maxEnd float64
	services := map[string]bool{}
	errorCount := 0

	for _, r := range rows {
		if r.StartMs < startMs {
			startMs = r.StartMs
		}
		status := model.TraceOK
		if r.StatusCode == "Error" {
			status = model.TraceError
			errorCount++
		}
		if e := r.StartOffsetMs + r.DurationMs; e > maxEnd {
			maxEnd = e
		}
		services[r.Service] = true
		nodes[r.SpanID] = &node{span: model.Span{
			SpanID: r.SpanID, ParentSpanID: r.ParentSpanID, Name: r.Name, Service: r.Service,
			Kind: model.SpanKind(r.Kind), StartOffsetMs: round1(r.StartOffsetMs), DurationMs: round1(r.DurationMs),
			Status: status, StatusMessage: r.StatusMessage,
		}}
	}
	for id, n := range nodes {
		p := n.span.ParentSpanID
		if p == "" || nodes[p] == nil {
			rootIDs = append(rootIDs, id)
		} else {
			nodes[p].children = append(nodes[p].children, id)
		}
	}

	// stabile Reihenfolge: nach Offset
	sortByOffset := func(ids []string) {
		sort.SliceStable(ids, func(i, j int) bool {
			return nodes[ids[i]].span.StartOffsetMs < nodes[ids[j]].span.StartOffsetMs
		})
	}
	sortByOffset(rootIDs)

	var spans []model.Span
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		n := nodes[id]
		n.span.Depth = depth
		spans = append(spans, n.span)
		sortByOffset(n.children)
		for _, c := range n.children {
			walk(c, depth+1)
		}
	}
	for _, id := range rootIDs {
		walk(id, 0)
	}

	svcList := make([]string, 0, len(services))
	for s := range services {
		svcList = append(svcList, s)
	}
	sort.Strings(svcList)

	return model.TraceDetail{
		TraceID: traceID, StartTimeUnixMs: startMs, DurationMs: round1(maxEnd),
		SpanCount: len(spans), ErrorCount: errorCount, Services: svcList, Spans: spans,
	}, nil
}

// --- HTTP / JSON-Helfer -----------------------------------------------------

// query führt eine parametrisierte Query aus und dekodiert FORMAT JSONEachRow
// zeilenweise in dest (Pointer auf Slice von Structs).
func (s *Store) query(ctx context.Context, sql string, params map[string]string, dest any) error {
	v := url.Values{}
	v.Set("database", s.cfg.Database)
	v.Set("output_format_json_quote_64bit_integers", "0")
	for k, val := range params {
		v.Set("param_"+k, val)
	}
	endpoint := strings.TrimRight(s.cfg.URL, "/") + "/?" + v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(sql+"\nFORMAT JSONEachRow"))
	if err != nil {
		return err
	}
	if s.cfg.User != "" {
		req.SetBasicAuth(s.cfg.User, s.cfg.Password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("clickhouse query: %w", err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clickhouse query status %d: %s", resp.StatusCode, strings.TrimSpace(body.String()))
	}
	return decodeJSONEachRow(body.Bytes(), dest)
}

// decodeJSONEachRow baut aus den NDJSON-Zeilen ein JSON-Array und unmarshalt es in dest.
func decodeJSONEachRow(data []byte, dest any) error {
	var arr bytes.Buffer
	arr.WriteByte('[')
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	first := true
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !first {
			arr.WriteByte(',')
		}
		arr.Write(line)
		first = false
	}
	if err := sc.Err(); err != nil {
		return err
	}
	arr.WriteByte(']')
	return json.Unmarshal(arr.Bytes(), dest)
}

func chTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000000000") }

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

func encodeCursor(offset int) string { return fmt.Sprintf("o%d", offset) }

func decodeCursor(c string) int {
	if len(c) < 2 || c[0] != 'o' {
		return 0
	}
	n := 0
	for _, ch := range c[1:] {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
