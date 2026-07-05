// Package model enthält die Domänentypen der Explore-API. Reines stdlib-Modell,
// geteilt von store, api und (später) der clickhouse-Implementierung. Die
// JSON-Tags sind der API-Kontrakt und werden vom Web-Client 1:1 gespiegelt.
package model

// HealthStatus ist der abgeleitete RED-Gesundheitszustand eines Service.
type HealthStatus string

const (
	HealthHealthy  HealthStatus = "healthy"
	HealthDegraded HealthStatus = "degraded"
	HealthCritical HealthStatus = "critical"
)

// TraceStatus ist der zusammengefasste Status eines Trace bzw. Span.
type TraceStatus string

const (
	TraceOK    TraceStatus = "ok"
	TraceError TraceStatus = "error"
)

// SpanKind spiegelt die OTel-SpanKind (kleingeschrieben) wider.
type SpanKind string

const (
	SpanServer   SpanKind = "server"
	SpanClient   SpanKind = "client"
	SpanInternal SpanKind = "internal"
	SpanProducer SpanKind = "producer"
	SpanConsumer SpanKind = "consumer"
)

// Latency bündelt die Latenz-Quantile in Millisekunden.
type Latency struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

// Sparkline ist eine kompakte Zeitreihe für die Service-Karte.
type Sparkline struct {
	Metric string    `json:"metric"` // "rate" | "errorRatio" | "p95"
	Values []float64 `json:"values"`
}

// Service ist eine RED-Zeile der Service-Health-Liste.
type Service struct {
	Name       string       `json:"name"`
	Status     HealthStatus `json:"status"`
	Rate       float64      `json:"rate"`       // req/s über das Fenster
	ErrorRatio float64      `json:"errorRatio"` // 0..1
	LatencyMs  Latency      `json:"latencyMs"`
	SpanCount  int64        `json:"spanCount"`
	ErrorCount int64        `json:"errorCount"`
	Sparkline  Sparkline    `json:"sparkline"`
}

// Window beschreibt das ausgewertete Zeitfenster (unix-Sekunden, step in s).
type Window struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Step  int64 `json:"step"`
}

// ServicesResult ist die Antwort von GET /api/v1/services.
type ServicesResult struct {
	Window   Window    `json:"window"`
	Services []Service `json:"services"`
}

// TraceSummary ist eine kompakte Zeile der Trace-Liste.
type TraceSummary struct {
	TraceID         string      `json:"traceId"`
	RootName        string      `json:"rootName"`
	RootService     string      `json:"rootService"`
	StartTimeUnixMs int64       `json:"startTimeUnixMs"`
	DurationMs      float64     `json:"durationMs"`
	SpanCount       int         `json:"spanCount"`
	ErrorCount      int         `json:"errorCount"`
	Status          TraceStatus `json:"status"`
}

// TraceList ist die Antwort von GET /api/v1/traces.
type TraceList struct {
	Traces     []TraceSummary `json:"traces"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

// Span ist eine Zeile im Trace-Waterfall. ParentSpanID == "" markiert die Wurzel.
type Span struct {
	SpanID        string      `json:"spanId"`
	ParentSpanID  string      `json:"parentSpanId"`
	Name          string      `json:"name"`
	Service       string      `json:"service"`
	Kind          SpanKind    `json:"kind"`
	StartOffsetMs float64     `json:"startOffsetMs"`
	DurationMs    float64     `json:"durationMs"`
	Depth         int         `json:"depth"`
	Status        TraceStatus `json:"status"`
	StatusMessage string      `json:"statusMessage,omitempty"`
}

// TraceDetail ist die Antwort von GET /api/v1/traces/{traceId}.
type TraceDetail struct {
	TraceID         string   `json:"traceId"`
	StartTimeUnixMs int64    `json:"startTimeUnixMs"`
	DurationMs      float64  `json:"durationMs"`
	SpanCount       int      `json:"spanCount"`
	ErrorCount      int      `json:"errorCount"`
	Services        []string `json:"services"`
	Spans           []Span   `json:"spans"`
}

// LogRecord ist eine Zeile im Log-Explorer.
type LogRecord struct {
	Timestamp      int64             `json:"timestamp"` // epoch ms
	ServiceName    string            `json:"serviceName"`
	Severity       string            `json:"severity"`       // INFO | WARN | ERROR | DEBUG ...
	SeverityNumber int32             `json:"severityNumber"` // OTLP 1..24
	Body           string            `json:"body"`
	TraceID        string            `json:"traceId,omitempty"`
	SpanID         string            `json:"spanId,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

// LogList ist die Antwort von GET /api/v1/logs.
type LogList struct {
	Logs       []LogRecord `json:"logs"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

// Point ist ein Zeitreihen-Datenpunkt (t = unix-Sekunden).
type Point struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

// Alert ist eine ausgewertete Alert-Regel gegen den aktuellen Service-Zustand.
type Alert struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Service   string  `json:"service"`
	Metric    string  `json:"metric"` // "errorRatio" | "p95"
	Threshold float64 `json:"threshold"`
	Severity  string  `json:"severity"` // "warning" | "critical"
	Value     float64 `json:"value"`
	Firing    bool    `json:"firing"`
}

// AlertList ist die Antwort von GET /api/v1/alerts.
type AlertList struct {
	Alerts []Alert `json:"alerts"`
	Firing int     `json:"firing"`
	Total  int     `json:"total"`
}

// OperationStat ist die RED-Aufschlüsselung einer Operation (SpanName) eines Service.
type OperationStat struct {
	Name       string  `json:"name"`
	SpanCount  int64   `json:"spanCount"`
	ErrorCount int64   `json:"errorCount"`
	ErrorRatio float64 `json:"errorRatio"`
	P95Ms      float64 `json:"p95Ms"`
}

// Dependency ist ein nachgelagerter Service, den dieser Service aufruft.
type Dependency struct {
	Service    string  `json:"service"`
	CallCount  int64   `json:"callCount"`
	ErrorRatio float64 `json:"errorRatio"`
	P95Ms      float64 `json:"p95Ms"`
}

// MapNode ist ein Service-Knoten in der Service-Map.
type MapNode struct {
	Name       string       `json:"name"`
	Status     HealthStatus `json:"status"`
	Rate       float64      `json:"rate"`
	ErrorRatio float64      `json:"errorRatio"`
	P95Ms      float64      `json:"p95Ms"`
	SpanCount  int64        `json:"spanCount"`
}

// MapEdge ist eine gerichtete Aufruf-Kante (from ruft to auf).
type MapEdge struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	CallCount  int64   `json:"callCount"`
	ErrorRatio float64 `json:"errorRatio"`
}

// ServiceMap ist die Antwort von GET /api/v1/service-map.
type ServiceMap struct {
	Window Window    `json:"window"`
	Nodes  []MapNode `json:"nodes"`
	Edges  []MapEdge `json:"edges"`
}

// ServiceDetail ist die Antwort von GET /api/v1/services/{name}.
type ServiceDetail struct {
	Name         string          `json:"name"`
	Status       HealthStatus    `json:"status"`
	Rate         float64         `json:"rate"`
	ErrorRatio   float64         `json:"errorRatio"`
	LatencyMs    Latency         `json:"latencyMs"`
	SpanCount    int64           `json:"spanCount"`
	ErrorCount   int64           `json:"errorCount"`
	Window       Window          `json:"window"`
	P95Series    []Point         `json:"p95Series"`
	RateSeries   []Point         `json:"rateSeries"`
	ErrorSeries  []Point         `json:"errorSeries"`
	Operations   []OperationStat `json:"operations"`
	Dependencies []Dependency    `json:"dependencies"`
}

// DeriveHealth leitet den Health-Status aus Error-Ratio und p95 gegen ein
// p95-SLO ab. Bewusst zentral, damit Seed und ClickHouse identisch klassifizieren.
func DeriveHealth(errorRatio, p95, sloP95 float64) HealthStatus {
	switch {
	case errorRatio > 0.02 || p95 > 2*sloP95:
		return HealthCritical
	case errorRatio > 0.005 || p95 > sloP95:
		return HealthDegraded
	default:
		return HealthHealthy
	}
}
