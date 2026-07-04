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

// Insert schreibt die Zeilen als JSONEachRow in otel_traces.
func (s *Sink) Insert(ctx context.Context, rows []Row) error {
	if len(rows) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i := range rows {
		if err := enc.Encode(rows[i]); err != nil {
			return err
		}
	}

	v := url.Values{}
	v.Set("database", s.cfg.Database)
	v.Set("query", "INSERT INTO otel_traces FORMAT JSONEachRow")
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
		return fmt.Errorf("clickhouse insert status %d: %s", resp.StatusCode, strings.TrimSpace(b.String()))
	}
	return nil
}
