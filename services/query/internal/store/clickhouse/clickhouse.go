// Package clickhouse ist der (künftige) Drop-in-Store gegen ClickHouse über das
// HTTP-Interface (:8123, FORMAT JSON). In dieser Scheibe nur Konstruktor + Ping;
// die Explore-Methoden mappen später parametrisierte SQL gegen otel_traces.
//
// Mapping-Notiz (aus der Design-Recherche, otel-collector-contrib clickhouse
// exporter, otel_traces): Duration ist NANOSEKUNDEN (/1e6 = ms); RED-Entry-Spans
// = SpanKind IN ('Server','Consumer'); Root-Spans = ParentSpanId=”; der
// Span-Baum (depth/parent) wird in Go aus ParentSpanId aufgebaut.
package clickhouse

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
)

// Config beschreibt die Verbindung zum ClickHouse-HTTP-Interface.
type Config struct {
	URL      string // z.B. http://clickhouse:8123
	Database string // z.B. otel
	User     string
	Password string
	Timeout  time.Duration
}

// Store implementiert store.Store gegen ClickHouse (in dieser Scheibe Skelett).
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

// endpoint baut die Query-URL inkl. database-Parameter (für spätere Nutzung).
func (s *Store) endpoint() string {
	u, _ := url.Parse(strings.TrimRight(s.cfg.URL, "/") + "/")
	q := u.Query()
	if s.cfg.Database != "" {
		q.Set("database", s.cfg.Database)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Die Explore-Methoden folgen als eigener Meilenstein (SQL gegen otel_traces).
func (s *Store) Services(context.Context, store.ServicesQuery) (model.ServicesResult, error) {
	return model.ServicesResult{}, store.ErrUnsupported
}

func (s *Store) Traces(context.Context, store.TracesQuery) (model.TraceList, error) {
	return model.TraceList{}, store.ErrUnsupported
}

func (s *Store) Trace(context.Context, string) (model.TraceDetail, error) {
	return model.TraceDetail{}, store.ErrUnsupported
}
