// Package chsink schreibt Trace-Spans in die ClickHouse-Tabelle otel_traces
// (JSONEachRow über das HTTP-Interface). Die Zeilenform entspricht exakt dem
// Schema des OTel-Collector-clickhouse-exporters, sodass der query-Service
// identisch liest, egal ob der Collector oder dieser Service ingested hat.
package chsink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Row ist eine otel_traces-Zeile.
type Row struct {
	Timestamp          string            `json:"Timestamp"`
	TraceId            string            `json:"TraceId"`
	SpanId             string            `json:"SpanId"`
	ParentSpanId       string            `json:"ParentSpanId"`
	TraceState         string            `json:"TraceState"`
	SpanName           string            `json:"SpanName"`
	SpanKind           string            `json:"SpanKind"`
	ServiceName        string            `json:"ServiceName"`
	ResourceAttributes map[string]string `json:"ResourceAttributes"`
	ScopeName          string            `json:"ScopeName"`
	ScopeVersion       string            `json:"ScopeVersion"`
	SpanAttributes     map[string]string `json:"SpanAttributes"`
	Duration           uint64            `json:"Duration"`
	StatusCode         string            `json:"StatusCode"`
	StatusMessage      string            `json:"StatusMessage"`
}

// LogRow ist eine otel_logs-Zeile.
type LogRow struct {
	Timestamp          string            `json:"Timestamp"`
	TraceId            string            `json:"TraceId"`
	SpanId             string            `json:"SpanId"`
	TraceFlags         uint8             `json:"TraceFlags"`
	SeverityText       string            `json:"SeverityText"`
	SeverityNumber     uint8             `json:"SeverityNumber"`
	ServiceName        string            `json:"ServiceName"`
	Body               string            `json:"Body"`
	ResourceSchemaUrl  string            `json:"ResourceSchemaUrl"`
	ResourceAttributes map[string]string `json:"ResourceAttributes"`
	ScopeSchemaUrl     string            `json:"ScopeSchemaUrl"`
	ScopeName          string            `json:"ScopeName"`
	ScopeVersion       string            `json:"ScopeVersion"`
	ScopeAttributes    map[string]string `json:"ScopeAttributes"`
	LogAttributes      map[string]string `json:"LogAttributes"`
}

// MetricRow ist eine otel_metrics_gauge/-_sum-Zeile. Die Sum-spezifischen Felder
// werden per JSON weggelassen, wenn nicht gesetzt (gauge nutzt sie nicht).
type MetricRow struct {
	ResourceAttributes map[string]string `json:"ResourceAttributes"`
	ScopeName          string            `json:"ScopeName"`
	ScopeVersion       string            `json:"ScopeVersion"`
	ServiceName        string            `json:"ServiceName"`
	MetricName         string            `json:"MetricName"`
	MetricDescription  string            `json:"MetricDescription"`
	MetricUnit         string            `json:"MetricUnit"`
	Attributes         map[string]string `json:"Attributes"`
	StartTimeUnix      string            `json:"StartTimeUnix"`
	TimeUnix           string            `json:"TimeUnix"`
	Value              float64           `json:"Value"`
	Flags              uint32            `json:"Flags"`
	// nur Sum:
	AggregationTemporality int32 `json:"AggregationTemporality,omitempty"`
	IsMonotonic            bool  `json:"IsMonotonic,omitempty"`
}

// Config beschreibt die ClickHouse-Verbindung.
type Config struct {
	URL      string
	Database string
	User     string
	Password string
	Timeout  time.Duration
}

// Sink schreibt Batches nach ClickHouse.
type Sink struct {
	cfg    Config
	client *http.Client
}

// New erstellt einen Sink.
func New(cfg Config, client *http.Client) *Sink {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Sink{cfg: cfg, client: client}
}

// Ping prüft die Erreichbarkeit über /ping.
func (s *Sink) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cfg.URL, "/")+"/ping", nil)
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

// Insert schreibt Trace-Spans als JSONEachRow in otel_traces.
func (s *Sink) Insert(ctx context.Context, rows []Row) error {
	return s.insertRows(ctx, "otel_traces", len(rows), func(enc *json.Encoder) error {
		for i := range rows {
			if err := enc.Encode(rows[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// InsertLogs schreibt Log-Records als JSONEachRow in otel_logs.
func (s *Sink) InsertLogs(ctx context.Context, rows []LogRow) error {
	return s.insertRows(ctx, "otel_logs", len(rows), func(enc *json.Encoder) error {
		for i := range rows {
			if err := enc.Encode(rows[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// InsertMetrics schreibt Gauge- und Sum-Rows in ihre jeweiligen Tabellen.
func (s *Sink) InsertMetrics(ctx context.Context, gauges, sums []MetricRow) error {
	if err := s.insertRows(ctx, "otel_metrics_gauge", len(gauges), func(enc *json.Encoder) error {
		for i := range gauges {
			if err := enc.Encode(gauges[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return s.insertRows(ctx, "otel_metrics_sum", len(sums), func(enc *json.Encoder) error {
		for i := range sums {
			if err := enc.Encode(sums[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// insertRows POSTet den JSONEachRow-Body an INSERT INTO <table>.
func (s *Sink) insertRows(ctx context.Context, table string, n int, encode func(*json.Encoder) error) error {
	if n == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := encode(json.NewEncoder(&buf)); err != nil {
		return err
	}

	v := url.Values{}
	v.Set("database", s.cfg.Database)
	v.Set("query", "INSERT INTO "+table+" FORMAT JSONEachRow")
	endpoint := strings.TrimRight(s.cfg.URL, "/") + "/?" + v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return err
	}
	if s.cfg.User != "" {
		req.SetBasicAuth(s.cfg.User, s.cfg.Password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return fmt.Errorf("clickhouse insert %s status %d: %s", table, resp.StatusCode, strings.TrimSpace(b.String()))
	}
	return nil
}
