// Package otlp implementiert die OTLP/HTTP-Ingestion des ingest-Service:
// echtes Parsen von OTLP-Traces (protobuf und JSON, inkl. gzip) und Schreiben
// nach ClickHouse (otel_traces). Metrics/Logs sind vorbereitet (501), Traces
// sind voll funktional.
package otlp

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/rocketplaneio/rocketplane/services/ingest/internal/chsink"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maxBody = 16 << 20 // 16 MiB

// Sink ist die Schreibsenke (von chsink.Sink erfüllt).
type Sink interface {
	Insert(ctx context.Context, rows []chsink.Row) error
	InsertLogs(ctx context.Context, rows []chsink.LogRow) error
	InsertMetrics(ctx context.Context, gauges, sums []chsink.MetricRow) error
	Ping(ctx context.Context) error
}

// Handler bündelt die OTLP-Routen.
type Handler struct {
	log  *slog.Logger
	sink Sink
}

// New erstellt einen Ingestion-Handler.
func New(log *slog.Logger, sink Sink) *Handler {
	return &Handler{log: log, sink: sink}
}

// Mux registriert alle Routen.
func (h *Handler) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.ready)
	mux.HandleFunc("POST /v1/traces", h.traces)
	mux.HandleFunc("POST /v1/logs", h.logs)
	mux.HandleFunc("POST /v1/metrics", h.metrics)
	return h.withLogging(mux)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "ingest"})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.sink.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// traces verarbeitet OTLP/HTTP-Trace-Exporte.
func (h *Handler) traces(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req := &coltracepb.ExportTraceServiceRequest{}
	isJSON := isJSONContentType(r.Header.Get("Content-Type"))
	if isJSON {
		err = protojson.Unmarshal(body, req)
	} else {
		err = proto.Unmarshal(body, req)
	}
	if err != nil {
		h.log.Warn("otlp decode", "err", err, "json", isJSON)
		http.Error(w, "malformed OTLP payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	rows := tracesToRows(req.GetResourceSpans())
	if err := h.sink.Insert(r.Context(), rows); err != nil {
		h.log.Error("clickhouse insert", "err", err, "spans", len(rows))
		http.Error(w, "storage error", http.StatusServiceUnavailable)
		return
	}
	h.log.Debug("ingested traces", "spans", len(rows))

	// Erfolgreiche, leere OTLP-Response im selben Encoding wie der Request.
	resp := &coltracepb.ExportTraceServiceResponse{}
	if isJSON {
		out, _ := protojson.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
		return
	}
	out, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// logs verarbeitet OTLP/HTTP-Log-Exporte.
func (h *Handler) logs(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req := &collogspb.ExportLogsServiceRequest{}
	isJSON := isJSONContentType(r.Header.Get("Content-Type"))
	if isJSON {
		err = protojson.Unmarshal(body, req)
	} else {
		err = proto.Unmarshal(body, req)
	}
	if err != nil {
		h.log.Warn("otlp logs decode", "err", err, "json", isJSON)
		http.Error(w, "malformed OTLP payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	rows := logsToRows(req.GetResourceLogs())
	if err := h.sink.InsertLogs(r.Context(), rows); err != nil {
		h.log.Error("clickhouse insert logs", "err", err, "records", len(rows))
		http.Error(w, "storage error", http.StatusServiceUnavailable)
		return
	}
	h.log.Debug("ingested logs", "records", len(rows))

	resp := &collogspb.ExportLogsServiceResponse{}
	if isJSON {
		out, _ := protojson.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
		return
	}
	out, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// metrics verarbeitet OTLP/HTTP-Metrik-Exporte (gauge + sum).
func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req := &colmetricspb.ExportMetricsServiceRequest{}
	isJSON := isJSONContentType(r.Header.Get("Content-Type"))
	if isJSON {
		err = protojson.Unmarshal(body, req)
	} else {
		err = proto.Unmarshal(body, req)
	}
	if err != nil {
		h.log.Warn("otlp metrics decode", "err", err, "json", isJSON)
		http.Error(w, "malformed OTLP payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	gauges, sums := metricsToRows(req.GetResourceMetrics())
	if err := h.sink.InsertMetrics(r.Context(), gauges, sums); err != nil {
		h.log.Error("clickhouse insert metrics", "err", err, "gauges", len(gauges), "sums", len(sums))
		http.Error(w, "storage error", http.StatusServiceUnavailable)
		return
	}
	h.log.Debug("ingested metrics", "gauges", len(gauges), "sums", len(sums))

	resp := &colmetricspb.ExportMetricsServiceResponse{}
	if isJSON {
		out, _ := protojson.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
		return
	}
	out, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func (h *Handler) notImplemented(signal string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error":  "not implemented",
			"signal": signal,
			"hint":   "OTLP metrics/logs ingestion arrives in a later milestone.",
		})
	}
}

func (h *Handler) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.log.Debug("request", "method", r.Method, "path", r.URL.Path, "ct", r.Header.Get("Content-Type"))
		next.ServeHTTP(w, r)
	})
}

// readBody liest den (optional gzip-komprimierten) Request-Body mit Limit.
func readBody(r *http.Request) ([]byte, error) {
	var reader io.Reader = http.MaxBytesReader(nil, r.Body, maxBody)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, errors.New("invalid gzip body")
		}
		defer gz.Close()
		reader = gz
	}
	return io.ReadAll(reader)
}

func isJSONContentType(ct string) bool {
	for i := 0; i < len(ct); i++ {
		if ct[i] == ';' {
			ct = ct[:i]
			break
		}
	}
	return ct == "application/json"
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
