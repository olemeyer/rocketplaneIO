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

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// fakeSink erfasst eingespeiste Zeilen.
type fakeSink struct {
	rows    []chsink.Row
	pingErr error
}

func (f *fakeSink) Insert(_ context.Context, rows []chsink.Row) error {
	f.rows = append(f.rows, rows...)
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

func TestMetricsLogsNotImplemented(t *testing.T) {
	h := newHandler(&fakeSink{})
	for _, p := range []string{"/v1/metrics", "/v1/logs"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, nil))
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("POST %s = %d, want 501", p, rec.Code)
		}
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
