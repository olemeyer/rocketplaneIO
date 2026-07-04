package otlp

import (
	"encoding/hex"
	"strconv"
	"time"

	"github.com/rocketplaneio/rocketplane/services/ingest/internal/chsink"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// spanKindString bildet den OTLP-SpanKind-Enum auf die PascalCase-Strings ab,
// die der Collector-clickhouse-exporter schreibt (pdata Kind().String()).
func spanKindString(k tracepb.Span_SpanKind) string {
	switch k {
	case tracepb.Span_SPAN_KIND_INTERNAL:
		return "Internal"
	case tracepb.Span_SPAN_KIND_SERVER:
		return "Server"
	case tracepb.Span_SPAN_KIND_CLIENT:
		return "Client"
	case tracepb.Span_SPAN_KIND_PRODUCER:
		return "Producer"
	case tracepb.Span_SPAN_KIND_CONSUMER:
		return "Consumer"
	default:
		return "Unspecified"
	}
}

// statusCodeString bildet den OTLP-StatusCode auf Unset/Ok/Error ab.
func statusCodeString(s *tracepb.Status) (string, string) {
	if s == nil {
		return "Unset", ""
	}
	switch s.GetCode() {
	case tracepb.Status_STATUS_CODE_OK:
		return "Ok", s.GetMessage()
	case tracepb.Status_STATUS_CODE_ERROR:
		return "Error", s.GetMessage()
	default:
		return "Unset", s.GetMessage()
	}
}

// attrsToMap wandelt OTLP-KeyValues in eine flache string-Map (Werte stringified).
func attrsToMap(kvs []*commonpb.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if kv == nil {
			continue
		}
		m[kv.GetKey()] = anyValueString(kv.GetValue())
	}
	return m
}

// anyValueString stringified einen OTLP-AnyValue.
func anyValueString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(val.BoolValue)
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(val.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(val.DoubleValue, 'g', -1, 64)
	case *commonpb.AnyValue_BytesValue:
		return hex.EncodeToString(val.BytesValue)
	default:
		return ""
	}
}

// hexOrEmpty encodet einen Trace-/Span-ID-Bytes-Slice zu lowercase-hex; leere IDs
// (Root-Spans) werden zu "".
func hexOrEmpty(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}

// tracesToRows extrahiert alle Spans aus ResourceSpans -> ScopeSpans -> Spans und
// baut otel_traces-Zeilen. service.name kommt aus den Resource-Attributen.
func tracesToRows(resourceSpans []*tracepb.ResourceSpans) []chsink.Row {
	var rows []chsink.Row
	for _, rs := range resourceSpans {
		if rs == nil {
			continue
		}
		var resAttrs map[string]string
		if rs.GetResource() != nil {
			resAttrs = attrsToMap(rs.GetResource().GetAttributes())
		} else {
			resAttrs = map[string]string{}
		}
		serviceName := resAttrs["service.name"]

		for _, ss := range rs.GetScopeSpans() {
			if ss == nil {
				continue
			}
			scopeName, scopeVersion := "", ""
			if sc := ss.GetScope(); sc != nil {
				scopeName = sc.GetName()
				scopeVersion = sc.GetVersion()
			}
			for _, sp := range ss.GetSpans() {
				if sp == nil {
					continue
				}
				statusCode, statusMsg := statusCodeString(sp.GetStatus())
				var dur uint64
				if end, start := sp.GetEndTimeUnixNano(), sp.GetStartTimeUnixNano(); end > start {
					dur = end - start
				}
				rows = append(rows, chsink.Row{
					Timestamp:          nanoToCH(sp.GetStartTimeUnixNano()),
					TraceId:            hexOrEmpty(sp.GetTraceId()),
					SpanId:             hexOrEmpty(sp.GetSpanId()),
					ParentSpanId:       hexOrEmpty(sp.GetParentSpanId()),
					TraceState:         sp.GetTraceState(),
					SpanName:           sp.GetName(),
					SpanKind:           spanKindString(sp.GetKind()),
					ServiceName:        serviceName,
					ResourceAttributes: resAttrs,
					ScopeName:          scopeName,
					ScopeVersion:       scopeVersion,
					SpanAttributes:     attrsToMap(sp.GetAttributes()),
					Duration:           dur,
					StatusCode:         statusCode,
					StatusMessage:      statusMsg,
				})
			}
		}
	}
	return rows
}

// nanoToCH formatiert Unix-Nanosekunden als ClickHouse-DateTime64(9)-String.
func nanoToCH(ns uint64) string {
	t := time.Unix(0, int64(ns)).UTC()
	return t.Format("2006-01-02 15:04:05.000000000")
}
