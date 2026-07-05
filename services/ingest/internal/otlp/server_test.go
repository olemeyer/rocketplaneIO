package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rocketplaneio/rocketplane/services/ingest/internal/chsink"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// fakeSink erfasst eingespeiste Zeilen.
type fakeSink struct {
	rows      []chsink.Row
	logRows   []chsink.LogRow
	gaugeRows []chsink.MetricRow
	sumRows   []chsink.MetricRow
	pingErr   error
}

func (f *fakeSink) Insert(_ context.Context, rows []chsink.Row) error {
	f.rows = append(f.rows, rows...)
	return nil
}
func (f *fakeSink) InsertLogs(_ context.Context, rows []chsink.LogRow) error {
	f.logRows = append(f.logRows, rows...)
	return nil
}
func (f *fakeSink) InsertMetrics(_ context.Context, gauges, sums []chsink.MetricRow) error {
	f.gaugeRows = append(f.gaugeRows, gauges...)
	f.sumRows = append(f.sumRows, sums...)
	return nil
}
func (f *fakeSink) Ping(context.Context) error { return f.pingErr }

func newHandler(sink Sink) http.Handler {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), sink).Mux()
}

// sampleRequest baut einen OTLP-Trace-Export mit einem Root + einem Child.
func sampleRequest() *coltracepb.ExportTraceServiceRequest {
	strAttr := func(k, v string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
	}
	traceID := []byte{0x7f, 0x3a, 0x9c, 0x2b, 0x1e, 0x8d, 0x4a, 0x5f, 0x9c, 0x0b, 0x3d, 0x2e, 0x1a, 0x4f, 0x6c, 0x7d}
	rootID := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	childID := []byte{9, 10, 11, 12, 13, 14, 15, 16}
	return &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				strAttr("service.name", "checkout-api"),
				strAttr("deployment.environment", "production"),
			}},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Scope: &commonpb.InstrumentationScope{Name: "test", Version: "1.0"},
				Spans: []*tracepb.Span{
					{
						TraceId: traceID, SpanId: rootID, ParentSpanId: nil,
						Name: "POST /checkout", Kind: tracepb.Span_SPAN_KIND_SERVER,
						StartTimeUnixNano: 1_700_000_000_000_000_000, EndTimeUnixNano: 1_700_000_000_342_000_000,
						Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "payment declined"},
					},
					{
						TraceId: traceID, SpanId: childID, ParentSpanId: rootID,
						Name: "payment.charge", Kind: tracepb.Span_SPAN_KIND_CLIENT,
						StartTimeUnixNano: 1_700_000_000_100_000_000, EndTimeUnixNano: 1_700_000_000_258_000_000,
						Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
					},
				},
			}},
		}},
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(&fakeSink{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["service"] != "ingest" {
		t.Fatalf("unexpected: %v", body)
	}
}

func TestIngestProtobuf(t *testing.T) {
	sink := &fakeSink{}
	body, _ := proto.Marshal(sampleRequest())
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	newHandler(sink).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-protobuf" {
		t.Errorf("response CT = %q", ct)
	}
	assertSampleRows(t, sink.rows)
}

func TestIngestJSON(t *testing.T) {
	sink := &fakeSink{}
	body, _ := protojson.Marshal(sampleRequest())
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newHandler(sink).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	assertSampleRows(t, sink.rows)
}

func TestIngestGzip(t *testing.T) {
	sink := &fakeSink{}
	raw, _ := proto.Marshal(sampleRequest())
	var gzbuf bytes.Buffer
	gw := gzip.NewWriter(&gzbuf)
	_, _ = gw.Write(raw)
	_ = gw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", &gzbuf)
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	newHandler(sink).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	assertSampleRows(t, sink.rows)
}

func TestIngestMalformed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader([]byte("not-protobuf")))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	newHandler(&fakeSink{}).ServeHTTP(rec, req)
	// "not-protobuf" ist zufällig kein gültiges Protobuf -> 400.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestIngestMetrics(t *testing.T) {
	sink := &fakeSink{}
	body, _ := proto.Marshal(sampleMetricsRequest())
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	newHandler(sink).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(sink.gaugeRows) != 1 || len(sink.sumRows) != 1 {
		t.Fatalf("gauges=%d sums=%d, want 1/1", len(sink.gaugeRows), len(sink.sumRows))
	}
	g := sink.gaugeRows[0]
	if g.ServiceName != "checkout-api" || g.MetricName != "system.cpu.utilization" || g.Value != 0.42 {
		t.Errorf("gauge mapping: %+v", g)
	}
	s := sink.sumRows[0]
	if s.MetricName != "http.server.request.count" || s.Value != 1234 || !s.IsMonotonic {
		t.Errorf("sum mapping: %+v", s)
	}
}

func TestIngestLogs(t *testing.T) {
	sink := &fakeSink{}
	body, _ := proto.Marshal(sampleLogsRequest())
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	newHandler(sink).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(sink.logRows) != 2 {
		t.Fatalf("logRows = %d, want 2", len(sink.logRows))
	}
	info := sink.logRows[0]
	if info.ServiceName != "checkout-api" || info.SeverityText != "INFO" || info.SeverityNumber != 9 {
		t.Errorf("info log mapping: %+v", info)
	}
	if info.Body != "checkout requested" {
		t.Errorf("body = %q", info.Body)
	}
	if info.TraceId != "7f3a9c2b1e8d4a5f9c0b3d2e1a4f6c7d" {
		t.Errorf("traceId = %q", info.TraceId)
	}
	err := sink.logRows[1]
	if err.SeverityText != "ERROR" || err.SeverityNumber != 17 {
		t.Errorf("error log mapping: %+v", err)
	}
}

func assertSampleRows(t *testing.T, rows []chsink.Row) {
	t.Helper()
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	root := rows[0]
	if root.TraceId != "7f3a9c2b1e8d4a5f9c0b3d2e1a4f6c7d" {
		t.Errorf("traceId = %q", root.TraceId)
	}
	if root.SpanId != "0102030405060708" || root.ParentSpanId != "" {
		t.Errorf("root ids = %q / parent %q", root.SpanId, root.ParentSpanId)
	}
	if root.SpanKind != "Server" || root.StatusCode != "Error" || root.StatusMessage != "payment declined" {
		t.Errorf("root mapping: kind=%q status=%q msg=%q", root.SpanKind, root.StatusCode, root.StatusMessage)
	}
	if root.ServiceName != "checkout-api" {
		t.Errorf("serviceName = %q", root.ServiceName)
	}
	if root.Duration != 342_000_000 { // 342ms in ns
		t.Errorf("duration = %d, want 342000000", root.Duration)
	}
	child := rows[1]
	if child.ParentSpanId != "0102030405060708" || child.SpanKind != "Client" {
		t.Errorf("child mapping: parent=%q kind=%q", child.ParentSpanId, child.SpanKind)
	}
}

// sampleLogsRequest baut einen OTLP-Logs-Export mit einem INFO- und einem ERROR-Record.
func sampleLogsRequest() *collogspb.ExportLogsServiceRequest {
	str := func(k, v string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
	}
	traceID := []byte{0x7f, 0x3a, 0x9c, 0x2b, 0x1e, 0x8d, 0x4a, 0x5f, 0x9c, 0x0b, 0x3d, 0x2e, 0x1a, 0x4f, 0x6c, 0x7d}
	body := func(s string) *commonpb.AnyValue {
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
	}
	return &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{str("service.name", "checkout-api")}},
			ScopeLogs: []*logspb.ScopeLogs{{
				Scope: &commonpb.InstrumentationScope{Name: "test", Version: "1.0"},
				LogRecords: []*logspb.LogRecord{
					{
						TimeUnixNano: 1_700_000_000_000_000_000, SeverityText: "INFO",
						SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
						Body:           body("checkout requested"), TraceId: traceID,
					},
					{
						TimeUnixNano: 1_700_000_000_100_000_000, SeverityText: "ERROR",
						SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_ERROR,
						Body:           body("checkout failed"), TraceId: traceID,
						Attributes: []*commonpb.KeyValue{str("exception.type", "DownstreamError")},
					},
				},
			}},
		}},
	}
}

// sampleMetricsRequest baut einen OTLP-Metrics-Export mit einem Gauge + einem Sum.
func sampleMetricsRequest() *colmetricspb.ExportMetricsServiceRequest {
	str := func(k, v string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
	}
	gauge := &metricspb.Metric{
		Name: "system.cpu.utilization", Unit: "1",
		Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{
			{TimeUnixNano: 1_700_000_000_000_000_000, Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 0.42}},
		}}},
	}
	sum := &metricspb.Metric{
		Name: "http.server.request.count", Unit: "{requests}",
		Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
			IsMonotonic:            true,
			AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			DataPoints: []*metricspb.NumberDataPoint{
				{TimeUnixNano: 1_700_000_000_000_000_000, Value: &metricspb.NumberDataPoint_AsInt{AsInt: 1234}},
			},
		}},
	}
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{str("service.name", "checkout-api")}},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Scope:   &commonpb.InstrumentationScope{Name: "test", Version: "1.0"},
				Metrics: []*metricspb.Metric{gauge, sum},
			}},
		}},
	}
}
