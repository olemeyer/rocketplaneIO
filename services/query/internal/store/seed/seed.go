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
	"strings"
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
	logs      []model.LogRecord            // nach Timestamp absteigend
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

func (s *Store) Service(_ context.Context, q store.ServiceQuery) (model.ServiceDetail, error) {
	var spec *svcSpec
	for i := range serviceSpecs {
		if serviceSpecs[i].name == q.Name {
			spec = &serviceSpecs[i]
			break
		}
	}
	if spec == nil {
		return model.ServiceDetail{}, store.ErrNotFound
	}

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
		step = end.Sub(start) / 24
	}
	if step <= 0 {
		step = time.Minute
	}
	windowSec := end.Sub(start).Seconds()
	if windowSec <= 0 {
		windowSec = 1
	}
	spanCount := int64(spec.rate * windowSec)
	errorCount := int64(float64(spanCount) * spec.errorRatio)

	// Zeitreihen: deterministisch aus Fingerprint + Bucket-Index moduliert.
	buckets := int(end.Sub(start) / step)
	if buckets < 1 {
		buckets = 1
	}
	if buckets > 200 {
		buckets = 200
	}
	p95, rate, errs := make([]model.Point, 0, buckets), make([]model.Point, 0, buckets), make([]model.Point, 0, buckets)
	for i := 0; i < buckets; i++ {
		t := start.Add(time.Duration(i) * step).Unix()
		h := hash64(spec.name + strconv.Itoa(i))
		wobble := 0.85 + float64(h%30)/100.0 // 0.85..1.15
		p95 = append(p95, model.Point{T: t, V: round1(spec.p95 * wobble)})
		rate = append(rate, model.Point{T: t, V: round1(spec.rate * wobble)})
		errs = append(errs, model.Point{T: t, V: round4(spec.errorRatio * wobble)})
	}

	// Operationen + Dependencies aus den Seed-Traces dieses Service.
	ops, deps := s.opsAndDeps(spec.name)

	return model.ServiceDetail{
		Name:         spec.name,
		Status:       model.DeriveHealth(spec.errorRatio, spec.p95, spec.sloP95),
		Rate:         spec.rate,
		ErrorRatio:   spec.errorRatio,
		LatencyMs:    model.Latency{P50: spec.p50, P95: spec.p95, P99: spec.p99},
		SpanCount:    spanCount,
		ErrorCount:   errorCount,
		Window:       model.Window{Start: start.Unix(), End: end.Unix(), Step: int64(step.Seconds())},
		P95Series:    p95,
		RateSeries:   rate,
		ErrorSeries:  errs,
		Operations:   ops,
		Dependencies: deps,
	}, nil
}

// seedMetrics ist der feste Metrik-Katalog des Seed-Stores.
var seedMetrics = []struct {
	name, typ, unit string
	base, amp       float64 // Basiswert + Amplitude je Service
}{
	{"system.cpu.utilization", "gauge", "1", 0.35, 0.25},
	{"system.memory.usage", "gauge", "By", 512, 180},
	{"http.server.active_requests", "gauge", "{requests}", 24, 18},
	{"http.server.request.count", "sum", "{requests}", 900, 400},
}

func (s *Store) Metrics(_ context.Context, _, _ time.Time) (model.MetricList, error) {
	out := make([]model.MetricMeta, 0, len(seedMetrics))
	for _, m := range seedMetrics {
		out = append(out, model.MetricMeta{Name: m.name, Type: m.typ, Unit: m.unit})
	}
	return model.MetricList{Metrics: out}, nil
}

func (s *Store) Metric(_ context.Context, q store.MetricQuery) (model.MetricData, error) {
	var spec *struct {
		name, typ, unit string
		base, amp       float64
	}
	for i := range seedMetrics {
		if seedMetrics[i].name == q.Name {
			spec = &seedMetrics[i]
			break
		}
	}
	if spec == nil {
		return model.MetricData{}, store.ErrNotFound
	}

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
		step = end.Sub(start) / 24
	}
	if step <= 0 {
		step = time.Minute
	}
	buckets := int(end.Sub(start) / step)
	if buckets < 1 {
		buckets = 1
	}
	if buckets > 200 {
		buckets = 200
	}

	series := make([]model.MetricSeries, 0, len(serviceSpecs))
	for _, sp := range serviceSpecs {
		pts := make([]model.Point, 0, buckets)
		for i := 0; i < buckets; i++ {
			t := start.Add(time.Duration(i) * step).Unix()
			h := hash64(spec.name + sp.name + strconv.Itoa(i))
			frac := float64(h%1000) / 1000.0 // 0..1
			v := spec.base + spec.amp*(frac-0.5)*2
			if v < 0 {
				v = 0
			}
			pts = append(pts, model.Point{T: t, V: round4(v)})
		}
		series = append(series, model.MetricSeries{Label: sp.name, Points: pts})
	}
	// Ohne GroupByService: zu einer Reihe aggregieren (Mittel bzw. Summe).
	if !q.GroupByService {
		agg := make([]model.Point, buckets)
		for _, s := range series {
			for i, p := range s.Points {
				agg[i].T = p.T
				agg[i].V += p.V
			}
		}
		if spec.typ == "gauge" {
			for i := range agg {
				agg[i].V = round4(agg[i].V / float64(len(series)))
			}
		}
		series = []model.MetricSeries{{Label: "", Points: agg}}
	}

	return model.MetricData{
		Name: spec.name, Type: spec.typ, Unit: spec.unit,
		Window: model.Window{Start: start.Unix(), End: end.Unix(), Step: int64(step.Seconds())},
		Series: series,
	}, nil
}

func (s *Store) ServiceMap(_ context.Context, start, end time.Time) (model.ServiceMap, error) {
	if end.IsZero() {
		end = s.now()
	}
	if start.IsZero() {
		start = end.Add(-15 * time.Minute)
	}

	// Knoten: alle Services aus den Trace-Spans, mit Span-/Fehler-Aggregat.
	type nAcc struct {
		spanCount, errCount int64
		durSum              float64
	}
	nodeMap := map[string]*nAcc{}
	type eKey struct{ from, to string }
	type eAcc struct {
		calls, errs int64
	}
	edgeMap := map[eKey]*eAcc{}

	for _, d := range s.details {
		byID := map[string]model.Span{}
		for _, sp := range d.Spans {
			byID[sp.SpanID] = sp
			a := nodeMap[sp.Service]
			if a == nil {
				a = &nAcc{}
				nodeMap[sp.Service] = a
			}
			a.spanCount++
			a.durSum += sp.DurationMs
			if sp.Status == model.TraceError {
				a.errCount++
			}
		}
		for _, sp := range d.Spans {
			if sp.ParentSpanID == "" {
				continue
			}
			parent, ok := byID[sp.ParentSpanID]
			if !ok || parent.Service == sp.Service {
				continue
			}
			k := eKey{parent.Service, sp.Service}
			e := edgeMap[k]
			if e == nil {
				e = &eAcc{}
				edgeMap[k] = e
			}
			e.calls++
			if sp.Status == model.TraceError {
				e.errs++
			}
		}
	}

	// SLO-Defaults je Service für die Health-Ableitung.
	slo := func(name string) float64 {
		if v, ok := map[string]float64{"checkout-api": 400, "payment-gateway": 300, "cart-service": 150, "inventory": 100}[name]; ok {
			return v
		}
		return 300
	}

	nodes := make([]model.MapNode, 0, len(nodeMap))
	for name, a := range nodeMap {
		errRatio := ratio(a.errCount, a.spanCount)
		p95 := round1(avg(a.durSum, a.spanCount) * 1.6)
		nodes = append(nodes, model.MapNode{
			Name: name, Status: model.DeriveHealth(errRatio, p95, slo(name)),
			Rate: round1(float64(a.spanCount) / end.Sub(start).Seconds()), ErrorRatio: round4(errRatio),
			P95Ms: p95, SpanCount: a.spanCount,
		})
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].SpanCount > nodes[j].SpanCount })

	edges := make([]model.MapEdge, 0, len(edgeMap))
	for k, e := range edgeMap {
		edges = append(edges, model.MapEdge{
			From: k.from, To: k.to, CallCount: e.calls, ErrorRatio: round4(ratio(e.errs, e.calls)),
		})
	}
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].CallCount > edges[j].CallCount })

	return model.ServiceMap{
		Window: model.Window{Start: start.Unix(), End: end.Unix()},
		Nodes:  nodes, Edges: edges,
	}, nil
}

// opsAndDeps aggregiert Operationen (SpanName des Service) und nachgelagerte
// Services (Child-Spans mit anderem Service) aus den Traces, die bei name wurzeln.
func (s *Store) opsAndDeps(name string) ([]model.OperationStat, []model.Dependency) {
	type acc struct {
		count, errCount int64
		durSum          float64
	}
	opMap := map[string]*acc{}
	depMap := map[string]*acc{}

	for _, d := range s.details {
		if len(d.Spans) == 0 || d.Spans[0].Service != name {
			continue
		}
		byID := map[string]model.Span{}
		for _, sp := range d.Spans {
			byID[sp.SpanID] = sp
		}
		for _, sp := range d.Spans {
			if sp.Service == name {
				a := opMap[sp.Name]
				if a == nil {
					a = &acc{}
					opMap[sp.Name] = a
				}
				a.count++
				a.durSum += sp.DurationMs
				if sp.Status == model.TraceError {
					a.errCount++
				}
			}
			// Dependency-Kante: Parent gehört zu name, Child zu anderem Service.
			if sp.Service != name && sp.ParentSpanID != "" {
				if parent, ok := byID[sp.ParentSpanID]; ok && parent.Service == name {
					a := depMap[sp.Service]
					if a == nil {
						a = &acc{}
						depMap[sp.Service] = a
					}
					a.count++
					a.durSum += sp.DurationMs
					if sp.Status == model.TraceError {
						a.errCount++
					}
				}
			}
		}
	}

	ops := make([]model.OperationStat, 0, len(opMap))
	for n, a := range opMap {
		ops = append(ops, model.OperationStat{
			Name: n, SpanCount: a.count, ErrorCount: a.errCount,
			ErrorRatio: ratio(a.errCount, a.count), P95Ms: round1(avg(a.durSum, a.count) * 1.4),
		})
	}
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].SpanCount > ops[j].SpanCount })

	deps := make([]model.Dependency, 0, len(depMap))
	for n, a := range depMap {
		deps = append(deps, model.Dependency{
			Service: n, CallCount: a.count, ErrorRatio: ratio(a.errCount, a.count),
			P95Ms: round1(avg(a.durSum, a.count) * 1.4),
		})
	}
	sort.SliceStable(deps, func(i, j int) bool { return deps[i].CallCount > deps[j].CallCount })
	return ops, deps
}

func ratio(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
func avg(sum float64, n int64) float64 {
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
func round4(f float64) float64 { return float64(int64(f*10000+0.5)) / 10000 }

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

// --- Logs -------------------------------------------------------------------

func (s *Store) Logs(_ context.Context, q store.LogsQuery) (model.LogList, error) {
	filtered := make([]model.LogRecord, 0, len(s.logs))
	search := strings.ToLower(q.Search)
	for _, l := range s.logs {
		if q.Service != "" && l.ServiceName != q.Service {
			continue
		}
		if q.TraceID != "" && l.TraceID != q.TraceID {
			continue
		}
		if q.MinSeverity > 0 && l.SeverityNumber < q.MinSeverity {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(l.Body), search) {
			continue
		}
		filtered = append(filtered, l)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
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
	return model.LogList{Logs: filtered[offset:end], NextCursor: next}, nil
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

	s.buildLogs()
}

// logMessages sind deterministische Body-Vorlagen je Root-Operation.
var logMessages = map[string][]string{
	"POST /checkout": {"checkout requested", "cart validated", "reserving inventory"},
	"POST /charge":   {"charge initiated", "contacting payment provider"},
	"GET /cart":      {"cart loaded", "cache hit"},
	"POST /reserve":  {"reservation created"},
}

// buildLogs erzeugt Logs, die mit den Traces korrelieren (gleiche TraceId): pro
// Trace ein INFO-Startlog + bei Error-Traces ein ERROR-Log; zusätzlich einige
// standalone-Logs ohne Trace.
func (s *Store) buildLogs() {
	for _, sum := range s.summaries {
		tmpls := logMessages[sum.RootName]
		if len(tmpls) == 0 {
			tmpls = []string{"request handled"}
		}
		h := hash64(sum.TraceID)
		body := tmpls[h%uint64(len(tmpls))]
		s.logs = append(s.logs, model.LogRecord{
			Timestamp:      sum.StartTimeUnixMs,
			ServiceName:    sum.RootService,
			Severity:       "INFO",
			SeverityNumber: 9,
			Body:           body + " trace=" + sum.TraceID[:8],
			TraceID:        sum.TraceID,
			SpanID:         hexN(sum.TraceID+"log", 16),
			Attributes:     map[string]string{"http.method": strings.Fields(sum.RootName)[0]},
		})
		if sum.ErrorCount > 0 {
			s.logs = append(s.logs, model.LogRecord{
				Timestamp:      sum.StartTimeUnixMs + int64(sum.DurationMs),
				ServiceName:    sum.RootService,
				Severity:       "ERROR",
				SeverityNumber: 17,
				Body:           sum.RootName + " failed: downstream error",
				TraceID:        sum.TraceID,
				SpanID:         hexN(sum.TraceID+"errlog", 16),
				Attributes:     map[string]string{"exception.type": "DownstreamError"},
			})
		}
	}
	// ein paar Infrastruktur-Logs ohne Trace-Bezug
	base := s.now()
	infra := []struct {
		svc, sev, body string
		num            int32
	}{
		{"cart-service", "WARN", "connection pool at 80% capacity", 13},
		{"payment-gateway", "INFO", "config reloaded", 9},
		{"inventory", "DEBUG", "stock cache warmed", 5},
	}
	for i, l := range infra {
		s.logs = append(s.logs, model.LogRecord{
			Timestamp:      base.UnixMilli() - int64(i)*45_000,
			ServiceName:    l.svc,
			Severity:       l.sev,
			SeverityNumber: l.num,
			Body:           l.body,
		})
	}
	sort.SliceStable(s.logs, func(i, j int) bool { return s.logs[i].Timestamp > s.logs[j].Timestamp })
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
