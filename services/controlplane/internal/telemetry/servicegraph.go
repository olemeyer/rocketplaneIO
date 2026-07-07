package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// servicegraph.go leitet die Service-Map-KANTEN aus Beyla-eBPF-Spans ab —
// der primäre, CNI-agnostische Weg (conntrack ist nur Fallback).
//
// Warum CLIENT-Spans als Anker: der Client-Span trägt die SAUBERE Identität des
// AUFRUFERS (ResourceAttributes k8s.namespace/owner/service) plus die Ziel-
// server.address. Server-Spans hingegen haben zwar sauberes ServerZiel, aber die
// client.address ist auf kube-proxy/iptables-Clustern durch SNAT + kubelet-Probes
// meist ein Node-Name/127.0.0.1 (unbrauchbar). Trace-Context-Paarung (ParentSpanId)
// greift ohne durchgängige Propagation kaum. Also: Client-Workload → server.address,
// letztere in der Control-Plane gegen die Topologie auf einen Workload aufgelöst.
//
// Tenancy: otel_traces trägt (noch) keine ClusterId — wie im Rest von traces.go
// gilt das Per-Deployment-Single-Tenant-Modell (ein Telemetrie-Store pro Cluster).

// ServiceGraphEdges aggregiert Client-Spans ab `since` zu noch nicht aufgelösten
// Kanten mit RED (Requests, Fehler, p95-Latenz). server.address wird um ein
// evtl. angehängtes ":port" bereinigt und kleingeschrieben.
func (s *Store) ServiceGraphEdges(ctx context.Context, since time.Time) ([]model.RawTraceEdge, error) {
	sql := fmt.Sprintf(`
		SELECT
		  ResourceAttributes['k8s.namespace.name'] AS clientNs,
		  coalesce(nullIf(ResourceAttributes['k8s.owner.name'], ''), ResourceAttributes['service.name']) AS clientName,
		  lower(replaceRegexpOne(SpanAttributes['server.address'], ':[0-9]+$', '')) AS serverAddr,
		  SpanAttributes['server.port'] AS serverPort,
		  multiIf(SpanAttributes['db.system.name'] != '', SpanAttributes['db.system.name'],
		          SpanAttributes['rpc.system'] != '', 'grpc', 'http') AS protocol,
		  count() AS reqs,
		  countIf(StatusCode = 'Error' OR SpanAttributes['http.response.status_code'] >= '500') AS errs,
		  quantile(0.95)(Duration) / 1e6 AS p95Ms
		FROM %s.otel_traces
		WHERE SpanKind = 'Client'
		  AND Timestamp >= {since:DateTime64(9)}
		  AND clientNs != ''
		  AND clientName != ''
		  AND serverAddr != ''
		GROUP BY clientNs, clientName, serverAddr, serverPort, protocol
		HAVING reqs > 0
		ORDER BY reqs DESC
		LIMIT 2000 FORMAT JSONEachRow`, s.db)

	params := url.Values{"query": {sql}}
	params.Set("param_since", nanoToCH(since.UnixNano()))

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []model.RawTraceEdge{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			ClientNs   string  `json:"clientNs"`
			ClientName string  `json:"clientName"`
			ServerAddr string  `json:"serverAddr"`
			ServerPort string  `json:"serverPort"`
			Protocol   string  `json:"protocol"`
			Reqs       uint64  `json:"reqs"`
			Errs       uint64  `json:"errs"`
			P95Ms      float64 `json:"p95Ms"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode edge row: %w", err)
		}
		out = append(out, model.RawTraceEdge{
			ClientNs: raw.ClientNs, ClientName: raw.ClientName,
			ServerAddr: raw.ServerAddr, ServerPort: raw.ServerPort, Protocol: raw.Protocol,
			Reqs: int64(raw.Reqs), Errs: int64(raw.Errs), P95Ms: raw.P95Ms,
		})
	}
	return out, nil
}
