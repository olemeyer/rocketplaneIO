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
// Zwei Quellen, die sich ergänzen (jede deckt die Blindstelle der anderen):
//   - CLIENT-Spans: der Aufrufer ist sauber (ResourceAttributes), das Ziel ist
//     server.address. Trägt auf kube-proxy/iptables-Clustern, wo die Client-IP
//     server-seitig durch SNAT verloren geht.
//   - SERVER-Spans: der Aufgerufene ist sauber, der Aufrufer ist client.address.
//     Trägt dort, wo die Client-IP erhalten bleibt (Cilium/eBPF-Datapath, gleicher
//     Node) — und Server-Spans sind meist zahlreicher.
//
// Beide liefern model.RawTraceEdge mit einer bekannten Workload-Seite + einer noch
// aufzulösenden Peer-Adresse; die Auflösung + Richtung passiert in der Control-Plane
// gegen die Topologie. Trace-Context-Paarung (ParentSpanId) wird bewusst NICHT
// genutzt — sie propagiert auf realen Stacks kaum.
//
// Tenancy: otel_traces trägt (noch) keine ClusterId — Per-Deployment-Single-Tenant
// wie im Rest von traces.go (ein Telemetrie-Store pro Cluster).

// ServiceGraphEdges aggregiert Client- UND Server-Spans ab `since` zu rohen Kanten
// mit RED (Requests, Fehler, p95-Latenz). Adressen werden um ein evtl. angehängtes
// ":port" bereinigt und kleingeschrieben; offensichtliche Nicht-Workloads
// (leer, localhost) werden schon hier gefiltert.
func (s *Store) ServiceGraphEdges(ctx context.Context, since time.Time) ([]model.RawTraceEdge, error) {
	client, err := s.spanEdges(ctx, since, true)
	if err != nil {
		return nil, err
	}
	server, err := s.spanEdges(ctx, since, false)
	if err != nil {
		return nil, err
	}
	return append(client, server...), nil
}

// spanEdges fragt eine Span-Richtung ab. knownIsClient=true → Client-Spans
// (Peer = server.address), sonst Server-Spans (Peer = client.address).
func (s *Store) spanEdges(ctx context.Context, since time.Time, knownIsClient bool) ([]model.RawTraceEdge, error) {
	kind, peerAttr := "Server", "client.address"
	if knownIsClient {
		kind, peerAttr = "Client", "server.address"
	}
	sql := fmt.Sprintf(`
		SELECT
		  ResourceAttributes['k8s.namespace.name'] AS knownNs,
		  coalesce(nullIf(ResourceAttributes['k8s.owner.name'], ''), ResourceAttributes['service.name']) AS knownName,
		  lower(replaceRegexpOne(SpanAttributes['%s'], ':[0-9]+$', '')) AS peer,
		  multiIf(SpanAttributes['db.system.name'] != '', SpanAttributes['db.system.name'],
		          SpanAttributes['rpc.system'] != '', 'grpc', 'http') AS protocol,
		  count() AS reqs,
		  countIf(StatusCode = 'Error' OR SpanAttributes['http.response.status_code'] >= '500') AS errs,
		  quantile(0.95)(Duration) / 1e6 AS p95Ms
		FROM %s.otel_traces
		WHERE SpanKind = {kind:String}
		  AND Timestamp >= {since:DateTime64(9)}
		  AND knownNs != '' AND knownName != ''
		  AND peer NOT IN ('', '127.0.0.1', '::1', 'localhost')
		GROUP BY knownNs, knownName, peer, protocol
		HAVING reqs > 0
		ORDER BY reqs DESC
		LIMIT 2000 FORMAT JSONEachRow`, peerAttr, s.db)

	params := url.Values{"query": {sql}}
	params.Set("param_since", nanoToCH(since.UnixNano()))
	params.Set("param_kind", kind)

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []model.RawTraceEdge{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			KnownNs   string  `json:"knownNs"`
			KnownName string  `json:"knownName"`
			Peer      string  `json:"peer"`
			Protocol  string  `json:"protocol"`
			Reqs      uint64  `json:"reqs"`
			Errs      uint64  `json:"errs"`
			P95Ms     float64 `json:"p95Ms"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode edge row: %w", err)
		}
		out = append(out, model.RawTraceEdge{
			KnownNs: raw.KnownNs, KnownName: raw.KnownName, Peer: raw.Peer,
			KnownIsClient: knownIsClient, Protocol: raw.Protocol,
			Reqs: int64(raw.Reqs), Errs: int64(raw.Errs), P95Ms: raw.P95Ms,
		})
	}
	return out, nil
}
