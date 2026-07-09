package alerts

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/events"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// escalation.go — the incident escalator: scans due open incidents and fires the
// next notification step of their escalation policy through the existing alert
// providers. Runs like the evaluator under the leader lock, on a short interval
// (the first step of a freshly declared incident should be paged promptly).
// Acknowledge/mitigate/resolve stops the chain (the query filters status='open'
// and the status handler nulls next_escalation_at).

const escalationInterval = 15 * time.Second

type Escalator struct {
	store  *store.Store
	broker *events.Broker
	http   *http.Client
}

func NewEscalator(st *store.Store, broker *events.Broker) *Escalator {
	return &Escalator{store: st, broker: broker, http: &http.Client{Timeout: 10 * time.Second}}
}

// Run ticks until ctx ends.
func (e *Escalator) Run(ctx context.Context) {
	log.Printf("alerts: escalator started (every %s)", escalationInterval)
	t := time.NewTicker(escalationInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n := e.Process(ctx, time.Now().UTC()); n > 0 {
				log.Printf("alerts: escalated %d incident step(s)", n)
			}
		}
	}
}

// Process fires all escalation steps due by `now` and returns the count. Public
// so that tests can trigger a tick deterministically.
func (e *Escalator) Process(ctx context.Context, now time.Time) int {
	due, err := e.store.DueEscalationIncidents(ctx, now)
	if err != nil {
		log.Printf("alerts: due escalations: %v", err)
		return 0
	}
	fired := 0
	for _, d := range due {
		steps, err := e.store.EscalationPolicyStepsByID(ctx, d.PolicyID)
		if err != nil {
			log.Printf("alerts: escalation policy %s: %v", d.PolicyID, err)
			continue
		}
		// Chain exhausted: just clear next_escalation_at (guarded, no event).
		if d.Step >= len(steps) {
			_, _ = e.store.AdvanceEscalation(ctx, d.IncidentID, d.Step, d.Step, nil, now, nil)
			continue
		}
		// Resolve providers + names FIRST, then claim the step ATOMICALLY, then
		// notify. This way an intervening acknowledge prevents the page (the
		// claim fails) and a delivery error does not re-fire the step (the state
		// has already been advanced).
		step := steps[d.Step]
		provs, err := e.store.ProvidersByIDs(ctx, step.ProviderIDs)
		if err != nil {
			log.Printf("alerts: escalation providers: %v", err)
		}
		names := make([]string, 0, len(provs))
		for i := range provs {
			names = append(names, provs[i].Name)
		}
		var next *time.Time
		if d.Step+1 < len(steps) {
			n := now.Add(clampMinutes(steps[d.Step+1].AfterMinutes))
			next = &n
		}
		claimed, err := e.store.AdvanceEscalation(ctx, d.IncidentID, d.Step, d.Step+1, next, now, names)
		if err != nil {
			log.Printf("alerts: advance escalation %s: %v", d.IncidentID, err)
			continue
		}
		if !claimed {
			continue // acknowledged/resolved or another tick beat us to it
		}
		p := &Payload{
			Rule:      incidentRuleLabel(d.Number, d.Title),
			State:     "firing",
			Severity:  d.Severity,
			Kind:      "incident",
			ClusterID: d.ClusterID.String(),
			At:        now,
			Message:   incidentEscalationMessage(d.Number, d.Title, d.Severity, d.Step),
		}
		for i := range provs {
			if err := SendNotification(ctx, e.http, &provs[i], p); err != nil {
				log.Printf("alerts: escalate notify %s (%s): %v", provs[i].Name, provs[i].Type, err)
			}
		}
		e.broker.Publish(d.ClusterID, "incidents", 0)
		fired++
	}
	return fired
}

// clampMinutes defensively bounds afterMinutes to [0, 7 days] so that the
// time.Duration multiplication never overflows (int minutes * 60e9 ns).
func clampMinutes(m int) time.Duration {
	if m < 0 {
		m = 0
	}
	if m > 7*24*60 {
		m = 7 * 24 * 60
	}
	return time.Duration(m) * time.Minute
}

func incidentRuleLabel(number int, title string) string {
	return "INC-" + strconv.Itoa(number) + " " + title
}

func incidentEscalationMessage(number int, title, severity string, step int) string {
	return "[ESCALATION] INC-" + strconv.Itoa(number) + " " + title + " (" + severity + ") — step " +
		strconv.Itoa(step+1) + ", still unacknowledged"
}
