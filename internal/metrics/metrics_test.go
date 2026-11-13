package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestCollectorCountersAndGauges(t *testing.T) {
	c := NewCollector()
	c.Inc(MetricReconcilesTotal, 1, "result", "available")
	c.Inc(MetricReconcilesTotal, 2, "result", "progressing")
	c.Inc(MetricReconcilesTotal, 1, "result", "available")
	c.Set(MetricReadyReplicas, 3, "deployment", "default/iris-mlp")

	if got := c.Counter(MetricReconcilesTotal, "result", "available"); got != 2 {
		t.Fatalf("available reconciles = %v, want 2", got)
	}
	if got := c.Counter(MetricReconcilesTotal, "result", "progressing"); got != 2 {
		t.Fatalf("progressing reconciles = %v, want 2", got)
	}
	if got := c.Counter(MetricReconcilesTotal, "result", "failed"); got != 0 {
		t.Fatalf("unseen counter = %v, want 0", got)
	}
	if got := c.Gauge(MetricReadyReplicas, "deployment", "default/iris-mlp"); got != 3 {
		t.Fatalf("ready gauge = %v, want 3", got)
	}
	// A gauge overwrite replaces the value; a counter never goes down.
	c.Set(MetricReadyReplicas, 2, "deployment", "default/iris-mlp")
	if got := c.Gauge(MetricReadyReplicas, "deployment", "default/iris-mlp"); got != 2 {
		t.Fatalf("ready gauge after overwrite = %v, want 2", got)
	}
}

func TestCollectorRender(t *testing.T) {
	c := NewCollector()
	c.Inc(MetricRolloutsTotal, 1, "result", "promoted")
	c.Inc(MetricScaleEventsTotal, 1, "direction", "up")
	c.Set(MetricDesiredReplicas, 4, "deployment", "default/iris-mlp")

	out := c.Render()
	for _, want := range []string{
		"# TYPE ml_operator_rollouts_total counter",
		`ml_operator_rollouts_total{result="promoted"} 1`,
		`ml_operator_scale_events_total{direction="up"} 1`,
		`ml_operator_desired_replicas{deployment="default/iris-mlp"} 4`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render() missing %q:\n%s", want, out)
		}
	}
	// An empty collector renders nothing.
	if got := NewCollector().Render(); got != "" {
		t.Fatalf("empty collector Render() = %q, want empty", got)
	}
	// Rendering is deterministic: two renders are identical.
	if second := c.Render(); second != out {
		t.Fatalf("Render() not deterministic:\n%s\nvs\n%s", out, second)
	}
}

func TestRecorderAggregation(t *testing.T) {
	r := NewRecorder(0)
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	r.Record("default/iris-mlp", EventNormal, "ScaledUp", "replicas 2 -> 4")
	r.Record("default/iris-mlp", EventNormal, "ScaledUp", "replicas 2 -> 4")
	r.Record("default/iris-mlp", EventWarning, "RolloutAborted", "canary failed the health gate")

	if r.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (identical events aggregate)", r.Len())
	}
	events := r.List()
	if events[0].Reason != "RolloutAborted" {
		t.Fatalf("newest event = %q, want RolloutAborted", events[0].Reason)
	}
	scaled := events[1]
	if scaled.Count != 2 || !scaled.FirstSeen.Equal(now) || !scaled.LastSeen.Equal(now) {
		t.Fatalf("aggregated event = %+v, want Count 2 with both times at now", scaled)
	}
	if got := r.EventsFor("default/other"); len(got) != 0 {
		t.Fatalf("EventsFor(other) = %d events, want 0", len(got))
	}
}

func TestRecorderCap(t *testing.T) {
	r := NewRecorder(3)
	for i := 0; i < 5; i++ {
		r.Record("default/iris-mlp", EventNormal, "Tick", "tick "+string(rune('a'+i)))
	}
	if r.Len() != 3 {
		t.Fatalf("Len() = %d, want 3 (cap)", r.Len())
	}
	events := r.List()
	if events[0].Message != "tick e" || events[2].Message != "tick c" {
		t.Fatalf("capped events = %v, want newest three (e, d, c)", events)
	}
}
