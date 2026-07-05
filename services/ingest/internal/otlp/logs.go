package otlp

import (
	"encoding/base64"
	"encoding/json"
	"strconv"

	"github.com/rocketplaneio/rocketplane/services/ingest/internal/chsink"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
)

// logsToRows extrahiert LogRecords aus ResourceLogs -> ScopeLogs -> LogRecords
// und baut otel_logs-Zeilen (Mapping identisch zum Collector-clickhouse-exporter).
func logsToRows(resourceLogs []*logspb.ResourceLogs) []chsink.LogRow {
	var rows []chsink.LogRow
	for _, rl := range resourceLogs {
		if rl == nil {
			continue
		}
		resAttrs := map[string]string{}
		if rl.GetResource() != nil {
			resAttrs = attrsToMap(rl.GetResource().GetAttributes())
		}
		serviceName := resAttrs["service.name"]
		resSchema := rl.GetSchemaUrl()

		for _, sl := range rl.GetScopeLogs() {
			if sl == nil {
				continue
			}
			scopeName, scopeVersion, scopeSchema := "", "", sl.GetSchemaUrl()
			var scopeAttrs map[string]string
			if sc := sl.GetScope(); sc != nil {
				scopeName = sc.GetName()
				scopeVersion = sc.GetVersion()
				scopeAttrs = attrsToMap(sc.GetAttributes())
			}
			for _, lr := range sl.GetLogRecords() {
				if lr == nil {
					continue
				}
				ts := lr.GetTimeUnixNano()
				if ts == 0 {
					ts = lr.GetObservedTimeUnixNano()
				}
				rows = append(rows, chsink.LogRow{
					Timestamp:          nanoToCH(ts),
					TraceId:            hexOrEmpty(lr.GetTraceId()),
					SpanId:             hexOrEmpty(lr.GetSpanId()),
					TraceFlags:         uint8(lr.GetFlags()),
					SeverityText:       lr.GetSeverityText(),
					SeverityNumber:     uint8(lr.GetSeverityNumber()),
					ServiceName:        serviceName,
					Body:               anyValueToString(lr.GetBody()),
					ResourceSchemaUrl:  resSchema,
					ResourceAttributes: resAttrs,
					ScopeSchemaUrl:     scopeSchema,
					ScopeName:          scopeName,
					ScopeVersion:       scopeVersion,
					ScopeAttributes:    scopeAttrs,
					LogAttributes:      attrsToMap(lr.GetAttributes()),
				})
			}
		}
	}
	return rows
}

// anyValueToString bildet pcommon.Value.AsString() für einen rohen AnyValue nach.
func anyValueToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return v.GetStringValue()
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(v.GetBoolValue())
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(v.GetIntValue(), 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(v.GetDoubleValue(), 'f', -1, 64)
	case *commonpb.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(v.GetBytesValue())
	case *commonpb.AnyValue_ArrayValue, *commonpb.AnyValue_KvlistValue:
		buf, _ := json.Marshal(v.GetValue())
		return string(buf)
	default:
		return ""
	}
}
