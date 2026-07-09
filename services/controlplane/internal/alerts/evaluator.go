// Package alerts is the control plane's check evaluator: every 15s the active
// rules are evaluated against ClickHouse and driven through the state machine
// ok → pending (for-duration) → firing → resolved. Transitions produce
// events, fire notifications to the providers and an SSE signal — the UI
// stays current without a reload.
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/prometheus/promql"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/events"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/promqlx"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/telemetry"
)

const evalInterval = 15 * time.Second

type Evaluator struct {
	store  *store.Store
	tele   *telemetry.Store
	broker *events.Broker
	promql *promqlx.Engine
	http   *http.Client
}

func New(st *store.Store, tele *telemetry.Store, broker *events.Broker, eng *promqlx.Engine) *Evaluator {
	return &Evaluator{store: st, tele: tele, broker: broker, promql: eng, http: &http.Client{Timeout: 10 * time.Second}}
}

// Run evaluates until ctx ends.
func (e *Evaluator) Run(ctx context.Context) {
	log.Printf("alerts: evaluator started (every %s)", evalInterval)
	t := time.NewTicker(evalInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.evalAll(ctx)
		}
	}
}

func (e *Evaluator) evalAll(ctx context.Context) {
	rules, err := e.store.ListEnabledRulesAll(ctx)
	if err != nil {
		log.Printf("alerts: list rules: %v", err)
		return
	}
	for i := range rules {
		e.evalRule(ctx, &rules[i])
	}
}

func breach(op string, value, threshold float64) bool {
	if op == "lt" {
		return value < threshold
	}
	return value > threshold
}

func (e *Evaluator) evalRule(ctx context.Context, r *model.AlertRule) {
	var params map[string]string
	_ = json.Unmarshal(r.Params, &params)
	if params == nil {
		params = map[string]string{}
	}
	window := time.Duration(r.WindowSeconds) * time.Second
	var value float64
	var err error
	if r.Kind == "promql" {
		value, err = e.evalPromQLMax(ctx, r.ClusterID, r.Query)
	} else if r.Kind == "derived" {
		// Custom metric as alert source: load definition + window scalar.
		defID, perr := uuid.Parse(params["definitionId"])
		if perr != nil {
			err = fmt.Errorf("derived rule needs params.definitionId")
		} else {
			var def *model.MetricDefinition
			def, err = e.store.GetMetricDefinition(ctx, r.ClusterID, defID)
			if err == nil && def.Source == "promql" {
				// PromQL metric: instant evaluation, alert value = max across series.
				value, err = e.evalPromQLMax(ctx, r.ClusterID, def.Query)
			} else if err == nil {
				value, err = e.tele.EvalDerivedScalar(ctx, r.ClusterID, telemetry.DerivedDef{
					Source: def.Source, Namespace: def.Namespace, Workload: def.Workload,
					Search: def.Search, ValueMode: def.ValueMode, Pattern: def.Pattern, Agg: def.Agg,
				}, window)
			}
		}
	} else {
		value, err = e.tele.EvalScalar(ctx, r.ClusterID, r.Kind, params, window)
	}
	if err != nil {
		_ = e.store.UpdateRuleEval(ctx, r.ID, r.State, r.StateSince, r.LastValue, err.Error())
		return
	}

	// Value history: the rule cards show as a sparkline EXACTLY the values
	// the evaluator saw (Scope 'alert', Name = rule ID).
	_ = e.tele.InsertInfraPoints(ctx, r.ClusterID, []telemetry.InfraPoint{
		{Scope: "alert", Name: r.ID.String(), Metric: "value", Value: value},
	})

	now := time.Now().UTC()
	newState := r.State
	isBreach := breach(r.Op, value, r.Threshold)
	switch r.State {
	case "ok":
		if isBreach {
			newState = "pending"
			if r.ForSeconds <= 0 {
				newState = "firing"
			}
		}
	case "pending":
		if !isBreach {
			newState = "ok"
		} else if now.Sub(r.StateSince) >= time.Duration(r.ForSeconds)*time.Second {
			newState = "firing"
		}
	case "firing":
		if !isBreach {
			newState = "ok"
		}
	}

	stateSince := r.StateSince
	if newState != r.State {
		stateSince = now
		// Maintenance windows suppress notifications AND auto-incident declaration
		// for the matching scope (still tracks state + writes events).
		maint, _ := e.store.InMaintenance(ctx, r.ClusterID, params["namespace"], now)
		msg := fmt.Sprintf("%s: value %.2f %s threshold %.2f (window %ds)", r.Name, value, opWord(r.Op), r.Threshold, r.WindowSeconds)
		if maint {
			msg += " [maintenance: suppressed]"
		}
		evID, _ := e.store.InsertAlertEvent(ctx, r.ID, r.ClusterID, r.State, newState, value, msg)
		var evPtr *uuid.UUID
		if evID != uuid.Nil {
			evPtr = &evID
		}
		// Snooze: the state machine + events keep running, notifications pause.
		muted := r.MutedUntil != nil && r.MutedUntil.After(now)
		if newState == "firing" {
			if !muted && !maint {
				e.notify(ctx, r, "firing", value)
			}
			// AUTO-INCIDENT: firing declares (or deduplicates) an incident —
			// the bracket that investigations and actions attach to. Skipped while
			// the scope is under an active maintenance window.
			if !maint {
				if _, _, err := e.store.DeclareIncidentFromAlert(ctx, r.ID, r.ClusterID, r.Name, r.Severity, value, evPtr); err != nil {
					log.Printf("alerts: declare incident for %s: %v", r.Name, err)
				} else {
					e.broker.Publish(r.ClusterID, "incidents", 0)
				}
			}
			// AUTO-REMEDIATION: firing dispatches the configured workflow —
			// the agent runs it as a verified pipeline. Suppressed in maintenance.
			if r.ActionDefinitionID != nil && !maint {
				e.dispatchRemediation(ctx, r, value)
			}
		} else if r.State == "firing" && newState == "ok" {
			if !muted && !maint {
				e.notify(ctx, r, "resolved", value)
			}
			// The auto-incident closes together with the cleared alert.
			if _, err := e.store.ResolveIncidentFromAlert(ctx, r.ID, r.ClusterID, r.Name, evPtr); err != nil {
				log.Printf("alerts: resolve incident for %s: %v", r.Name, err)
			} else {
				e.broker.Publish(r.ClusterID, "incidents", 0)
			}
		}
		e.broker.Publish(r.ClusterID, "alerts", 0)
	}
	_ = e.store.UpdateRuleEval(ctx, r.ID, newState, stateSince, value, "")
}

func opWord(op string) string {
	if op == "lt" {
		return "below"
	}
	return "above"
}

/* ── Notifier ───────────────────────────────────────────────────────────── */

// Payload is the webhook format (and the data basis for Slack/email text).
type Payload struct {
	Rule      string    `json:"rule"`
	State     string    `json:"state"` // firing | resolved | test
	Severity  string    `json:"severity"`
	Value     float64   `json:"value"`
	Threshold float64   `json:"threshold"`
	Kind      string    `json:"kind"`
	ClusterID string    `json:"clusterId"`
	At        time.Time `json:"at"`
	Message   string    `json:"message"`
}

func (e *Evaluator) notify(ctx context.Context, r *model.AlertRule, state string, value float64) {
	provs, err := e.store.ProvidersByIDs(ctx, r.ProviderIDs)
	if err != nil {
		log.Printf("alerts: load providers: %v", err)
		return
	}
	p := Payload{
		Rule: r.Name, State: state, Severity: r.Severity, Value: value,
		Threshold: r.Threshold, Kind: r.Kind, ClusterID: r.ClusterID.String(),
		At:      time.Now().UTC(),
		Message: fmt.Sprintf("[%s] %s is %s — value %.2f %s %.2f", strings.ToUpper(state), r.Name, state, value, opWord(r.Op), r.Threshold),
	}
	for i := range provs {
		if err := SendNotification(ctx, e.http, &provs[i], &p); err != nil {
			log.Printf("alerts: notify %s (%s): %v", provs[i].Name, provs[i].Type, err)
		}
	}
}

// SendNotification sends a payload through a provider — also used by the
// "send test" endpoint.
func SendNotification(ctx context.Context, client *http.Client, prov *model.AlertProvider, p *Payload) error {
	var cfg map[string]string
	_ = json.Unmarshal(prov.Config, &cfg)
	switch prov.Type {
	case "webhook":
		body, _ := json.Marshal(p)
		return postJSON(ctx, client, cfg["url"], body, cfg["authorization"])
	case "slack":
		icon := "🔔"
		if p.State == "firing" {
			icon = "🔴"
		} else if p.State == "resolved" {
			icon = "🟢"
		}
		body, _ := json.Marshal(map[string]string{
			"text": fmt.Sprintf("%s *%s* — %s\nvalue `%.2f` threshold `%.2f` (%s, %s)", icon, p.Rule, p.State, p.Value, p.Threshold, p.Kind, p.Severity),
		})
		return postJSON(ctx, client, cfg["url"], body, "")
	case "email":
		return sendMail(cfg, p)
	default:
		return fmt.Errorf("unknown provider type %q", prov.Type)
	}
}

func postJSON(ctx context.Context, client *http.Client, url string, body []byte, auth string) error {
	if url == "" {
		return fmt.Errorf("provider has no url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned %d", resp.StatusCode)
	}
	return nil
}

func sendMail(cfg map[string]string, p *Payload) error {
	host, port, from, to := cfg["host"], cfg["port"], cfg["from"], cfg["to"]
	if host == "" || from == "" || to == "" {
		return fmt.Errorf("email provider needs host, from, to")
	}
	if port == "" {
		port = "587"
	}
	subject := fmt.Sprintf("[%s] %s — %s", strings.ToUpper(p.State), p.Rule, p.Severity)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n", from, to, subject, p.Message)
	var auth smtp.Auth
	if cfg["user"] != "" {
		auth = smtp.PlainAuth("", cfg["user"], cfg["password"], host)
	}
	return smtp.SendMail(host+":"+port, auth, from, strings.Split(to, ","), []byte(msg))
}

// evalPromQLMax: instant query, value = maximum across all result series.
func (e *Evaluator) evalPromQLMax(ctx context.Context, clusterID uuid.UUID, query string) (float64, error) {
	res, closeFn, err := e.promql.QueryInstant(ctx, clusterID, query, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	defer closeFn()
	max := 0.0
	switch v := res.Value.(type) {
	case promql.Vector:
		for i, s := range v {
			if i == 0 || s.F > max {
				max = s.F
			}
		}
	case promql.Scalar:
		max = v.V
	}
	return max, nil
}

// dispatchRemediation creates the auto-remediation action (source snapshot
// like a manual dispatch — the audit shows exactly what ran).
func (e *Evaluator) dispatchRemediation(ctx context.Context, r *model.AlertRule, value float64) {
	def, err := e.store.GetActionDefinitionForCluster(ctx, r.ClusterID, *r.ActionDefinitionID)
	if err != nil {
		log.Printf("alerts: remediation def for %s: %v", r.Name, err)
		return
	}
	// Merge the definition's defaults — rule args override only explicitly
	// set values, missing ones fall back to the definition default.
	args := map[string]string{}
	var dparams []struct {
		Name    string `json:"name"`
		Default string `json:"default"`
	}
	_ = json.Unmarshal(def.Params, &dparams)
	for _, p := range dparams {
		if p.Default != "" {
			args[p.Name] = p.Default
		}
	}
	overrides := map[string]string{}
	_ = json.Unmarshal(r.ActionArgs, &overrides)
	for k, v := range overrides {
		if v != "" {
			args[k] = v
		}
	}
	params, _ := json.Marshal(map[string]any{
		"definitionId":   def.ID,
		"source":         def.Source,
		"args":           args,
		"timeoutSeconds": def.TimeoutSeconds,
	})
	if _, err := e.store.CreateSystemAction(ctx, r.ClusterID, "script", "-", "Script", def.Name, params); err != nil {
		log.Printf("alerts: dispatch remediation %s: %v", def.Name, err)
		return
	}
	_, _ = e.store.InsertAlertEvent(ctx, r.ID, r.ClusterID, "firing", "firing", value,
		fmt.Sprintf("auto-remediation dispatched: workflow %q", def.Name))
	e.broker.Publish(r.ClusterID, "actions", 0)
	log.Printf("alerts: %s firing → dispatched workflow %q", r.Name, def.Name)
}
