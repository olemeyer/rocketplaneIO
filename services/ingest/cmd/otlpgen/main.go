// Command otlpgen sendet kontinuierlich realistische OTLP-Traces (protobuf über
// OTLP/HTTP) an den ingest-Service — der realistische Ingestion-Pfad
// otlpgen -> ingest(OTLP) -> ClickHouse. Ersetzt den direkten ClickHouse-
// Generator (query/cmd/tracegen), wenn man den echten OTLP-Weg demonstrieren will.
//
// Nutzung:
//
//	go run ./cmd/otlpgen                          # sendet an http://localhost:4318
//	go run ./cmd/otlpgen -endpoint http://host:4318 -every 2s
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"log/slog"
	"math"
	mrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type childSpec struct {
	op, service string
	kind        tracepb.Span_SpanKind
	frac, dur   float64
	errWith     bool
}
type svcSpec struct {
	name, rootOp string
	kind         tracepb.Span_SpanKind
	errorRatio   float64
	p50, p99     float64
	children     []childSpec
}

var specs = []svcSpec{
	{"checkout-api", "POST /checkout", tracepb.Span_SPAN_KIND_SERVER, 0.042, 210, 1310, []childSpec{
		{"auth.verify", "auth", tracepb.Span_SPAN_KIND_INTERNAL, 0.03, 0.12, false},
		{"cart.load", "cart-service", tracepb.Span_SPAN_KIND_INTERNAL, 0.12, 0.16, false},
		{"payment.charge", "payment-gateway", tracepb.Span_SPAN_KIND_CLIENT, 0.30, 0.46, true},
		{"inventory.reserve", "inventory", tracepb.Span_SPAN_KIND_CLIENT, 0.78, 0.10, false},
		{"email.enqueue", "notifier", tracepb.Span_SPAN_KIND_INTERNAL, 0.90, 0.07, false},
	}},
	{"payment-gateway", "POST /charge", tracepb.Span_SPAN_KIND_SERVER, 0.008, 120, 520, []childSpec{
		{"stripe.charge", "stripe", tracepb.Span_SPAN_KIND_CLIENT, 0.15, 0.7, true},
	}},
	{"cart-service", "GET /cart", tracepb.Span_SPAN_KIND_SERVER, 0.001, 38, 180, []childSpec{
		{"cache.get", "redis", tracepb.Span_SPAN_KIND_CLIENT, 0.1, 0.3, false},
		{"db.query", "cart-service", tracepb.Span_SPAN_KIND_CLIENT, 0.4, 0.4, false},
	}},
	{"inventory", "POST /reserve", tracepb.Span_SPAN_KIND_SERVER, 0.0002, 22, 96, []childSpec{
		{"db.query", "inventory", tracepb.Span_SPAN_KIND_CLIENT, 0.2, 0.5, false},
	}},
}

func main() {
	var endpoint, every string
	flag.StringVar(&endpoint, "endpoint", envOr("OTLP_ENDPOINT", "http://localhost:4318"), "OTLP/HTTP base endpoint")
	flag.StringVar(&every, "every", "2s", "send interval")
	flag.Parse()
	interval, err := time.ParseDuration(every)
	if err != nil {
		interval = 2 * time.Second
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	rng := mrand.New(mrand.NewSource(7))
	base := strings.TrimRight(endpoint, "/")
	tracesURL := base + "/v1/traces"
	logsURL := base + "/v1/logs"
	metricsURL := base + "/v1/metrics"
	client := &http.Client{Timeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	log.Info("otlpgen started", "endpoint", base, "every", interval.String())

	for {
		select {
		case <-ctx.Done():
			log.Info("stopping")
			return
		case <-ticker.C:
			traceReq, logReq := buildRequests(rng)
			tbody, _ := proto.Marshal(traceReq)
			if err := send(ctx, client, tracesURL, tbody); err != nil {
				log.Error("send traces", "err", err)
				continue
			}
			lbody, _ := proto.Marshal(logReq)
			if err := send(ctx, client, logsURL, lbody); err != nil {
				log.Error("send logs", "err", err)
				continue
			}
			metricReq := buildMetrics(rng)
			mbody, _ := proto.Marshal(metricReq)
			if err := send(ctx, client, metricsURL, mbody); err != nil {
				log.Error("send metrics", "err", err)
				continue
			}
			log.Info("sent", "spans", countSpans(traceReq), "logs", countLogs(logReq), "metrics", countMetrics(metricReq))
		}
	}
}

// metricSpecs beschreibt die von otlpgen emittierten Metriken je Service.
var metricSpecs = []struct {
	name, unit, typ string
	base, amp       float64
}{
	{"system.cpu.utilization", "1", "gauge", 0.35, 0.25},
	{"system.memory.usage", "By", "gauge", 536870912, 134217728},
	{"http.server.active_requests", "{requests}", "gauge", 24, 18},
	{"http.server.request.count", "{requests}", "sum", 900, 400},
}

// buildMetrics erzeugt gauge- und sum-DataPoints je Service (ein ResourceMetrics
// pro Service, damit service.name im Resource korrekt gesetzt ist).
func buildMetrics(rng *mrand.Rand) *colmetricspb.ExportMetricsServiceRequest {
	nowNs := uint64(time.Now().UnixNano())
	var rms []*metricspb.ResourceMetrics
	for _, sp := range specs {
		var metrics []*metricspb.Metric
		for _, ms := range metricSpecs {
			v := ms.base + ms.amp*(rng.Float64()-0.5)*2
			if v < 0 {
				v = 0
			}
			dp := &metricspb.NumberDataPoint{
				TimeUnixNano: nowNs, StartTimeUnixNano: nowNs,
				Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: v},
			}
			m := &metricspb.Metric{Name: ms.name, Unit: ms.unit}
			if ms.typ == "sum" {
				m.Data = &metricspb.Metric_Sum{Sum: &metricspb.Sum{
					IsMonotonic:            true,
					AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
					DataPoints:             []*metricspb.NumberDataPoint{dp},
				}}
			} else {
				m.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{dp}}}
			}
			metrics = append(metrics, m)
		}
		rms = append(rms, &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				strKV("service.name", sp.name), strKV("deployment.environment", "production"),
			}},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Scope:   &commonpb.InstrumentationScope{Name: "rocketplane/otlpgen", Version: "0.1.0"},
				Metrics: metrics,
			}},
		})
	}
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: rms}
}

func countMetrics(req *colmetricspb.ExportMetricsServiceRequest) int {
	n := 0
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			n += len(sm.GetMetrics())
		}
	}
	return n
}

// buildRequests erzeugt korrelierte Traces + Logs (gleiche TraceId je Trace).
func buildRequests(rng *mrand.Rand) (*coltracepb.ExportTraceServiceRequest, *collogspb.ExportLogsServiceRequest) {
	now := time.Now()
	var rss []*tracepb.ResourceSpans
	var rls []*logspb.ResourceLogs
	for _, sp := range specs {
		n := 1 + rng.Intn(3)
		for i := 0; i < n; i++ {
			spans, logs := traceWithLogs(sp, now, rng)
			rss = append(rss, spans...)
			rls = append(rls, logs...)
		}
	}
	return &coltracepb.ExportTraceServiceRequest{ResourceSpans: rss},
		&collogspb.ExportLogsServiceRequest{ResourceLogs: rls}
}

// traceWithLogs erzeugt einen Trace (mehrere ResourceSpans) plus korrelierte Logs
// (gleiche TraceId): ein INFO-Startlog + bei Fehlern ein ERROR-Log.
func traceWithLogs(sp svcSpec, now time.Time, rng *mrand.Rand) ([]*tracepb.ResourceSpans, []*logspb.ResourceLogs) {
	traceID := randBytes(16)
	rootID := randBytes(8)
	durMs := sampleDurationMs(sp.p50, sp.p99, rng)
	isErr := rng.Float64() < sp.errorRatio
	startNs := uint64(now.Add(-time.Duration(rng.Intn(1500)) * time.Millisecond).UnixNano())

	out := []*tracepb.ResourceSpans{
		oneSpanResource(sp.name, span(traceID, rootID, nil, sp.rootOp, sp.kind, startNs, durMs, isErr)),
	}
	for _, c := range sp.children {
		offNs := uint64(durMs * c.frac * 1e6)
		cdur := durMs * c.dur * (0.8 + 0.4*rng.Float64())
		out = append(out, oneSpanResource(c.service,
			span(traceID, randBytes(8), rootID, c.op, c.kind, startNs+offNs, cdur, isErr && c.errWith)))
	}

	// korrelierte Logs (gleiche TraceId)
	logs := []*logspb.ResourceLogs{
		oneLogResource(sp.name, logRecord(traceID, rootID, startNs, "INFO", 9, sp.rootOp+" started")),
	}
	if isErr {
		endNs := startNs + uint64(durMs*1e6)
		logs = append(logs, oneLogResource(sp.name,
			logRecord(traceID, rootID, endNs, "ERROR", 17, sp.rootOp+" failed: downstream error")))
	}
	return out, logs
}

func oneLogResource(service string, lr *logspb.LogRecord) *logspb.ResourceLogs {
	return &logspb.ResourceLogs{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			strKV("service.name", service),
			strKV("deployment.environment", "production"),
		}},
		ScopeLogs: []*logspb.ScopeLogs{{
			Scope:      &commonpb.InstrumentationScope{Name: "rocketplane/otlpgen", Version: "0.1.0"},
			LogRecords: []*logspb.LogRecord{lr},
		}},
	}
}

func logRecord(traceID, spanID []byte, tsNs uint64, sevText string, sevNum logspb.SeverityNumber, body string) *logspb.LogRecord {
	return &logspb.LogRecord{
		TimeUnixNano:   tsNs,
		SeverityText:   sevText,
		SeverityNumber: sevNum,
		Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: body}},
		TraceId:        traceID,
		SpanId:         spanID,
		Attributes:     []*commonpb.KeyValue{strKV("log.gen", "otlpgen")},
	}
}

func oneSpanResource(service string, s *tracepb.Span) *tracepb.ResourceSpans {
	return &tracepb.ResourceSpans{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			strKV("service.name", service),
			strKV("deployment.environment", "production"),
		}},
		ScopeSpans: []*tracepb.ScopeSpans{{
			Scope: &commonpb.InstrumentationScope{Name: "rocketplane/otlpgen", Version: "0.1.0"},
			Spans: []*tracepb.Span{s},
		}},
	}
}

func span(traceID, spanID, parent []byte, name string, kind tracepb.Span_SpanKind, startNs uint64, durMs float64, isErr bool) *tracepb.Span {
	s := &tracepb.Span{
		TraceId: traceID, SpanId: spanID, ParentSpanId: parent,
		Name: name, Kind: kind,
		StartTimeUnixNano: startNs, EndTimeUnixNano: startNs + uint64(durMs*1e6),
		Attributes: []*commonpb.KeyValue{strKV("span.gen", "otlpgen")},
	}
	if isErr {
		s.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "request failed"}
	} else {
		s.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}
	}
	return s
}

func send(ctx context.Context, client *http.Client, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return &httpError{code: resp.StatusCode, body: b.String()}
	}
	return nil
}

type httpError struct {
	code int
	body string
}

func (e *httpError) Error() string {
	return "otlp export failed: status " + itoa(e.code) + " " + e.body
}

func sampleDurationMs(p50, p99 float64, rng *mrand.Rand) float64 {
	if p50 <= 0 {
		p50 = 1
	}
	sigma := math.Log(p99/p50) / 2.326
	if sigma <= 0 {
		sigma = 0.3
	}
	return math.Exp(math.Log(p50) + sigma*rng.NormFloat64())
}

func countSpans(req *coltracepb.ExportTraceServiceRequest) int {
	n := 0
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			n += len(ss.GetSpans())
		}
	}
	return n
}

func countLogs(req *collogspb.ExportLogsServiceRequest) int {
	n := 0
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			n += len(sl.GetLogRecords())
		}
	}
	return n
}

func strKV(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
