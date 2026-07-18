package sync

// inventory.go — das VOLLSTÄNDIGE Kubernetes-Inventar für die Resources-Seite
// und Konsumenten wie das MCP-Interface: der Agent listet periodisch alle
// relevanten Ressourcen-Kinds und pusht eine KOMPAKTE Zusammenfassung (nie
// Secret-DATEN — nur Metadaten). Generisches Item-Format {namespace, name,
// createdAt, info{k:v}}, damit CP/UI ohne Schema-Wissen rendern können.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const inventoryInterval = 60 * time.Second

type invItem struct {
	Namespace string            `json:"namespace,omitempty"`
	Name      string            `json:"name"`
	CreatedAt time.Time         `json:"createdAt"`
	Info      map[string]string `json:"info"`
}

// RunInventory pusht das Inventar alle 60s (und einmal sofort).
func (s *Syncer) RunInventory(ctx context.Context) error {
	t := time.NewTicker(inventoryInterval)
	defer t.Stop()
	s.pushInventory(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.pushInventory(ctx)
		}
	}
}

func (s *Syncer) pushInventory(ctx context.Context) {
	kinds := map[string][]invItem{}
	collect := func(kind string, fn func(context.Context) ([]invItem, error)) {
		items, err := fn(ctx)
		if err != nil {
			// fehlende RBAC-Rechte o.ä. — Kind auslassen, Rest trotzdem pushen
			log.Printf("inventory: %s: %v", kind, err)
			return
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Namespace != items[j].Namespace {
				return items[i].Namespace < items[j].Namespace
			}
			return items[i].Name < items[j].Name
		})
		if len(items) > 500 {
			items = items[:500]
		}
		kinds[kind] = items
	}

	collect("Service", s.invServices)
	collect("Ingress", s.invIngresses)
	collect("ConfigMap", s.invConfigMaps)
	collect("Secret", s.invSecrets)
	collect("Job", s.invJobs)
	collect("CronJob", s.invCronJobs)
	collect("HorizontalPodAutoscaler", s.invHPAs)
	collect("PodDisruptionBudget", s.invPDBs)
	collect("NetworkPolicy", s.invNetPols)
	collect("ServiceAccount", s.invServiceAccounts)
	collect("ResourceQuota", s.invQuotas)
	collect("LimitRange", s.invLimitRanges)
	collect("PersistentVolume", s.invPVs)

	if err := s.postJSON(ctx, "/api/agent/inventory", map[string]any{"kinds": kinds}); err != nil {
		log.Printf("inventory: push failed: %v", err)
	}
}

/* ── Collectors (kompakte Zusammenfassungen) ─────────────────────────────── */

func (s *Syncer) invServices(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		ports := make([]string, 0, len(o.Spec.Ports))
		for _, p := range o.Spec.Ports {
			tp := p.TargetPort.String()
			if tp == "0" || tp == "" {
				tp = fmt.Sprint(p.Port)
			}
			ports = append(ports, fmt.Sprintf("%d→%s/%s", p.Port, tp, p.Protocol))
		}
		info := map[string]string{
			"type":      string(o.Spec.Type),
			"clusterIP": o.Spec.ClusterIP,
			"ports":     strings.Join(ports, ", "),
		}
		if len(o.Spec.Selector) > 0 {
			sel := make([]string, 0, len(o.Spec.Selector))
			for k, v := range o.Spec.Selector {
				sel = append(sel, k+"="+v)
			}
			sort.Strings(sel)
			info["selector"] = strings.Join(sel, ",")
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, info})
	}
	return out, nil
}

func (s *Syncer) invIngresses(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		hosts := []string{}
		paths := 0
		for _, r := range o.Spec.Rules {
			if r.Host != "" {
				hosts = append(hosts, r.Host)
			}
			if r.HTTP != nil {
				paths += len(r.HTTP.Paths)
			}
		}
		cls := ""
		if o.Spec.IngressClassName != nil {
			cls = *o.Spec.IngressClassName
		}
		lb := []string{}
		for _, ing := range o.Status.LoadBalancer.Ingress {
			if ing.IP != "" {
				lb = append(lb, ing.IP)
			} else if ing.Hostname != "" {
				lb = append(lb, ing.Hostname)
			}
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"class":   cls,
			"hosts":   strings.Join(hosts, ", "),
			"paths":   fmt.Sprint(paths),
			"tls":     fmt.Sprint(len(o.Spec.TLS) > 0),
			"address": strings.Join(lb, ", "),
		}})
	}
	return out, nil
}

func (s *Syncer) invConfigMaps(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		keys := make([]string, 0, len(o.Data))
		for k := range o.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		shown := strings.Join(keys, ", ")
		if len(shown) > 160 {
			shown = shown[:160] + "…"
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"keys": fmt.Sprint(len(o.Data)), "names": shown,
		}})
	}
	return out, nil
}

// invSecrets: NUR Metadaten (Typ + Key-Anzahl) — niemals Werte.
func (s *Syncer) invSecrets(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.CoreV1().Secrets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"type": string(o.Type), "keys": fmt.Sprint(len(o.Data)),
		}})
	}
	return out, nil
}

func (s *Syncer) invJobs(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		status := "Active"
		for _, c := range o.Status.Conditions {
			if c.Status != corev1.ConditionTrue {
				continue
			}
			if string(c.Type) == "Complete" {
				status = "Complete"
			}
			if string(c.Type) == "Failed" {
				status = "Failed"
			}
		}
		dur := ""
		if o.Status.StartTime != nil {
			end := time.Now()
			if o.Status.CompletionTime != nil {
				end = o.Status.CompletionTime.Time
			}
			dur = end.Sub(o.Status.StartTime.Time).Round(time.Second).String()
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"status": status, "succeeded": fmt.Sprint(o.Status.Succeeded), "failed": fmt.Sprint(o.Status.Failed), "duration": dur,
		}})
	}
	return out, nil
}

func (s *Syncer) invCronJobs(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		last := ""
		if o.Status.LastScheduleTime != nil {
			last = o.Status.LastScheduleTime.Format(time.RFC3339)
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"schedule": o.Spec.Schedule,
			"suspend":  fmt.Sprint(o.Spec.Suspend != nil && *o.Spec.Suspend),
			"active":   fmt.Sprint(len(o.Status.Active)),
			"last run": last,
		}})
	}
	return out, nil
}

func (s *Syncer) invHPAs(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.AutoscalingV2().HorizontalPodAutoscalers("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		min := int32(1)
		if o.Spec.MinReplicas != nil {
			min = *o.Spec.MinReplicas
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"target":  o.Spec.ScaleTargetRef.Kind + "/" + o.Spec.ScaleTargetRef.Name,
			"bounds":  fmt.Sprintf("%d–%d", min, o.Spec.MaxReplicas),
			"current": fmt.Sprint(o.Status.CurrentReplicas),
			"desired": fmt.Sprint(o.Status.DesiredReplicas),
		}})
	}
	return out, nil
}

func (s *Syncer) invPDBs(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.PolicyV1().PodDisruptionBudgets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		rule := ""
		if o.Spec.MinAvailable != nil {
			rule = "minAvailable=" + o.Spec.MinAvailable.String()
		}
		if o.Spec.MaxUnavailable != nil {
			rule = "maxUnavailable=" + o.Spec.MaxUnavailable.String()
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"rule":    rule,
			"healthy": fmt.Sprintf("%d/%d", o.Status.CurrentHealthy, o.Status.DesiredHealthy),
			"allowed": fmt.Sprint(o.Status.DisruptionsAllowed),
		}})
	}
	return out, nil
}

func (s *Syncer) invNetPols(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.NetworkingV1().NetworkPolicies("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		sel := metav1.FormatLabelSelector(&o.Spec.PodSelector)
		if sel == "<none>" {
			sel = "all pods"
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"selects": sel,
			"ingress": fmt.Sprint(len(o.Spec.Ingress)),
			"egress":  fmt.Sprint(len(o.Spec.Egress)),
		}})
	}
	return out, nil
}

func (s *Syncer) invServiceAccounts(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.CoreV1().ServiceAccounts("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"secrets": fmt.Sprint(len(o.Secrets)),
		}})
	}
	return out, nil
}

func (s *Syncer) invQuotas(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.CoreV1().ResourceQuotas("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		parts := []string{}
		for name, hard := range o.Status.Hard {
			used := o.Status.Used[name]
			parts = append(parts, fmt.Sprintf("%s %s/%s", name, used.String(), hard.String()))
		}
		sort.Strings(parts)
		usage := strings.Join(parts, " · ")
		if len(usage) > 180 {
			usage = usage[:180] + "…"
		}
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{"usage": usage}})
	}
	return out, nil
}

func (s *Syncer) invLimitRanges(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.CoreV1().LimitRanges("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		out = append(out, invItem{o.Namespace, o.Name, o.CreationTimestamp.Time, map[string]string{
			"limits": fmt.Sprint(len(o.Spec.Limits)),
		}})
	}
	return out, nil
}

func (s *Syncer) invPVs(ctx context.Context) ([]invItem, error) {
	list, err := s.clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]invItem, 0, len(list.Items))
	for i := range list.Items {
		o := &list.Items[i]
		cap := ""
		if v, ok := o.Spec.Capacity["storage"]; ok {
			cap = v.String()
		}
		claim := ""
		if o.Spec.ClaimRef != nil {
			claim = o.Spec.ClaimRef.Namespace + "/" + o.Spec.ClaimRef.Name
		}
		out = append(out, invItem{"", o.Name, o.CreationTimestamp.Time, map[string]string{
			"phase":    string(o.Status.Phase),
			"capacity": cap,
			"claim":    claim,
			"class":    o.Spec.StorageClassName,
			"reclaim":  string(o.Spec.PersistentVolumeReclaimPolicy),
		}})
	}
	return out, nil
}
