// Package telemetry verbindet die Control-Plane mit dem ClickHouse-Telemetrie-
// Store. Logs gehen als JSONEachRow über das ClickHouse-HTTP-Interface (:8123) —
// bewusst ohne schweren Treiber (Muster der bewährten alten Ingest-Pipeline).
// Multi-Tenancy: ClusterId wird SERVERSEITIG aus dem Agent-Token gestempelt und
// ist führendes Element des Sort-Keys; jede Query erzwingt ClusterId im WHERE.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// LogRecord ist der Sync-Contract Agent → Control-Plane (camelCase JSON).
type LogRecord struct {
	TsUnixNano     int64  `json:"ts"`
	Namespace      string `json:"namespace"`
	PodName        string `json:"pod"`
	ContainerName  string `json:"container"`
	WorkloadKind   string `json:"workloadKind"`
	WorkloadName   string `json:"workloadName"`
	Stream         string `json:"stream"` // stdout|stderr
	Body           string `json:"body"`
	SeverityText   string `json:"severityText"`
	SeverityNumber uint8  `json:"severityNumber"`
}

// chLogRow ist die ClickHouse-Zeile (Spaltennamen = Schema).
type chLogRow struct {
	Timestamp      string            `json:"Timestamp"`
	ClusterId      string            `json:"ClusterId"`
	SeverityText   string            `json:"SeverityText"`
	SeverityNumber uint8             `json:"SeverityNumber"`
	ServiceName    string            `json:"ServiceName"`
	Namespace      string            `json:"Namespace"`
	WorkloadKind   string            `json:"WorkloadKind"`
	WorkloadName   string            `json:"WorkloadName"`
	PodName        string            `json:"PodName"`
	ContainerName  string            `json:"ContainerName"`
	Stream         string            `json:"Stream"`
	Body           string            `json:"Body"`
	LogAttributes  map[string]string `json:"LogAttributes"`
}

// Store spricht ClickHouse über HTTP.
type Store struct {
	base   string // z.B. http://localhost:8123
	user   string
	pass   string
	db     string
	client *http.Client
}

func NewStore(baseURL, user, pass, db string) *Store {
	return &Store{
		base:   strings.TrimRight(baseURL, "/"),
		user:   user,
		pass:   pass,
		db:     db,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// Enabled meldet, ob ein ClickHouse konfiguriert ist.
func (s *Store) Enabled() bool { return s != nil && s.base != "" }

func nanoToCH(ns int64) string {
	return time.Unix(0, ns).UTC().Format("2006-01-02 15:04:05.000000000")
}

// EnsureLogsSchema legt otel_logs idempotent mit dem CP-EIGENEN Schema an
// (ClusterId + Workload-Spalten für Tenancy/Filter). Wichtig: Die CP BESITZT
// diese Tabelle — der OTel-Collector darf otel_logs NICHT anlegen (kein
// logs-Pipeline im Collector), sonst gewinnt sein Standard-OTel-Schema den Race
// und dieser CREATE IF NOT EXISTS ist ein No-Op gegen eine Tabelle ohne ClusterId
// → Query bricht. Container-Logs kommen vom Agent (role=logs) über /api/agent/logs,
// nicht über OTLP.
func (s *Store) EnsureLogsSchema(ctx context.Context) error {
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.otel_logs (
			Timestamp      DateTime64(9) CODEC(Delta, ZSTD(1)),
			ClusterId      String        CODEC(ZSTD(1)),
			Namespace      LowCardinality(String),
			WorkloadKind   LowCardinality(String),
			WorkloadName   String        CODEC(ZSTD(1)),
			ServiceName    String        CODEC(ZSTD(1)),
			PodName        String        CODEC(ZSTD(1)),
			ContainerName  LowCardinality(String),
			Stream         LowCardinality(String),
			SeverityText   LowCardinality(String),
			SeverityNumber UInt8,
			Body           String        CODEC(ZSTD(1)),
			LogAttributes  Map(LowCardinality(String), String)
		) ENGINE = MergeTree
		ORDER BY (ClusterId, Namespace, WorkloadName, Timestamp)
		TTL toDateTime(Timestamp) + INTERVAL 3 DAY`, s.db)
	return s.exec(ctx, url.Values{"query": {ddl}}, nil)
}

// EnsureLogsIndexes legt Skip-Indizes auf Body idempotent an: tokenbf_v1
// beschleunigt Wort-/Token-Suchen, ngrambf_v1 Substring- und Regex-Literale.
// Kein MATERIALIZE nötig — die TTL ist 3 Tage, Alt-Parts ohne Index wachsen
// von selbst heraus.
func (s *Store) EnsureLogsIndexes(ctx context.Context) error {
	stmts := []string{
		fmt.Sprintf("ALTER TABLE %s.otel_logs ADD INDEX IF NOT EXISTS idx_body_token Body TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 4", s.db),
		fmt.Sprintf("ALTER TABLE %s.otel_logs ADD INDEX IF NOT EXISTS idx_body_ngram Body TYPE ngrambf_v1(4, 65536, 3, 7) GRANULARITY 4", s.db),
	}
	for _, ddl := range stmts {
		if err := s.exec(ctx, url.Values{"query": {ddl}}, nil); err != nil {
			return err
		}
	}
	return nil
}

// InsertLogs schreibt einen Batch als JSONEachRow. ClusterId kommt aus der
// Agent-Auth — nie vom Client.
func (s *Store) InsertLogs(ctx context.Context, clusterID uuid.UUID, recs []LogRecord) error {
	if len(recs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range recs {
		row := chLogRow{
			Timestamp:      nanoToCH(r.TsUnixNano),
			ClusterId:      clusterID.String(),
			SeverityText:   r.SeverityText,
			SeverityNumber: r.SeverityNumber,
			ServiceName:    r.WorkloadName,
			Namespace:      r.Namespace,
			WorkloadKind:   r.WorkloadKind,
			WorkloadName:   r.WorkloadName,
			PodName:        r.PodName,
			ContainerName:  r.ContainerName,
			Stream:         r.Stream,
			Body:           r.Body,
			LogAttributes:  map[string]string{},
		}
		if err := enc.Encode(row); err != nil {
			return fmt.Errorf("encode log row: %w", err)
		}
	}
	q := url.Values{"query": {fmt.Sprintf("INSERT INTO %s.otel_logs FORMAT JSONEachRow", s.db)}}
	return s.exec(ctx, q, &buf)
}

/* ── Query ────────────────────────────────────────────────────────────────── */

// LogsQuery sind die (org-gescopten) Filter des Logs-Explorers.
type LogsQuery struct {
	ClusterID   uuid.UUID
	Namespace   string   // "" = alle
	Workload    string   // "" = alle (WorkloadName)
	Workloads   []string // IN-Filter (Trace-Korrelation über mehrere Services)
	Pod         string   // "" = alle (PodName — für Kontext-Ansichten)
	MinSeverity uint8    // 0 = alle
	Search      string   // Substring, case-insensitiv
	Regexes     []string // RE2-Pattern (ClickHouse match(); case-sensitiv, (?i) für insensitiv)
	RegexMode   string   // "any" (OR, default) | "all" (AND)
	Exclude     []string // NOT-Substrings, case-insensitiv
	Fuzzy       string   // ngram-Ähnlichkeitssuche (tippfehlertolerant)
	Since       time.Time
	Until       time.Time
	Limit       int
}

const (
	maxSearchPatterns = 5
	maxPatternLen     = 512
	// fuzzyThreshold: ngramSearchCaseInsensitive liefert 0..1; 0.4 toleriert
	// Tippfehler/Varianten, ohne beliebige Zeilen zu matchen.
	fuzzyThreshold = 0.4
)

// Validate prüft die erweiterten Suchparameter, bevor die Query ClickHouse
// erreicht: Go-RE2 ist kompatibel zu ClickHouse-re2, ein Compile-Fehler hier
// wäre dort ein Query-Fehler — so wird daraus ein sauberes 400.
func (q LogsQuery) Validate() error {
	if len(q.Regexes) > maxSearchPatterns {
		return fmt.Errorf("at most %d regex patterns", maxSearchPatterns)
	}
	if len(q.Exclude) > maxSearchPatterns {
		return fmt.Errorf("at most %d exclude terms", maxSearchPatterns)
	}
	if q.RegexMode != "" && q.RegexMode != "any" && q.RegexMode != "all" {
		return errors.New(`regexMode must be "any" or "all"`)
	}
	for _, rx := range q.Regexes {
		if rx == "" || len(rx) > maxPatternLen {
			return fmt.Errorf("regex pattern must be 1..%d chars", maxPatternLen)
		}
		if _, err := regexp.Compile(rx); err != nil {
			return fmt.Errorf("invalid regex %q: %v", rx, err)
		}
	}
	for _, ex := range q.Exclude {
		if ex == "" || len(ex) > maxPatternLen {
			return fmt.Errorf("exclude term must be 1..%d chars", maxPatternLen)
		}
	}
	if len(q.Fuzzy) > maxPatternLen {
		return fmt.Errorf("fuzzy term must be at most %d chars", maxPatternLen)
	}
	return nil
}

// searchConds baut die erweiterten Body-Suchbedingungen (Regex/Exclude/Fuzzy)
// als SQL-Fragment (beginnt mit " AND …" oder ist leer) und setzt die
// zugehörigen ClickHouse-Parameter — parametrisiert, Injection-sicher.
func (q LogsQuery) searchConds(params url.Values) string {
	var b strings.Builder
	if len(q.Regexes) > 0 {
		op := " OR "
		if q.RegexMode == "all" {
			op = " AND "
		}
		parts := make([]string, len(q.Regexes))
		for i, rx := range q.Regexes {
			parts[i] = fmt.Sprintf("match(Body, {rx%d:String})", i)
			params.Set(fmt.Sprintf("param_rx%d", i), rx)
		}
		b.WriteString(" AND (" + strings.Join(parts, op) + ")")
	}
	for i, ex := range q.Exclude {
		fmt.Fprintf(&b, " AND positionCaseInsensitive(Body, {ex%d:String}) = 0", i)
		params.Set(fmt.Sprintf("param_ex%d", i), ex)
	}
	if q.Fuzzy != "" {
		fmt.Fprintf(&b, " AND ngramSearchCaseInsensitive(Body, {fz:String}) > %g", fuzzyThreshold)
		params.Set("param_fz", q.Fuzzy)
	}
	return b.String()
}

// LogLine ist die API-Antwortzeile (camelCase).
type LogLine struct {
	Ts             string `json:"ts"` // RFC3339Nano
	Namespace      string `json:"namespace"`
	WorkloadName   string `json:"workloadName"`
	PodName        string `json:"podName"`
	ContainerName  string `json:"containerName"`
	Stream         string `json:"stream"`
	SeverityText   string `json:"severityText"`
	SeverityNumber uint8  `json:"severityNumber"`
	Body           string `json:"body"`
}

// Histogram-Bucket für das Volumen-Chart.
type LogBucket struct {
	Ts     string `json:"ts"`
	Count  uint64 `json:"count"`
	Errors uint64 `json:"errors"`
	Warns  uint64 `json:"warns"`
}

// QueryLogs liest Zeilen absteigend nach Zeit. Parametrisiert (Injection-sicher).
func (s *Store) QueryLogs(ctx context.Context, q LogsQuery) ([]LogLine, error) {
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 200
	}
	inClause := ""
	if len(q.Workloads) > 0 {
		parts := make([]string, len(q.Workloads))
		for i := range q.Workloads {
			parts[i] = fmt.Sprintf("{win%d:String}", i)
		}
		inClause = "AND WorkloadName IN (" + strings.Join(parts, ",") + ")"
	}
	params := url.Values{}
	sql := fmt.Sprintf(`
		SELECT toString(Timestamp) AS ts, Namespace, WorkloadName, PodName, ContainerName,
		       Stream, SeverityText, SeverityNumber, Body
		FROM %s.otel_logs
		WHERE ClusterId = {cid:String}
		  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		  %s %s %s %s %s %s %s
		ORDER BY Timestamp DESC
		LIMIT %d FORMAT JSONEachRow`,
		s.db,
		cond(q.Namespace != "", "AND Namespace = {ns:String}"),
		cond(q.Workload != "", "AND WorkloadName = {wl:String}"),
		inClause,
		cond(q.Pod != "", "AND PodName = {pod:String}"),
		cond(q.MinSeverity > 0, "AND SeverityNumber >= {msev:UInt8}"),
		cond(q.Search != "", "AND positionCaseInsensitive(Body, {search:String}) > 0"),
		q.searchConds(params),
		q.Limit)

	params.Set("query", sql)
	params.Set("param_cid", q.ClusterID.String())
	params.Set("param_since", nanoToCH(q.Since.UnixNano()))
	params.Set("param_until", nanoToCH(q.Until.UnixNano()))
	if q.Namespace != "" {
		params.Set("param_ns", q.Namespace)
	}
	if q.Workload != "" {
		params.Set("param_wl", q.Workload)
	}
	for i, w := range q.Workloads {
		params.Set(fmt.Sprintf("param_win%d", i), w)
	}
	if q.Pod != "" {
		params.Set("param_pod", q.Pod)
	}
	if q.MinSeverity > 0 {
		params.Set("param_msev", fmt.Sprint(q.MinSeverity))
	}
	if q.Search != "" {
		params.Set("param_search", q.Search)
	}

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []LogLine{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			Ts             string `json:"ts"`
			Namespace      string `json:"Namespace"`
			WorkloadName   string `json:"WorkloadName"`
			PodName        string `json:"PodName"`
			ContainerName  string `json:"ContainerName"`
			Stream         string `json:"Stream"`
			SeverityText   string `json:"SeverityText"`
			SeverityNumber uint8  `json:"SeverityNumber"`
			Body           string `json:"Body"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode log line: %w", err)
		}
		out = append(out, LogLine{
			Ts: raw.Ts, Namespace: raw.Namespace, WorkloadName: raw.WorkloadName,
			PodName: raw.PodName, ContainerName: raw.ContainerName, Stream: raw.Stream,
			SeverityText: raw.SeverityText, SeverityNumber: raw.SeverityNumber, Body: raw.Body,
		})
	}
	return out, nil
}

// QueryHistogram liefert Zeit-Buckets (Volumen + Fehleranteil) fürs Chart.
func (s *Store) QueryHistogram(ctx context.Context, q LogsQuery, buckets int) ([]LogBucket, error) {
	if buckets <= 0 || buckets > 240 {
		buckets = 60
	}
	stepSec := int64(q.Until.Sub(q.Since).Seconds()) / int64(buckets)
	if stepSec < 1 {
		stepSec = 1
	}
	params := url.Values{}
	sql := fmt.Sprintf(`
		SELECT toString(toStartOfInterval(Timestamp, INTERVAL %d SECOND)) AS ts,
		       count() AS c, countIf(SeverityNumber >= 17) AS e,
		       countIf(SeverityNumber >= 13 AND SeverityNumber < 17) AS w
		FROM %s.otel_logs
		WHERE ClusterId = {cid:String}
		  AND Timestamp >= {since:DateTime64(9)} AND Timestamp < {until:DateTime64(9)}
		  %s %s %s %s %s
		GROUP BY ts ORDER BY ts FORMAT JSONEachRow`,
		stepSec, s.db,
		cond(q.Namespace != "", "AND Namespace = {ns:String}"),
		cond(q.Workload != "", "AND WorkloadName = {wl:String}"),
		cond(q.MinSeverity > 0, "AND SeverityNumber >= {msev:UInt8}"),
		cond(q.Search != "", "AND positionCaseInsensitive(Body, {search:String}) > 0"),
		q.searchConds(params))

	params.Set("query", sql)
	params.Set("param_cid", q.ClusterID.String())
	params.Set("param_since", nanoToCH(q.Since.UnixNano()))
	params.Set("param_until", nanoToCH(q.Until.UnixNano()))
	if q.Namespace != "" {
		params.Set("param_ns", q.Namespace)
	}
	if q.Workload != "" {
		params.Set("param_wl", q.Workload)
	}
	if q.MinSeverity > 0 {
		params.Set("param_msev", fmt.Sprint(q.MinSeverity))
	}
	if q.Search != "" {
		params.Set("param_search", q.Search)
	}

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []LogBucket{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			Ts string `json:"ts"`
			C  uint64 `json:"c"`
			E  uint64 `json:"e"`
			W  uint64 `json:"w"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode bucket: %w", err)
		}
		out = append(out, LogBucket{Ts: raw.Ts, Count: raw.C, Errors: raw.E, Warns: raw.W})
	}
	return out, nil
}

func cond(when bool, clause string) string {
	if when {
		return clause
	}
	return ""
}

/* ── HTTP-Plumbing ────────────────────────────────────────────────────────── */

func (s *Store) exec(ctx context.Context, params url.Values, body io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/?"+params.Encode(), body)
	if err != nil {
		return err
	}
	s.auth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("clickhouse %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (s *Store) get(ctx context.Context, params url.Values) ([]byte, error) {
	// UInt64 unquoted ausgeben — sonst dekodiert count() als String statt Zahl.
	params.Set("output_format_json_quote_64bit_integers", "0")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	s.auth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		msg := strings.TrimSpace(string(body[:min(len(body), 4096)]))
		if tbl, missing := missingTable(msg); missing {
			return nil, fmt.Errorf("%w: %s — this table is written by Beyla + the OTel collector "+
				"(deploy/telemetry/); without them this cluster has no traces or RED metrics", ErrTelemetryUnavailable, tbl)
		}
		return nil, fmt.Errorf("clickhouse %d: %s", resp.StatusCode, msg)
	}
	return body, nil
}

// ErrTelemetryUnavailable marks "the telemetry pipeline was never installed"
// as distinct from "the query failed". The eBPF pipeline is optional, so the
// tables it owns are routinely absent — and an agent handed a raw
// "clickhouse 404: Code: 60 …" will guess at the cause instead of reporting it.
var ErrTelemetryUnavailable = errors.New("telemetry pipeline not installed")

// missingTable recognises ClickHouse's UNKNOWN_TABLE (code 60) and returns the
// identifier it names.
func missingTable(msg string) (string, bool) {
	if !strings.Contains(msg, "Code: 60") && !strings.Contains(msg, "UNKNOWN_TABLE") {
		return "", false
	}
	if i := strings.Index(msg, "identifier '"); i >= 0 {
		rest := msg[i+len("identifier '"):]
		if j := strings.Index(rest, "'"); j > 0 {
			return rest[:j], true
		}
	}
	return "unknown table", true
}

func (s *Store) auth(req *http.Request) {
	if s.user != "" {
		req.Header.Set("X-ClickHouse-User", s.user)
		req.Header.Set("X-ClickHouse-Key", s.pass)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
