package store

import "testing"

// A completed Job has zero ready pods forever. Reporting that as "critical"
// turns every CronJob run into a permanent fake outage.
func TestDeriveHealthJobs(t *testing.T) {
	cases := []struct {
		name                                      string
		kind                                      string
		ready, total, restarts, succeeded, failed int
		want                                      string
	}{
		{"completed job", "Job", 0, 1, 0, 1, 0, "healthy"},
		{"running job", "Job", 1, 1, 0, 0, 0, "healthy"},
		{"failed job", "Job", 0, 1, 0, 0, 1, "critical"},
		{"failed after a success", "Job", 0, 2, 0, 1, 1, "critical"},
		{"job with no pods yet", "Job", 0, 1, 0, 0, 0, "unknown"},
		{"deployment down", "Deployment", 0, 2, 0, 0, 0, "critical"},
		{"deployment partial", "Deployment", 1, 2, 0, 0, 0, "degraded"},
		{"deployment flapping", "Deployment", 2, 2, 7, 0, 0, "degraded"},
		{"deployment healthy", "Deployment", 2, 2, 0, 0, 0, "healthy"},
		{"scaled to zero", "Deployment", 0, 0, 0, 0, 0, "unknown"},
	}
	for _, c := range cases {
		got := deriveHealth(c.kind, c.ready, c.total, c.restarts, c.succeeded, c.failed)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
