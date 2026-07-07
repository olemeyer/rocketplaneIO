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

// FlowEdges liest Beylas L4-Netzwerk-Flow-Metrik (beyla.network.flow.bytes):
// Verbindungs-Kanten für Protokolle, die L7-seitig nicht parsebar sind (NATS,
// ClickHouse-native, …). Beide Seiten sind von Beyla bereits kube-dekoriert
// (k8s.src/dst.owner.name + namespace). Nur direction='request' — die
// 'response'-Zeilen sind derselbe Verkehr rückwärts und würden jede Kante
// spiegeln. Bytes = Counter-Delta je Beyla-Instanz (Restart-fest via greatest 0),
// über Instanzen summiert.
func (s *Store) FlowEdges(ctx context.Context, since time.Time) ([]model.RawFlowEdge, error) {
	sql := fmt.Sprintf(`
		SELECT srcNs, srcName, dstNs, dstName, sum(d) AS bytes
		FROM (
			SELECT
			  Attributes['k8s.src.namespace'] AS srcNs,
			  Attributes['k8s.src.owner.name'] AS srcName,
			  Attributes['k8s.dst.namespace'] AS dstNs,
			  Attributes['k8s.dst.owner.name'] AS dstName,
			  greatest(0, max(Value) - min(Value)) AS d
			FROM %s.otel_metrics_sum
			WHERE MetricName = 'beyla.network.flow.bytes'
			  AND Attributes['direction'] = 'request'
			  AND TimeUnix >= {since:DateTime64(9)}
			  AND srcName != '' AND dstName != ''
			  AND srcNs != '' AND dstNs != ''
			GROUP BY ResourceAttributes['service.instance.id'], srcNs, srcName, dstNs, dstName
		)
		GROUP BY srcNs, srcName, dstNs, dstName
		ORDER BY bytes DESC
		LIMIT 2000 FORMAT JSONEachRow`, s.db)

	params := url.Values{"query": {sql}}
	params.Set("param_since", nanoToCH(since.UnixNano()))

	body, err := s.get(ctx, params)
	if err != nil {
		return nil, err
	}
	out := []model.RawFlowEdge{}
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var raw struct {
			SrcNs   string  `json:"srcNs"`
			SrcName string  `json:"srcName"`
			DstNs   string  `json:"dstNs"`
			DstName string  `json:"dstName"`
			Bytes   float64 `json:"bytes"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decode flow row: %w", err)
		}
		out = append(out, model.RawFlowEdge{
			SrcNs: raw.SrcNs, SrcName: raw.SrcName,
			DstNs: raw.DstNs, DstName: raw.DstName, Bytes: raw.Bytes,
		})
	}
	return out, nil
}

// spanEdges fragt eine Span-Richtung ab. knownIsClient=true → Client-Spans
// (Peer = server.address), sonst Server-Spans (Peer = client.address).
func (s *Store) spanEdges(ctx context.Context, since time.Time, knownIsClient bool) ([]model.RawTraceEdge, error) {
	kind, peerAttr := "Server", "client.address"
	// Ephemeral-Port-Guard (nur Client-Spans): Beyla vertauscht bei Verbindungen,
	// die VOR dem eBPF-Attach bestanden, gelegentlich die Richtung und emittiert
	// auf der Server-Seite Client-Spans, deren server.port dann der EPHEMERAL-
	// Quellport des echten Clients ist (>=32768). Legitime Ziele haben Service-
	// Ports (5432, 8123, 443, …) — ephemere server.ports sind ein sicheres
	// Flip-Signal und werden an der Quelle verworfen (sonst entstehen Rückwärts-
	// Kanten wie postgres→pgbouncer).
	portGuard := ""
	if knownIsClient {
		kind, peerAttr = "Client", "server.address"
		portGuard = "AND (SpanAttributes['server.port'] = '' OR toUInt32OrZero(SpanAttributes['server.port']) < 32768)"
	}
	// knownNs fällt auf service.namespace zurück: Beyla verliert bei geforkten
	// DB-Backends (CNPG-Postgres) zeitweise die k8s-Dekoration, setzt aber
	// service.namespace korrekt — ohne Fallback gingen deren Kanten verloren.
	sql := fmt.Sprintf(`
		SELECT
		  coalesce(nullIf(ResourceAttributes['k8s.namespace.name'], ''), ResourceAttributes['service.namespace']) AS knownNs,
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
		  %s
		GROUP BY knownNs, knownName, peer, protocol
		HAVING reqs > 0
		ORDER BY reqs DESC
		LIMIT 2000 FORMAT JSONEachRow`, peerAttr, s.db, portGuard)

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
