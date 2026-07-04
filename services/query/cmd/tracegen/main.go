// Command tracegen speist kontinuierlich realistische OTel-Spans direkt in die
// ClickHouse-Tabelle otel_traces ein (JSONEachRow über das HTTP-Interface) — ein
// Load-Generator, der echte, wachsende Live-Telemetrie erzeugt. Die Tabelle ist
// identisch zu der, die der OTel-Collector-clickhouse-exporter anlegen wuerde.
//
// Nutzung:
//
//	go run ./cmd/tracegen                 # Dauerbetrieb, ~alle 2s ein Batch
//	go run ./cmd/tracegen -backfill 30m   # zuerst 30min Historie, dann live
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math"
	mrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// svcSpec kalibriert die Optik auf die bestehende UI (p50/p95/p99 in ms).
type svcSpec struct {
	name       string
	rootOp     string
	kind       string // Root-SpanKind
	rate       float64
	errorRatio float64
	p50, p99   float64
	children   []childSpec
}

type childSpec struct {
	op      string
	service string
	kind    string
	frac    float64 // Anteil der Root-Dauer als Offset-Basis
	dur     float64 // Anteil der Root-Dauer als Dauer
	errWith bool    // erbt Fehler vom Root
}

var specs = []svcSpec{
	{"checkout-api", "POST /checkout", "Server", 1204, 0.042, 210, 1310, []childSpec{
		{"auth.verify", "auth", "Internal", 0.03, 0.12, false},
		{"cart.load", "cart-service", "Internal", 0.12, 0.16, false},
		{"payment.charge", "payment-gateway", "Client", 0.30, 0.46, true},
		{"inventory.reserve", "inventory", "Client", 0.78, 0.10, false},
		{"email.enqueue", "notifier", "Internal", 0.90, 0.07, false},
	}},
	{"payment-gateway", "POST /charge", "Server", 640, 0.008, 120, 520, []childSpec{
		{"stripe.charge", "stripe", "Client", 0.15, 0.7, true},
		{"db.write", "payment-gateway", "Client", 0.05, 0.1, false},
	}},
	{"cart-service", "GET /cart", "Server", 2103, 0.001, 38, 180, []childSpec{
		{"cache.get", "redis", "Client", 0.1, 0.3, false},
		{"db.query", "cart-service", "Client", 0.4, 0.4, false},
	}},
	{"inventory", "POST /reserve", "Server", 880, 0.0002, 22, 96, []childSpec{
		{"db.query", "inventory", "Client", 0.2, 0.5, false},
	}},
}

type row struct {
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

func main() {
	var (
		backfill time.Duration
		every    time.Duration
		chURL    = envOr("CLICKHOUSE_URL", "http://localhost:8123")
		chDB     = envOr("CLICKHOUSE_DB", "otel")
		chUser   = envOr("CLICKHOUSE_USER", "rocketplane")
		chPass   = envOr("CLICKHOUSE_PASSWORD", "rocketplane")
	)
	flag.DurationVar(&backfill, "backfill", 15*time.Minute, "amount of history to seed before going live")
	flag.DurationVar(&every, "every", 2*time.Second, "batch interval in live mode")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	rng := mrand.New(mrand.NewSource(42))
	sink := &clickhouse{url: strings.TrimRight(chURL, "/"), db: chDB, user: chUser, pass: chPass, client: &http.Client{Timeout: 15 * time.Second}}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 1) Backfill: Historie in Sekundenschritten erzeugen (skaliert herunter,
	// damit die Tabelle nicht explodiert — ~1 Trace je Service alle 3s Historie).
	if backfill > 0 {
		now := time.Now()
		start := now.Add(-backfill)
		var batch []row
		for t := start; t.Before(now); t = t.Add(3 * time.Second) {
			for _, sp := range specs {
				if rng.Float64() < 0.5 { // nicht jeder Service in jedem Tick
					continue
				}
				batch = append(batch, spanRows(sp, t, rng)...)
			}
			if len(batch) >= 2000 {
				if err := sink.insert(ctx, batch); err != nil {
					log.Error("backfill insert", "err", err)
				}
				batch = batch[:0]
			}
		}
		if len(batch) > 0 {
			if err := sink.insert(ctx, batch); err != nil {
				log.Error("backfill insert", "err", err)
			}
		}
		log.Info("backfill done", "window", backfill.String())
	}

	// 2) Live: kontinuierlich neue Traces.
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	log.Info("live mode", "every", every.String(), "clickhouse", chURL)
	for {
		select {
		case <-ctx.Done():
			log.Info("stopping")
			return
		case <-ticker.C:
			var batch []row
			now := time.Now()
			for _, sp := range specs {
				n := 1 + rng.Intn(3)
				for i := 0; i < n; i++ {
					ts := now.Add(-time.Duration(rng.Intn(int(every.Milliseconds()))) * time.Millisecond)
					batch = append(batch, spanRows(sp, ts, rng)...)
				}
			}
			if err := sink.insert(ctx, batch); err != nil {
				log.Error("live insert", "err", err)
			} else {
				log.Info("inserted", "spans", len(batch))
			}
		}
	}
}

// spanRows erzeugt Root + Children eines Trace als otel_traces-Zeilen.
func spanRows(sp svcSpec, start time.Time, rng *mrand.Rand) []row {
	traceID := hexID(16)
	rootID := hexID(8)
	dur := sampleDurationMs(sp.p50, sp.p99, rng)
	isErr := rng.Float64() < sp.errorRatio

	res := map[string]string{"service.name": sp.name, "deployment.environment": "production"}
	rows := []row{{
		Timestamp: chTime(start), TraceId: traceID, SpanId: rootID, ParentSpanId: "",
		SpanName: sp.rootOp, SpanKind: sp.kind, ServiceName: sp.name,
		ResourceAttributes: res, ScopeName: "rocketplane/tracegen",
		SpanAttributes: map[string]string{"http.method": strings.Fields(sp.rootOp)[0]},
		Duration:       uint64(dur * 1e6), StatusCode: okErr(isErr), StatusMessage: errMsg(isErr, "request failed"),
	}}

	for _, c := range sp.children {
		off := time.Duration(dur*c.frac) * time.Millisecond
		cdur := dur * c.dur * (0.8 + 0.4*rng.Float64())
		cErr := isErr && c.errWith
		rows = append(rows, row{
			Timestamp: chTime(start.Add(off)), TraceId: traceID, SpanId: hexID(8), ParentSpanId: rootID,
			SpanName: c.op, SpanKind: c.kind, ServiceName: c.service,
			ResourceAttributes: map[string]string{"service.name": c.service, "deployment.environment": "production"},
			ScopeName:          "rocketplane/tracegen",
			SpanAttributes:     map[string]string{},
			Duration:           uint64(cdur * 1e6), StatusCode: okErr(cErr), StatusMessage: errMsg(cErr, "downstream error"),
		})
	}
	return rows
}

// sampleDurationMs zieht eine Latenz aus einer log-normalen Verteilung, kalibriert
// grob auf p50/p99.
func sampleDurationMs(p50, p99 float64, rng *mrand.Rand) float64 {
	if p50 <= 0 {
		p50 = 1
	}
	sigma := math.Log(p99/p50) / 2.326 // z(0.99) ~ 2.326
	if sigma <= 0 {
		sigma = 0.3
	}
	mu := math.Log(p50)
	return math.Exp(mu + sigma*rng.NormFloat64())
}

func okErr(isErr bool) string {
	if isErr {
		return "Error"
	}
	return "Ok"
}
func errMsg(isErr bool, msg string) string {
	if isErr {
		return msg
	}
	return ""
}

// chTime formatiert als ClickHouse-DateTime64(9)-freundlichen String.
func chTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000000000") }

func hexID(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- ClickHouse-Sink -------------------------------------------------------

type clickhouse struct {
	url, db, user, pass string
	client              *http.Client
}

func (c *clickhouse) insert(ctx context.Context, rows []row) error {
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
	q := "INSERT INTO otel_traces FORMAT JSONEachRow"
	url := fmt.Sprintf("%s/?database=%s&query=%s", c.url, c.db, urlEncode(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b := make([]byte, 512)
		n, _ := resp.Body.Read(b)
		return fmt.Errorf("clickhouse %d: %s", resp.StatusCode, string(b[:n]))
	}
	return nil
}

func urlEncode(s string) string {
	r := strings.NewReplacer(" ", "%20", "\n", "%0A")
	return r.Replace(s)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
