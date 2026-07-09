package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"text/template"
)

// agent_manifest.go rendert das Kubernetes-Install-Manifest für den Agenten und
// liefert es unter GET /api/agent/manifest aus. So wird der „Connect cluster"-
// Command zu einem einzigen `kubectl apply -f <url>`: kubectl (auf dem Laptop)
// holt dieses Manifest — Enroll-Token, Cluster-Name, Image und die agent-seitige
// Control-Plane-URL sind bereits eingesetzt — und wendet es auf den aktuellen
// Context an. Zwei Varianten:
//   - Flows aus  → gehärtetes Deployment (nur Topologie, read-only)
//   - Flows an   → DaemonSet + hostNetwork + NET_ADMIN (conntrack-Flows für die Map)

type manifestData struct {
	Image           string
	ControlPlaneURL string
	EnrollToken     string
	ClusterName     string
	AgentVersion    string
	Flows           bool
}

// y quotet einen Wert sicher als YAML — JSON-Strings sind valides YAML, also
// erledigt json.Marshal das Escaping (Anführungszeichen, Sonderzeichen).
func y(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var agentManifestTmpl = template.Must(template.New("manifest").Funcs(template.FuncMap{"y": y}).Parse(`apiVersion: v1
kind: Namespace
metadata:
  name: rocketplane
  labels:
    app.kubernetes.io/part-of: rocketplane
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: rocketplane-agent
  namespace: rocketplane
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: rocketplane-agent
rules:
  - apiGroups: [""]
    resources: ["namespaces", "pods", "services", "nodes", "persistentvolumeclaims"]
    verbs: ["get", "list", "watch"]
  # debug_bundle / pod_events: Events lesen (Triage-Signale)
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["get", "list"]
  # kubelet stats/summary via API-Server-Proxy (Hardware-/Volume-Auslastung)
  - apiGroups: [""]
    resources: ["nodes/proxy"]
    verbs: ["get"]
  # HA leader election: with replicas>1 exactly one agent syncs and acts.
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "create", "update"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["delete"]
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets", "daemonsets"]
    verbs: ["get", "list", "watch", "patch"]
  # Node-Wartung: cordon/drain (patch) + PDB-respektierende Evictions
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["patch"]
  - apiGroups: [""]
    resources: ["pods/eviction"]
    verbs: ["create"]
  # rollout undo: vorherige Revision aus den ReplicaSets lesen
  - apiGroups: ["apps"]
    resources: ["replicasets"]
    verbs: ["get", "list", "watch"]
  # hpa_set: HPA-Grenzen (min/max) anpassen
  - apiGroups: ["autoscaling"]
    resources: ["horizontalpodautoscalers"]
    verbs: ["get", "list", "watch", "patch"]
  # cronjob_trigger/suspend/resume + manuelle Job-Läufe
  - apiGroups: ["batch"]
    resources: ["cronjobs"]
    verbs: ["get", "list", "watch", "patch"]
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["get", "list", "watch", "create", "delete"]
  # Katalog Batch 2 — get_resource/describe_resource (Config lesen), patch_secret,
  # patch_configmap, create/delete_configmap, Helm-Release-Einblick (Secrets lesen)
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "update", "patch"]
  - apiGroups: [""]
    resources: ["serviceaccounts", "resourcequotas", "limitranges"]
    verbs: ["get", "list"]
  # exec_readonly: EIN whitelisted Diagnose-Kommando (cat/ls/…) im Container
  - apiGroups: [""]
    resources: ["pods/exec"]
    verbs: ["create"]
  # pod_logs: kubectl logs [--previous] direkt vom Kubelet
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
  # run_debug_pod: ephemere Probe-Pods (auto-cleanup; delete ist oben schon erlaubt)
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["create"]
  # patch_resource/restore_resource: Service/Ingress/NetworkPolicy/PDB editieren
  - apiGroups: [""]
    resources: ["services"]
    verbs: ["patch", "update"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses", "networkpolicies"]
    verbs: ["get", "list", "watch", "patch", "update"]
  - apiGroups: ["policy"]
    resources: ["poddisruptionbudgets"]
    verbs: ["get", "list", "watch", "patch", "update"]
  # pvc_expand: Storage-Requests vergrößern (nur wachsen — Guard im Agenten)
  - apiGroups: [""]
    resources: ["persistentvolumeclaims"]
    verbs: ["patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: rocketplane-agent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: rocketplane-agent
subjects:
  - kind: ServiceAccount
    name: rocketplane-agent
    namespace: rocketplane
---
apiVersion: v1
kind: Secret
metadata:
  name: rocketplane-agent-enroll
  namespace: rocketplane
type: Opaque
stringData:
  enroll-token: {{ y .EnrollToken }}
---
{{- if .Flows }}
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: rocketplane-agent
  namespace: rocketplane
  labels:
    app.kubernetes.io/name: rocketplane-agent
    app.kubernetes.io/part-of: rocketplane
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: rocketplane-agent
  template:
    metadata:
      labels:
        app.kubernetes.io/name: rocketplane-agent
        app.kubernetes.io/part-of: rocketplane
    spec:
      serviceAccountName: rocketplane-agent
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      tolerations:
        - operator: Exists
      containers:
        - name: agent
          image: {{ y .Image }}
          imagePullPolicy: IfNotPresent
          securityContext:
            capabilities:
              add: ["NET_ADMIN"]
              drop: ["ALL"]
          env:
            - name: RP_CONTROLPLANE_URL
              value: {{ y .ControlPlaneURL }}
            - name: RP_ENROLL_TOKEN
              valueFrom:
                secretKeyRef:
                  name: rocketplane-agent-enroll
                  key: enroll-token
            - name: RP_CLUSTER_NAME
              value: {{ y .ClusterName }}
            - name: RP_AGENT_VERSION
              value: {{ y .AgentVersion }}
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          # Zero-config Log-Collection: Node-Logs read-only mitlesen.
          # /var/lib/docker/containers deckt Docker-Runtime-Symlinks ab (minikube).
          volumeMounts:
            - name: varlogpods
              mountPath: /var/log/pods
              readOnly: true
            - name: varlibdockercontainers
              mountPath: /var/lib/docker/containers
              readOnly: true
          resources:
            requests: {cpu: 50m, memory: 96Mi}
            limits: {cpu: 300m, memory: 256Mi}
      volumes:
        - name: varlogpods
          hostPath: {path: /var/log/pods}
        - name: varlibdockercontainers
          hostPath: {path: /var/lib/docker/containers}
{{- else }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: rocketplane-agent
  namespace: rocketplane
  labels:
    app.kubernetes.io/name: rocketplane-agent
    app.kubernetes.io/part-of: rocketplane
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: rocketplane-agent
  template:
    metadata:
      labels:
        app.kubernetes.io/name: rocketplane-agent
        app.kubernetes.io/part-of: rocketplane
    spec:
      serviceAccountName: rocketplane-agent
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: agent
          image: {{ y .Image }}
          imagePullPolicy: IfNotPresent
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          env:
            - name: RP_CONTROLPLANE_URL
              value: {{ y .ControlPlaneURL }}
            - name: RP_ENROLL_TOKEN
              valueFrom:
                secretKeyRef:
                  name: rocketplane-agent-enroll
                  key: enroll-token
            - name: RP_CLUSTER_NAME
              value: {{ y .ClusterName }}
            - name: RP_AGENT_VERSION
              value: {{ y .AgentVersion }}
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          resources:
            requests: {cpu: 50m, memory: 64Mi}
            limits: {cpu: 200m, memory: 128Mi}
{{- end }}
---
# Beyla — eBPF auto-instrumentation: L7 traces + L4 network flows produce the
# service map and RED metrics, no code changes. Privileged + host-level, like
# every eBPF monitor (Cilium/Coroot run the same way). It exports OTLP to an
# in-cluster collector Service at otel-collector:4318; deploy one alongside, or
# use the Helm chart to point Beyla at a different endpoint (beyla.otlpEndpoint)
# or turn it off (beyla.enabled=false).
apiVersion: v1
kind: ServiceAccount
metadata:
  name: rocketplane-beyla
  namespace: rocketplane
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: rocketplane-beyla
rules:
  - apiGroups: [""]
    resources: ["pods", "services", "nodes"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["replicasets", "deployments", "statefulsets", "daemonsets"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: rocketplane-beyla
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: rocketplane-beyla
subjects:
  - kind: ServiceAccount
    name: rocketplane-beyla
    namespace: rocketplane
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: rocketplane-beyla
  namespace: rocketplane
  labels:
    app.kubernetes.io/name: rocketplane-beyla
    app.kubernetes.io/part-of: rocketplane
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: rocketplane-beyla
  template:
    metadata:
      labels:
        app.kubernetes.io/name: rocketplane-beyla
        app.kubernetes.io/part-of: rocketplane
    spec:
      serviceAccountName: rocketplane-beyla
      hostPID: true
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      tolerations:
        - operator: Exists
      containers:
        - name: beyla
          image: grafana/beyla:2.2
          securityContext:
            privileged: true
          env:
            - name: BEYLA_OPEN_PORT
              value: "80,443,3000,3001,4173,5000,5678,8000,8080,8090,8443,9000,3306,5432,6379,27017"
            - name: BEYLA_SKIP_GO_SPECIFIC_TRACERS
              value: "true"
            - name: BEYLA_KUBE_METADATA_ENABLE
              value: "true"
            - name: BEYLA_NETWORK_METRICS
              value: "true"
            - name: BEYLA_OTEL_METRICS_FEATURES
              value: "application,network"
            - name: BEYLA_KUBE_CLUSTER_NAME
              value: {{ y .ClusterName }}
            - name: OTEL_EXPORTER_OTLP_ENDPOINT
              value: "http://otel-collector:4318"
            - name: OTEL_EXPORTER_OTLP_PROTOCOL
              value: "http/protobuf"
            - name: BEYLA_METRICS_INTERVAL
              value: "15s"
            - name: BEYLA_BPF_CONTEXT_PROPAGATION
              value: "headers"
            - name: BEYLA_BPF_TRACK_REQUEST_HEADERS
              value: "true"
          resources:
            requests: {cpu: 50m, memory: 256Mi}
            limits: {cpu: 800m, memory: 1Gi}
`))

// handleAgentManifest rendert das Install-Manifest. Der Enroll-Token (Query)
// autorisiert den Aufruf implizit — das Manifest enthält nur genau diesen dem
// Aufrufer bereits bekannten Token, es leakt also nichts.
func (s *Server) handleAgentManifest(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if !strings.HasPrefix(token, "rpe_") || len(token) < 8 {
		writeErr(w, http.StatusBadRequest, "valid enroll token required")
		return
	}
	agentVersion := "0.3.0-flows"
	if !s.cfg.AgentFlows {
		agentVersion = "0.3.0"
	}
	data := manifestData{
		Image:           s.cfg.AgentImage,
		ControlPlaneURL: s.cfg.AgentControlPlaneURL,
		EnrollToken:     token,
		ClusterName:     strings.TrimSpace(r.URL.Query().Get("name")),
		AgentVersion:    agentVersion,
		Flows:           s.cfg.AgentFlows,
	}

	var buf bytes.Buffer
	if err := agentManifestTmpl.Execute(&buf, data); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to render manifest")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
