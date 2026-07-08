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

// escalation.go — der Incident-Escalator: scannt fällige offene Incidents und
// feuert den nächsten Notification-Schritt ihrer Eskalations-Policy über die
// bestehenden Alert-Provider. Läuft wie der Evaluator unter dem Leader-Lock, in
// kurzem Intervall (der erste Schritt eines frisch deklarierten Incidents soll
// zeitnah paged werden). Acknowledge/mitigate/resolve stoppt die Kette (die
// Query filtert status='open' und der Status-Handler nullt next_escalation_at).

const escalationInterval = 15 * time.Second

type Escalator struct {
	store  *store.Store
	broker *events.Broker
	http   *http.Client
}

func NewEscalator(st *store.Store, broker *events.Broker) *Escalator {
	return &Escalator{store: st, broker: broker, http: &http.Client{Timeout: 10 * time.Second}}
}

// Run tickt bis ctx endet.
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

// Process feuert alle bis `now` fälligen Eskalationsschritte und gibt die Anzahl
// zurück. Öffentlich, damit Tests deterministisch einen Tick auslösen können.
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
		// Kette erschöpft: nur next_escalation_at leeren, kein Event.
		if d.Step >= len(steps) {
			_ = e.store.AdvanceEscalation(ctx, d.IncidentID, d.Step, nil, d.Step, nil)
			continue
		}
		step := steps[d.Step]
		provs, err := e.store.ProvidersByIDs(ctx, step.ProviderIDs)
		if err != nil {
			log.Printf("alerts: escalation providers: %v", err)
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
		names := make([]string, 0, len(provs))
		for i := range provs {
			if err := SendNotification(ctx, e.http, &provs[i], p); err != nil {
				log.Printf("alerts: escalate notify %s (%s): %v", provs[i].Name, provs[i].Type, err)
			}
			names = append(names, provs[i].Name)
		}
		// Nächste Fälligkeit = jetzt + afterMinutes des Folgeschritts (oder Ende).
		var next *time.Time
		if d.Step+1 < len(steps) {
			n := now.Add(time.Duration(steps[d.Step+1].AfterMinutes) * time.Minute)
			next = &n
		}
		if err := e.store.AdvanceEscalation(ctx, d.IncidentID, d.Step+1, next, d.Step, names); err != nil {
			log.Printf("alerts: advance escalation %s: %v", d.IncidentID, err)
			continue
		}
		e.broker.Publish(d.ClusterID, "incidents", 0)
		fired++
	}
	return fired
}

func incidentRuleLabel(number int, title string) string {
	return "INC-" + strconv.Itoa(number) + " " + title
}

func incidentEscalationMessage(number int, title, severity string, step int) string {
	return "[ESCALATION] INC-" + strconv.Itoa(number) + " " + title + " (" + severity + ") — step " +
		strconv.Itoa(step+1) + ", still unacknowledged"
}
