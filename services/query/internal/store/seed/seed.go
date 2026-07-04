// Package seed ist die deterministische In-Memory-Implementierung von
// store.Store. Werte hängen rein am Fingerprint (fnv) — dieselbe Query liefert
// byte-identische Ergebnisse. Die Optik ist 1:1 auf den ursprünglichen App-Shell-
// Mock kalibriert (checkout-api, payment-gateway, cart-service, inventory).
package seed

import (
	"context"
	"encoding/base64"
	"hash/fnv"
	"sort"
	"strconv"
	"time"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
)

// tracesPerService bestimmt die Größe des generierten Trace-Katalogs.
const tracesPerService = 30

// refTraceID ist der feste Referenz-Trace von checkout-api (7-Span-Waterfall).
const refTraceID = "7f3a9c2b1e8d4a5f9c0b3d2e1a4f6c7d"

type svcSpec struct {
	name       string
	rootOp     string
	rate       float64 // req/s
	errorRatio float64 // 0..1
	p50        float64
	p95        float64
	p99        float64
	sloP95     float64
	spark      []float64
}

// serviceSpecs kalibriert auf den bisherigen Mock.
var serviceSpecs = []svcSpec{
	{"checkout-api", "POST /checkout", 1204, 0.042, 210, 842, 1310, 400, []float64{5, 7, 6, 9, 8, 12, 16, 14}},
	{"payment-gateway", "POST /charge", 640, 0.008, 120, 310, 520, 300, []float64{8, 7, 9, 8, 10, 9, 11, 10}},
	{"cart-service", "GET /cart", 2103, 0.001, 38, 96, 180, 150, []float64{6, 6, 5, 7, 6, 6, 7, 6}},
	{"inventory", "POST /reserve", 880, 0.0002, 22, 54, 96, 100, []float64{4, 5, 4, 4, 5, 4, 5, 4}},
}

// Store ist der deterministische Seed-Store.
type Store struct {
	now       func() time.Time
	summaries []model.TraceSummary         // nach StartTime absteigend
	details   map[string]model.TraceDetail // traceID -> Detail
}

// Option konfiguriert den Seed-Store.
type Option func(*Store)

// WithNow injiziert eine feste Uhr (für zeitstabile Tests).
func WithNow(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// New baut den deterministischen Katalog auf.
func New(opts ...Option) *Store {
	s := &Store{now: time.Now, details: map[string]model.TraceDetail{}}
	for _, o := range opts {
		o(s)
	}
	s.build()
	return s
}

var _ store.Store = (*Store)(nil)

func (s *Store) Ping(context.Context) error { return nil }
func (s *Store) Close() error               { return nil }

// --- Services (RED) ---------------------------------------------------------

func (s *Store) Services(_ context.Context, q store.ServicesQuery) (model.ServicesResult, error) {
	end := q.End
	if end.IsZero() {
		end = s.now()
	}
	start := q.Start
	if start.IsZero() {
		start = end.Add(-15 * time.Minute)
	}
	step := q.Step
	if step <= 0 {
		step = end.Sub(start) / 8
	}
	windowSec := end.Sub(start).Seconds()
	if windowSec <= 0 {
		windowSec = 1
	}

	services := make([]model.Service, 0, len(serviceSpecs))
	for _, sp := range serviceSpecs {
		spanCount := int64(sp.rate * windowSec)
		errorCount := int64(float64(spanCount) * sp.errorRatio)
		services = append(services, model.Service{
			Name:       sp.name,
			Status:     model.DeriveHealth(sp.errorRatio, sp.p95, sp.sloP95),
			Rate:       sp.rate,
			ErrorRatio: sp.errorRatio,
			LatencyMs:  model.Latency{P50: sp.p50, P95: sp.p95, P99: sp.p99},
			SpanCount:  spanCount,
			ErrorCount: errorCount,
			Sparkline:  model.Sparkline{Metric: "rate", Values: sp.spark},
		})
	}
	sortServices(services, q.Sort)

	return model.ServicesResult{
		Window: model.Window{
			Start: start.Unix(),
			End:   end.Unix(),
			Step:  int64(step.Seconds()),
		},
		Services: services,
	}, nil
}

func sortServices(s []model.Service, by string) {
	sort.SliceStable(s, func(i, j int) bool {
		switch by {
		case "rate":
			return s[i].Rate > s[j].Rate
		case "p95":
			return s[i].LatencyMs.P95 > s[j].LatencyMs.P95
		default: // "error"
			return s[i].ErrorRatio > s[j].ErrorRatio
		}
	})
}

// --- Traces -----------------------------------------------------------------

func (s *Store) Traces(_ context.Context, q store.TracesQuery) (model.TraceList, error) {
	filtered := make([]model.TraceSummary, 0, len(s.summaries))
	for _, t := range s.summaries {
		if q.Service != "" && t.RootService != q.Service {
			continue
		}
		if q.Status != "" && string(t.Status) != q.Status {
			continue
		}
		if q.MinDurationMs > 0 && t.DurationMs < q.MinDurationMs {
			continue
		}
		if q.MaxDurationMs > 0 && t.DurationMs > q.MaxDurationMs {
			continue
		}
		filtered = append(filtered, t)
	}

	if q.Sort == "duration" {
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].DurationMs > filtered[j].DurationMs })
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := decodeCursor(q.Cursor)
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	next := ""
	if end < len(filtered) {
		next = encodeCursor(end)
	} else {
		end = len(filtered)
	}

	return model.TraceList{Traces: filtered[offset:end], NextCursor: next}, nil
}

func (s *Store) Trace(_ context.Context, traceID string) (model.TraceDetail, error) {
	d, ok := s.details[traceID]
	if !ok {
		return model.TraceDetail{}, store.ErrNotFound
	}
	return d, nil
}

// --- Katalog-Aufbau ---------------------------------------------------------

func (s *Store) build() {
	base := s.now()
	for _, sp := range serviceSpecs {
		for i := 0; i < tracesPerService; i++ {
			var detail model.TraceDetail
			if sp.name == "checkout-api" && i == 0 {
				detail = referenceTrace(base)
			} else {
				detail = generatedTrace(sp, i, base)
			}
			s.details[detail.TraceID] = detail
			s.summaries = append(s.summaries, summaryOf(detail))
		}
	}
	sort.SliceStable(s.summaries, func(i, j int) bool {
		return s.summaries[i].StartTimeUnixMs > s.summaries[j].StartTimeUnixMs
	})
}

func summaryOf(d model.TraceDetail) model.TraceSummary {
	status := model.TraceOK
	if d.ErrorCount > 0 {
		status = model.TraceError
	}
	root := d.Spans[0]
	return model.TraceSummary{
		TraceID:         d.TraceID,
		RootName:        root.Name,
		RootService:     root.Service,
		StartTimeUnixMs: d.StartTimeUnixMs,
		DurationMs:      d.DurationMs,
		SpanCount:       d.SpanCount,
		ErrorCount:      d.ErrorCount,
		Status:          status,
	}
}

// generatedTrace synthetisiert einen deterministischen Trace (Root + Children).
func generatedTrace(sp svcSpec, i int, base time.Time) model.TraceDetail {
	h := hash64(sp.name + "#" + strconv.Itoa(i))
	traceID := hexN(sp.name+"#"+strconv.Itoa(i), 32)

	span := sp.p99 - sp.p50
	if span < 1 {
		span = 1
	}
	dur := sp.p50 + float64(h%uint64(span))
	isError := h%24 == 0
	startMs := base.UnixMilli() - int64(i)*137_000 - int64(hash64(sp.name)%97_000)

	rootID := hexN(traceID+"root", 16)
	spans := []model.Span{{
		SpanID: rootID, ParentSpanID: "", Name: sp.rootOp, Service: sp.name,
		Kind: model.SpanServer, StartOffsetMs: 0, DurationMs: dur, Depth: 0,
		Status: model.TraceOK,
	}}

	childOps := []string{"db.query", "cache.get", "rpc.call"}
	childSvcs := []string{sp.name, "cache", "auth"}
	nChildren := 2 + int(h%2) // 2 oder 3
	for c := 0; c < nChildren; c++ {
		ch := hash64(traceID + childOps[c])
		off := dur * (0.1 + 0.25*float64(c))
		cdur := dur * (0.15 + float64(ch%20)/100.0)
		spans = append(spans, model.Span{
			SpanID: hexN(traceID+childOps[c], 16), ParentSpanID: rootID,
			Name: childOps[c], Service: childSvcs[c], Kind: model.SpanClient,
			StartOffsetMs: round1(off), DurationMs: round1(cdur), Depth: 1,
			Status: model.TraceOK,
		})
	}

	errorCount := 0
	if isError {
		spans[0].Status = model.TraceError
		spans[0].StatusMessage = "internal error"
		last := len(spans) - 1
		spans[last].Status = model.TraceError
		errorCount = 2
	}

	return model.TraceDetail{
		TraceID:         traceID,
		StartTimeUnixMs: startMs,
		DurationMs:      round1(dur),
		SpanCount:       len(spans),
		ErrorCount:      errorCount,
		Services:        distinctServices(spans),
		Spans:           spans,
	}
}

// referenceTrace ist der feste 7-Span-Waterfall aus dem Mock.
func referenceTrace(base time.Time) model.TraceDetail {
	rootID := hexN(refTraceID+"root", 16)
	payID := hexN(refTraceID+"payment", 16)
	spans := []model.Span{
		{SpanID: rootID, ParentSpanID: "", Name: "POST /checkout", Service: "checkout-api", Kind: model.SpanServer, StartOffsetMs: 0, DurationMs: 342, Depth: 0, Status: model.TraceError, StatusMessage: "payment declined"},
		{SpanID: hexN(refTraceID+"auth", 16), ParentSpanID: rootID, Name: "auth.verify", Service: "auth", Kind: model.SpanInternal, StartOffsetMs: 10, DurationMs: 41, Depth: 1, Status: model.TraceOK},
		{SpanID: hexN(refTraceID+"cart", 16), ParentSpanID: rootID, Name: "cart.load", Service: "cart-service", Kind: model.SpanInternal, StartOffsetMs: 41, DurationMs: 55, Depth: 1, Status: model.TraceOK},
		{SpanID: payID, ParentSpanID: rootID, Name: "payment.charge", Service: "payment-gateway", Kind: model.SpanClient, StartOffsetMs: 103, DurationMs: 158, Depth: 1, Status: model.TraceError, StatusMessage: "card declined"},
		{SpanID: hexN(refTraceID+"stripe", 16), ParentSpanID: payID, Name: "stripe.charge", Service: "stripe", Kind: model.SpanClient, StartOffsetMs: 116, DurationMs: 130, Depth: 2, Status: model.TraceError, StatusMessage: "insufficient_funds"},
		{SpanID: hexN(refTraceID+"inv", 16), ParentSpanID: rootID, Name: "inventory.reserve", Service: "inventory", Kind: model.SpanClient, StartOffsetMs: 267, DurationMs: 34, Depth: 1, Status: model.TraceOK},
		{SpanID: hexN(refTraceID+"email", 16), ParentSpanID: rootID, Name: "email.enqueue", Service: "notifier", Kind: model.SpanInternal, StartOffsetMs: 308, DurationMs: 24, Depth: 1, Status: model.TraceOK},
	}
	return model.TraceDetail{
		TraceID:         refTraceID,
		StartTimeUnixMs: base.UnixMilli() - 4_000,
		DurationMs:      342,
		SpanCount:       len(spans),
		ErrorCount:      3,
		Services:        distinctServices(spans),
		Spans:           spans,
	}
}

// --- Helfer -----------------------------------------------------------------

func distinctServices(spans []model.Span) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, sp := range spans {
		if !seen[sp.Service] {
			seen[sp.Service] = true
			out = append(out, sp.Service)
		}
	}
	sort.Strings(out)
	return out
}

func hash64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// hexN erzeugt n Hex-Zeichen deterministisch aus s (n Vielfaches von 16 ideal).
func hexN(s string, n int) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, n)
	h := hash64(s)
	for i := 0; i < n; i++ {
		if i%16 == 0 && i > 0 {
			h = hash64(s + strconv.Itoa(i))
		}
		out[i] = hexdigits[h&0xf]
		h >>= 4
	}
	return string(out)
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(c string) int {
	if c == "" {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
