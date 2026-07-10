package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// actions_handlers.go — Safe-Actions über beide Kanäle:
//   Browser (Session): Aktion anlegen + Feed lesen, Workload-Pods listen.
//   Agent (Bearer):    pending claimen, Ergebnis melden.
// Die Whitelist ist bewusst eng — jede Aktion ist ein konkreter, benannter
// kubectl-Handgriff, nichts Generisches.

// actionRules: erlaubte Aktionen je Target-Kind + Params-Validierung.
var actionTargetKinds = map[string]map[string]bool{
	"rollout_restart": {"Deployment": true, "StatefulSet": true, "DaemonSet": true},
	"rollout_undo":    {"Deployment": true},
	"scale":           {"Deployment": true, "StatefulSet": true},
	"delete_pod":      {"Pod": true},
	// Release-Handgriffe des Alltags
	"set_image":      {"Deployment": true, "StatefulSet": true, "DaemonSet": true},
	"rollout_pause":  {"Deployment": true},
	"rollout_resume": {"Deployment": true},
	// Autoscaling (die ehrliche Skalierung in HPA-Clustern)
	"hpa_set": {"HorizontalPodAutoscaler": true},
	// Batch
	"cronjob_trigger": {"CronJob": true},
	"cronjob_suspend": {"CronJob": true},
	"cronjob_resume":  {"CronJob": true},
	// Housekeeping (namespace-scoped: TargetName = Namespace)
	"cleanup_pods": {"Namespace": true},
	// Read-only Investigation-Bündel (kein Mutieren)
	"pod_events":   {"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Pod": true},
	"debug_bundle": {"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Pod": true},
	// Node-Wartung (cluster-scoped — namespace ist "-")
	"cordon":       {"Node": true},
	"uncordon":     {"Node": true},
	"drain":        {"Node": true},
	"node_taint":   {"Node": true},
	"node_untaint": {"Node": true},
	// Erweiterter Katalog (Batch 1)
	"annotate":              {"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Pod": true, "Node": true, "Namespace": true, "ConfigMap": true, "Service": true, "PersistentVolumeClaim": true, "CronJob": true, "HorizontalPodAutoscaler": true},
	"set_label":             {"Node": true, "Namespace": true},
	"patch_configmap":       {"ConfigMap": true},
	"set_env":               {"Deployment": true, "StatefulSet": true, "DaemonSet": true},
	"set_resources":         {"Deployment": true, "StatefulSet": true, "DaemonSet": true},
	"statefulset_partition": {"StatefulSet": true},
	"hpa_toggle":            {"HorizontalPodAutoscaler": true},
	"rollout_to_revision":   {"Deployment": true},
	"evict_pod":             {"Pod": true},
	"cleanup_jobs":          {"Namespace": true},
	"rollout_history":       {"Deployment": true, "StatefulSet": true}, // read
	"drain_preview":         {"Node": true},                            // read
	// Erweiterter Katalog (Batch 2 — Profi-Abdeckung)
	"get_resource":      generalReadKinds, // read: YAML jeder whitelisted Ressource
	"describe_resource": generalReadKinds, // read: Status+Conditions+Events
	"get_secret":        {"Secret": true}, // read: Keys+Hashes (nie Klartext)
	"helm_releases":     {"Namespace": true},
	"exec_readonly":     {"Pod": true},
	"patch_secret":      {"Secret": true},
	"create_configmap":  {"ConfigMap": true},
	"delete_configmap":  {"ConfigMap": true},
	"pdb_set":           {"PodDisruptionBudget": true},
	"patch_resource":    {"Service": true, "Ingress": true, "NetworkPolicy": true, "PodDisruptionBudget": true, "ResourceQuota": true, "LimitRange": true},
	"pvc_expand":        {"PersistentVolumeClaim": true},
	"delete_job":        {"Job": true},
	"restore_resource":  {"Service": true, "Ingress": true, "NetworkPolicy": true, "PodDisruptionBudget": true, "ConfigMap": true, "ResourceQuota": true, "LimitRange": true},
	// Katalog Batch 3 — generische Incident-Primitive
	"pod_logs":      {"Pod": true},       // read: kubectl logs [--previous] direkt vom Kubelet
	"list_events":   {"Namespace": true}, // read: alle jüngsten Events eines Namespaces
	"run_debug_pod": {"Namespace": true}, // ephemerer Probe-Pod (Image-Bisect, HTTP/DNS-Probe, PVC-Inspektion)
	"net_probe":     {"Namespace": true}, // read: http/tcp/dns/tls-Probe direkt vom Agenten (Erreichbarkeit, Cert-Expiry)
}

// generalReadKinds: alles, was get_resource/describe_resource lesen dürfen.
var generalReadKinds = map[string]bool{
	"Deployment": true, "StatefulSet": true, "DaemonSet": true, "Pod": true,
	"ConfigMap": true, "Secret": true, "Service": true, "Ingress": true,
	"NetworkPolicy": true, "PodDisruptionBudget": true, "HorizontalPodAutoscaler": true,
	"CronJob": true, "Job": true, "PersistentVolumeClaim": true, "Node": true,
	"Namespace": true, "ServiceAccount": true, "ResourceQuota": true, "LimitRange": true,
}

// validateActionParams prüft die typisierten Params je Kind — nichts
// Unvalidiertes erreicht den Cluster. Kinds ohne Params (rollout_*, cordon,
// cronjob_*, cleanup_pods …) fallen durch auf true.
func validateActionParams(w http.ResponseWriter, kind string, raw json.RawMessage) bool {
	// Generisch für ALLE Kinds: optionaler params.timeoutSeconds — der Copilot
	// (und die UI) steuern den Ablauf-Timeout je Situation; die Grenzen sind hart.
	if len(raw) > 0 {
		var tp struct {
			TimeoutSeconds *float64 `json:"timeoutSeconds"`
		}
		if json.Unmarshal(raw, &tp) == nil && tp.TimeoutSeconds != nil && (*tp.TimeoutSeconds < 10 || *tp.TimeoutSeconds > 1800) {
			writeErr(w, http.StatusBadRequest, "params.timeoutSeconds must be 10..1800 seconds")
			return false
		}
	}
	switch kind {
	case "scale":
		var p struct {
			Replicas *int `json:"replicas"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Replicas == nil || *p.Replicas < 0 || *p.Replicas > 50 {
			writeErr(w, http.StatusBadRequest, "scale requires params.replicas in 0..50")
			return false
		}
	case "set_image":
		var p struct {
			Image string `json:"image"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Image == "" || len(p.Image) > 512 {
			writeErr(w, http.StatusBadRequest, "set_image requires params.image")
			return false
		}
	case "hpa_set":
		var p struct {
			MinReplicas *int `json:"minReplicas"`
			MaxReplicas *int `json:"maxReplicas"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.MaxReplicas == nil {
			writeErr(w, http.StatusBadRequest, "hpa_set requires params.maxReplicas")
			return false
		}
		min := 1
		if p.MinReplicas != nil {
			min = *p.MinReplicas
		}
		if min < 1 || *p.MaxReplicas < min || *p.MaxReplicas > 200 {
			writeErr(w, http.StatusBadRequest, "hpa_set bounds must satisfy 1 <= min <= max <= 200")
			return false
		}
	case "node_taint":
		var p struct {
			Key    string `json:"key"`
			Effect string `json:"effect"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Key == "" {
			writeErr(w, http.StatusBadRequest, "node_taint requires params.key")
			return false
		}
		switch p.Effect {
		case "NoSchedule", "PreferNoSchedule", "NoExecute":
		default:
			writeErr(w, http.StatusBadRequest, "node_taint effect must be NoSchedule, PreferNoSchedule or NoExecute")
			return false
		}
	case "node_untaint":
		var p struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Key == "" {
			writeErr(w, http.StatusBadRequest, "node_untaint requires params.key")
			return false
		}
	case "annotate", "set_label":
		var p struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Remove bool   `json:"remove"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Key == "" || len(p.Key) > 253 || len(p.Value) > 4096 {
			writeErr(w, http.StatusBadRequest, kind+" requires params.key (<=253 chars, value <=4KiB)")
			return false
		}
	case "patch_configmap":
		var p struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Key == "" || len(p.Value) > 64*1024 {
			writeErr(w, http.StatusBadRequest, "patch_configmap requires params.key (value <=64KiB)")
			return false
		}
	case "set_env":
		var p struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Name == "" || len(p.Name) > 128 || len(p.Value) > 8192 {
			writeErr(w, http.StatusBadRequest, "set_env requires params.name (<=128 chars, value <=8KiB)")
			return false
		}
	case "evict_pod":
		var p struct {
			GracePeriodSeconds *int `json:"gracePeriodSeconds"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || (p.GracePeriodSeconds != nil && (*p.GracePeriodSeconds < 0 || *p.GracePeriodSeconds > 3600)) {
			writeErr(w, http.StatusBadRequest, "evict_pod gracePeriodSeconds must be 0..3600")
			return false
		}
	case "cleanup_jobs":
		var p struct {
			States         []string `json:"states"`
			OlderThanHours *int     `json:"olderThanHours"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || (p.OlderThanHours != nil && *p.OlderThanHours < 0) {
			writeErr(w, http.StatusBadRequest, "cleanup_jobs olderThanHours must be >= 0")
			return false
		}
		for _, st := range p.States {
			if st != "Complete" && st != "Failed" {
				writeErr(w, http.StatusBadRequest, "cleanup_jobs states must be Complete and/or Failed")
				return false
			}
		}
	case "set_resources":
		var p struct {
			RequestsCPU    string `json:"requestsCpu"`
			RequestsMemory string `json:"requestsMemory"`
			LimitsCPU      string `json:"limitsCpu"`
			LimitsMemory   string `json:"limitsMemory"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || (p.RequestsCPU == "" && p.RequestsMemory == "" && p.LimitsCPU == "" && p.LimitsMemory == "") {
			writeErr(w, http.StatusBadRequest, "set_resources requires at least one of requestsCpu/requestsMemory/limitsCpu/limitsMemory")
			return false
		}
	case "statefulset_partition":
		var p struct {
			Partition *int `json:"partition"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Partition == nil || *p.Partition < 0 {
			writeErr(w, http.StatusBadRequest, "statefulset_partition requires params.partition >= 0")
			return false
		}
	case "hpa_toggle":
		var p struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Enabled == nil {
			writeErr(w, http.StatusBadRequest, "hpa_toggle requires params.enabled (bool)")
			return false
		}
	case "rollout_to_revision":
		var p struct {
			Revision *int64 `json:"revision"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Revision == nil || *p.Revision < 1 {
			writeErr(w, http.StatusBadRequest, "rollout_to_revision requires params.revision >= 1")
			return false
		}
	case "patch_secret":
		var p struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Remove bool   `json:"remove"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Key == "" || len(p.Key) > 253 || len(p.Value) > 64*1024 {
			writeErr(w, http.StatusBadRequest, "patch_secret requires params.key (<=253 chars, value <=64KiB)")
			return false
		}
	case "create_configmap":
		var p struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || len(p.Data) == 0 {
			writeErr(w, http.StatusBadRequest, "create_configmap requires params.data (non-empty map)")
			return false
		}
		total := 0
		for k, v := range p.Data {
			if k == "" || len(k) > 253 {
				writeErr(w, http.StatusBadRequest, "create_configmap keys must be 1..253 chars")
				return false
			}
			total += len(v)
		}
		if total > 512*1024 {
			writeErr(w, http.StatusBadRequest, "create_configmap data too large (max 512KiB)")
			return false
		}
	case "pdb_set":
		var p struct {
			MinAvailable   *string `json:"minAvailable"`
			MaxUnavailable *string `json:"maxUnavailable"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || (p.MinAvailable == nil && p.MaxUnavailable == nil) || (p.MinAvailable != nil && p.MaxUnavailable != nil) {
			writeErr(w, http.StatusBadRequest, "pdb_set requires EXACTLY ONE of params.minAvailable / params.maxUnavailable (int or percentage string)")
			return false
		}
	case "patch_resource":
		if len(raw) > 64*1024 {
			writeErr(w, http.StatusBadRequest, "patch_resource params too large (max 64KiB)")
			return false
		}
		var p struct {
			Patch map[string]any `json:"patch"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || len(p.Patch) == 0 {
			writeErr(w, http.StatusBadRequest, "patch_resource requires params.patch (JSON merge patch object)")
			return false
		}
		// Identität/Typ dürfen nie umgeschrieben werden.
		for _, forbidden := range []string{"kind", "apiVersion"} {
			if _, ok := p.Patch[forbidden]; ok {
				writeErr(w, http.StatusBadRequest, "patch_resource must not change "+forbidden)
				return false
			}
		}
		if md, ok := p.Patch["metadata"].(map[string]any); ok {
			for _, forbidden := range []string{"name", "namespace", "uid"} {
				if _, bad := md[forbidden]; bad {
					writeErr(w, http.StatusBadRequest, "patch_resource must not change metadata."+forbidden)
					return false
				}
			}
		}
	case "pvc_expand":
		var p struct {
			Size string `json:"size"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Size == "" || len(p.Size) > 32 {
			writeErr(w, http.StatusBadRequest, "pvc_expand requires params.size (e.g. \"20Gi\")")
			return false
		}
	case "exec_readonly":
		var p struct {
			Command   []string `json:"command"`
			Container string   `json:"container"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || len(p.Command) == 0 {
			writeErr(w, http.StatusBadRequest, "exec_readonly requires params.command (argv array)")
			return false
		}
		if err := validateExecCommand(p.Command); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return false
		}
	case "restore_resource":
		if len(raw) > 300*1024 {
			writeErr(w, http.StatusBadRequest, "restore_resource snapshot too large")
			return false
		}
		var p struct {
			Snapshot map[string]any `json:"snapshot"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || len(p.Snapshot) == 0 {
			writeErr(w, http.StatusBadRequest, "restore_resource requires params.snapshot (the before-object)")
			return false
		}
	case "get_secret":
		// keine Params nötig; reveal ist bewusst NICHT implementiert (v1) —
		// Secret-Werte erreichen weder LLM noch DB.
	case "pod_logs":
		var p struct {
			TailLines *int `json:"tailLines"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || (p.TailLines != nil && (*p.TailLines < 1 || *p.TailLines > 500)) {
			writeErr(w, http.StatusBadRequest, "pod_logs tailLines must be 1..500")
			return false
		}
	case "list_events":
		var p struct {
			Limit *int `json:"limit"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || (p.Limit != nil && (*p.Limit < 1 || *p.Limit > 100)) {
			writeErr(w, http.StatusBadRequest, "list_events limit must be 1..100")
			return false
		}
	case "net_probe":
		var p struct {
			Mode   string `json:"mode"`
			Target string `json:"target"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Target == "" || len(p.Target) > 512 {
			writeErr(w, http.StatusBadRequest, "net_probe requires params.target (<=512 chars)")
			return false
		}
		switch p.Mode {
		case "http", "tcp", "dns", "tls":
		default:
			writeErr(w, http.StatusBadRequest, "net_probe mode must be http, tcp, dns or tls")
			return false
		}
	case "run_debug_pod":
		var p struct {
			Image   string   `json:"image"`
			Command []string `json:"command"`
			PVCName string   `json:"pvcName"`
			Node    string   `json:"nodeName"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Image == "" || len(p.Image) > 256 || strings.ContainsAny(p.Image, " \t\n") {
			writeErr(w, http.StatusBadRequest, "run_debug_pod requires params.image (no whitespace, <=256 chars)")
			return false
		}
		if len(p.Command) == 0 || len(p.Command) > 20 {
			writeErr(w, http.StatusBadRequest, "run_debug_pod requires params.command (argv, 1..20 args)")
			return false
		}
		for _, c := range p.Command {
			if len(c) > 2048 {
				writeErr(w, http.StatusBadRequest, "run_debug_pod command argument too long")
				return false
			}
		}
		if len(p.PVCName) > 253 || len(p.Node) > 253 {
			writeErr(w, http.StatusBadRequest, "run_debug_pod pvcName/nodeName too long")
			return false
		}
	}
	return true
}

// hasResourceOverride: get_resource/describe_resource mit explizitem
// apiVersion+resource (Plural) — der CRD-Lesepfad.
func hasResourceOverride(raw json.RawMessage) bool {
	var p struct {
		APIVersion string `json:"apiVersion"`
		Resource   string `json:"resource"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return false
	}
	return p.APIVersion != "" && p.Resource != "" && len(p.APIVersion) <= 100 && len(p.Resource) <= 100
}

// execAllowedCommands: read-only Diagnose-Kommandos, argv-basiert (keine Shell).
var execAllowedCommands = map[string]bool{
	"cat": true, "ls": true, "head": true, "tail": true, "df": true, "du": true,
	"stat": true, "wc": true, "env": true, "id": true, "uname": true, "find": true,
}

// validateExecCommand erzwingt Whitelist + verbietet Shell-Metazeichen — es gibt
// keine Shell, aber Defense-in-Depth gegen busybox-Wrapper und Log-Injection.
func validateExecCommand(argv []string) error {
	if !execAllowedCommands[argv[0]] {
		return fmt.Errorf("exec_readonly: command %q is not in the read-only whitelist", argv[0])
	}
	if len(argv) > 12 {
		return errors.New("exec_readonly: too many arguments (max 12)")
	}
	for _, a := range argv {
		if len(a) > 512 {
			return errors.New("exec_readonly: argument too long")
		}
		if strings.ContainsAny(a, "|;&$><`\n\r") {
			return fmt.Errorf("exec_readonly: argument %q contains shell metacharacters", a)
		}
	}
	// find darf nicht in -exec/-delete eskalieren.
	if argv[0] == "find" {
		for _, a := range argv[1:] {
			if a == "-exec" || a == "-execdir" || a == "-delete" || a == "-ok" || a == "-okdir" {
				return errors.New("exec_readonly: find must not use -exec/-delete")
			}
		}
	}
	return nil
}

type createActionReq struct {
	Kind            string          `json:"kind"`
	TargetNamespace string          `json:"targetNamespace"`
	TargetKind      string          `json:"targetKind"`
	TargetName      string          `json:"targetName"`
	Params          json.RawMessage `json:"params"`
	// Script-Actions: Definition + benannte Argumente. Die Source wird beim
	// Dispatch GESNAPSHOTTET — der Audit-Trail zeigt exakt, was lief.
	DefinitionID string            `json:"definitionId"`
	Args         map[string]string `json:"args"`
	// Ad-hoc-Script (Copilot propose_script): Source direkt statt Definition.
	// Wird wie im Editor kompiliert (checkScriptSource) — nichts Kaputtes läuft.
	Source         string            `json:"source"`
	Title          string            `json:"title"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
	ScriptArgs     map[string]string `json:"scriptArgs"`
	// Safe Actions v2 grouping (optional). The Copilot passes chatId + turnId so
	// all actions from one turn land in ONE group (a trace); investigationNodeId
	// back-links the run to the Investigation node that spawned it. A caller may
	// instead pass an explicit groupId (manual batch). When none are set, the
	// action gets its own group-of-one.
	GroupID             string `json:"groupId"`
	ChatID              string `json:"chatId"`
	InvestigationID     string `json:"investigationId"`
	TurnID              string `json:"turnId"`
	InvestigationNodeID string `json:"investigationNodeId"`
}

// handleCreateAction — POST /api/orgs/{org}/clusters/{cluster}/actions
func (s *Server) handleCreateAction(w http.ResponseWriter, r *http.Request) {
	orgID, role, ok := s.resolveOrgRole(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "cluster not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load cluster")
		return
	}

	var req createActionReq
	if !decode(w, r, &req) {
		return
	}
	if req.Kind == "script" && req.DefinitionID == "" && req.Source != "" {
		// Ad-hoc-Script vom Copilot (propose_script): kompilieren wie im Editor,
		// dann als normale Script-Action (destructive, arm-to-fire) einreihen.
		if len(req.Source) > 64*1024 {
			writeErr(w, http.StatusBadRequest, "script source too large (max 64KiB)")
			return
		}
		title := req.Title
		if title == "" {
			title = "adhoc-script"
		}
		if err := checkScriptSource(title, req.Source); err != nil {
			writeErr(w, http.StatusBadRequest, "script does not compile: "+err.Error())
			return
		}
		timeout := req.TimeoutSeconds
		if timeout < 30 || timeout > 1800 {
			timeout = 600
		}
		if req.ScriptArgs == nil {
			req.ScriptArgs = map[string]string{}
		}
		params, _ := json.Marshal(map[string]any{
			"source":         req.Source,
			"args":           req.ScriptArgs,
			"timeoutSeconds": timeout,
		})
		req.Params = params
		req.TargetKind = "Script"
		req.TargetName = title
		if req.TargetNamespace == "" {
			req.TargetNamespace = "-"
		}
	} else if req.Kind == "script" {
		defID, err := uuid.Parse(req.DefinitionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "script actions require a definitionId")
			return
		}
		def, err := s.store.GetActionDefinition(r.Context(), orgID, defID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "action definition not found")
			return
		}
		if req.Args == nil {
			req.Args = map[string]string{}
		}
		// Typed-Args-Validierung: nichts Untypisiertes erreicht den Cluster.
		specs, err := parseParamSpecs(def.Params)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "definition has invalid params")
			return
		}
		if err := validateArgs(specs, req.Args); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		params, _ := json.Marshal(map[string]any{
			"definitionId":   def.ID,
			"source":         def.Source,
			"args":           req.Args,
			"timeoutSeconds": def.TimeoutSeconds,
		})
		req.Params = params
		req.TargetKind = "Script"
		req.TargetName = def.Name
		if req.TargetNamespace == "" {
			req.TargetNamespace = "-"
		}
	} else {
		kinds, okKind := actionTargetKinds[req.Kind]
		if !okKind || !kinds[req.TargetKind] {
			// Ausnahme: get_resource/describe_resource dürfen mit explizitem
			// params.apiVersion+resource JEDEN Kind lesen (CRDs: Certificates,
			// ArgoCD Applications, CNPG Clusters, …) — read-only, kein Risiko.
			if !(okKind && (req.Kind == "get_resource" || req.Kind == "describe_resource" || req.Kind == "patch_resource") && hasResourceOverride(req.Params)) {
				writeErr(w, http.StatusBadRequest, "unsupported action/target combination")
				return
			}
		}
		if (req.TargetKind == "Node" || req.TargetKind == "Namespace") && req.TargetNamespace == "" {
			req.TargetNamespace = "-" // cluster- bzw. namespace-scoped Ziele
		}
		if req.TargetNamespace == "" || req.TargetName == "" {
			writeErr(w, http.StatusBadRequest, "targetNamespace and targetName are required")
			return
		}
		if !validateActionParams(w, req.Kind, req.Params) {
			return
		}
	}

	// RBAC on mutation blast-radius: a plain member may run read-level actions
	// (diagnostics: get_resource, pod_logs, net_probe, describe, …) but any
	// cluster-mutating action (reversible/disruptive/destructive, incl. scripts)
	// requires admin. The level is the same one the Copilot guardrails use.
	var lvlParams map[string]any
	_ = json.Unmarshal(req.Params, &lvlParams)
	level := actionLevel(req.Kind, lvlParams)
	if level != "read" && roleRank(role) < roleRank("admin") {
		writeErr(w, http.StatusForbidden, "running "+level+" actions requires admin role — a member can only run read-only diagnostics")
		return
	}

	user, _ := auth.UserFrom(r.Context())

	// Safe Actions v2: resolve the group this run belongs to.
	//   copilot turn (chatId+turnId) → one shared group per turn (idempotent),
	//   explicit groupId            → append to that group,
	//   neither                     → group-of-one (via CreateAction).
	sourceKind := "builtin"
	if req.Kind == "script" {
		sourceKind = "script"
	}
	var invNodeID *uuid.UUID
	if id, err := uuid.Parse(req.InvestigationNodeID); err == nil {
		invNodeID = &id
	}
	var a *model.Action
	var groupID *uuid.UUID
	if chatID, err := uuid.Parse(req.ChatID); err == nil && req.TurnID != "" {
		var invID *uuid.UUID
		if id, e := uuid.Parse(req.InvestigationID); e == nil {
			invID = &id
		}
		title := req.Kind
		if req.TargetName != "" {
			title = req.Kind + " " + req.TargetName
		}
		g, gerr := s.store.GetOrCreateTurnGroup(r.Context(), clusterID, user.ID, &chatID, invID, req.TurnID, title)
		if gerr != nil {
			writeErr(w, http.StatusInternalServerError, "failed to open action group")
			return
		}
		groupID = &g.ID
	} else if id, err := uuid.Parse(req.GroupID); err == nil {
		groupID = &id
	}
	var err error
	if groupID != nil {
		a, err = s.store.AppendAction(r.Context(), *groupID, invNodeID, clusterID, user.ID, req.Kind, req.TargetNamespace, req.TargetKind, req.TargetName, sourceKind, req.Params)
	} else {
		a, err = s.store.CreateAction(r.Context(), clusterID, user.ID, req.Kind, req.TargetNamespace, req.TargetKind, req.TargetName, req.Params)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create action")
		return
	}
	a.RequestedBy = user.Email
	// Audit every non-read action (the mutation trail SREs and auditors want).
	if level != "read" {
		s.audit(r, &orgID, "action.created", "action", a.ID.String(), req.Kind+" "+req.TargetKind+"/"+req.TargetName, map[string]any{"level": level, "kind": req.Kind})
	}
	s.broker.Publish(clusterID, "actions", 0)
	// dispatch weckt den Agent-Stream: claimen in Push-Latenz statt Poll-Takt.
	s.broker.Publish(clusterID, "dispatch", 0)
	writeJSON(w, http.StatusCreated, a)
}

// handleListActions — GET /api/orgs/{org}/clusters/{cluster}/actions?namespace=&target=&limit=
func (s *Server) handleListActions(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	actions, err := s.store.ListActions(r.Context(), clusterID, q.Get("namespace"), q.Get("target"), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list actions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

// handleWorkloadPods — GET /api/orgs/{org}/clusters/{cluster}/workload-pods?namespace=&kind=&name=
func (s *Server) handleWorkloadPods(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	q := r.URL.Query()
	pods, err := s.store.ListWorkloadPods(r.Context(), clusterID, q.Get("namespace"), q.Get("kind"), q.Get("name"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list pods")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pods": pods})
}

// handleAgentActions — GET /api/agent/actions (claimt pending → running; der
// Agent ruft das auf ein dispatch-Signal hin auf, plus seltener Fallback-Poll).
func (s *Server) handleAgentActions(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := auth.ClusterIDFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no cluster in context")
		return
	}
	actions, err := s.store.ClaimPendingActions(r.Context(), clusterID, 10)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to claim actions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

// handleAgentActionResult — POST /api/agent/actions/{action}/result
func (s *Server) handleAgentActionResult(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := auth.ClusterIDFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no cluster in context")
		return
	}
	actionID, err := uuid.Parse(r.PathValue("action"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid action id")
		return
	}
	var req struct {
		Status   string          `json:"status"`
		Result   string          `json:"result"`
		Progress string          `json:"progress"`
		Steps    json.RawMessage `json:"steps"`
		// Revert: inverse Katalog-Action mit Before-Snapshot (nur bei succeeded).
		Revert json.RawMessage `json:"revert"`
		// Snapshot: das VOR der Mutation gestrippte Zielobjekt (generisch).
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Result) > 32_000 {
		req.Result = req.Result[:32_000]
	}
	if len(req.Progress) > 500 {
		req.Progress = req.Progress[:500]
	}
	if len(req.Steps) > 64_000 {
		writeErr(w, http.StatusBadRequest, "steps too large")
		return
	}
	if len(req.Revert) > 300_000 {
		req.Revert = nil // Revert ist optional — zu groß heisst schlicht: keiner
	}
	if len(req.Snapshot) > 300_000 {
		req.Snapshot = nil
	}
	var err2 error
	cancel := false
	switch req.Status {
	case "running":
		// Zwischenstand — die Antwort trägt den Cancel-Wunsch zurück zum
		// Agenten (outbound-only-Rückkanal).
		cancel, err2 = s.store.UpdateActionProgress(r.Context(), clusterID, actionID, req.Progress, req.Steps)
	case "succeeded", "failed", "cancelled":
		err2 = s.store.CompleteAction(r.Context(), clusterID, actionID, req.Status, req.Result, req.Steps, req.Revert)
		if err2 == nil && len(req.Snapshot) > 0 {
			_ = s.store.SetActionSnapshot(r.Context(), clusterID, actionID, req.Snapshot)
		}
	default:
		writeErr(w, http.StatusBadRequest, "status must be running, succeeded, failed or cancelled")
		return
	}
	if err2 != nil {
		if errors.Is(err2, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "action not found or not running")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update action")
		return
	}
	// Progress/Endstatus → Streams; leicht gedrosselt (Reports kommen ~1.2s).
	s.broker.Publish(clusterID, "actions", 400*time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "cancel": cancel})
}

// handleCancelAction — POST /api/orgs/{org}/clusters/{cluster}/actions/{action}/cancel
func (s *Server) handleCancelAction(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	actionID, err := uuid.Parse(r.PathValue("action"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid action id")
		return
	}
	if r.URL.Query().Get("force") == "true" {
		// Notausgang: sofort finalisieren, ohne auf den Agenten zu warten —
		// nichts bleibt jemals dauerhaft hängen.
		if err := s.store.ForceCancel(r.Context(), clusterID, actionID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "action not found or already finished")
				return
			}
			writeErr(w, http.StatusInternalServerError, "failed to force-cancel")
			return
		}
		s.broker.Publish(clusterID, "actions", 0)
		// Dem (evtl. noch lebenden) Agenten trotzdem den Abbruch signalisieren.
		s.broker.PublishData(clusterID, "cancel", fmt.Sprintf(`{"actionId":%q}`, actionID))
		writeJSON(w, http.StatusOK, map[string]any{"status": "cancelled", "forced": true})
		return
	}
	status, err := s.store.RequestCancel(r.Context(), clusterID, actionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "action not found or already finished")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to cancel")
		return
	}
	s.broker.Publish(clusterID, "actions", 0)
	if status == "running" {
		// Der Agent führt bereits aus → cancel-Event mit actionId, damit der
		// Rollback SOFORT startet (der Progress-Report-Rückkanal bleibt als
		// Fallback, falls der Stream gerade nicht steht).
		s.broker.PublishData(clusterID, "cancel", fmt.Sprintf(`{"actionId":%q}`, actionID))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}
