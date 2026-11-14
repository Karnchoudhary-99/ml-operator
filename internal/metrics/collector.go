package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Collector holds the operator's Prometheus-style metrics: counters
// (monotonically increasing, labelled) and gauges (current values,
// labelled). It is safe for concurrent use and renders as Prometheus
// text exposition, so a scrape endpoint can serve Render() verbatim.
type Collector struct {
	mu       sync.Mutex
	counters map[string]map[string]float64 // metric -> labels key -> value
	gauges   map[string]map[string]float64
}

// NewCollector returns an empty collector.
func NewCollector() *Collector {
	return &Collector{
		counters: make(map[string]map[string]float64),
		gauges:   make(map[string]map[string]float64),
	}
}

// Inc adds delta to the counter named metric with the given labels.
// Labels are key=value pairs, e.g. "result", "available".
func (c *Collector) Inc(metric string, delta float64, labels ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := labelsKey(labels)
	if c.counters[metric] == nil {
		c.counters[metric] = make(map[string]float64)
	}
	c.counters[metric][key] += delta
}

// Set records the current value of the gauge named metric with the
// given labels, overwriting any previous value.
func (c *Collector) Set(metric string, value float64, labels ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := labelsKey(labels)
	if c.gauges[metric] == nil {
		c.gauges[metric] = make(map[string]float64)
	}
	c.gauges[metric][key] = value
}

// Counter returns the current value of a counter, or 0 when unseen.
func (c *Collector) Counter(metric string, labels ...string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counters[metric][labelsKey(labels)]
}

// Gauge returns the current value of a gauge, or 0 when unseen.
func (c *Collector) Gauge(metric string, labels ...string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gauges[metric][labelsKey(labels)]
}

// Metric names the operator exports.
const (
	// MetricReconcilesTotal counts reconciles by outcome
	// (label "result": "available", "progressing", "degraded",
	// "failed", "error").
	MetricReconcilesTotal = "ml_operator_reconciles_total"
	// MetricRolloutsTotal counts finished canary rollouts by
	// outcome (label "result": "promoted", "aborted").
	MetricRolloutsTotal = "ml_operator_rollouts_total"
	// MetricScaleEventsTotal counts autoscaling decisions by
	// direction (label "direction": "up", "down", "held").
	MetricScaleEventsTotal = "ml_operator_scale_events_total"
	// MetricApplyRetriesTotal counts cluster operations that
	// needed retries, by outcome (label "outcome": "recovered"
	// — the retry succeeded — or "exhausted" — every attempt
	// failed and the reconcile degraded).
	MetricApplyRetriesTotal = "ml_operator_apply_retries_total"
	// MetricReadyReplicas is the current ready replica count per
	// deployment (label "deployment": "namespace/name").
	MetricReadyReplicas = "ml_operator_ready_replicas"
	// MetricDesiredReplicas is the current desired replica count
	// per deployment (label "deployment": "namespace/name").
	MetricDesiredReplicas = "ml_operator_desired_replicas"
)

// Render returns the metrics in Prometheus text exposition format:
// "# HELP" / "# TYPE" lines, then one `name{labels} value` line per
// series, sorted for deterministic output. Unknown or NaN values
// are never emitted — Set/Inc only store finite numbers.
func (c *Collector) Render() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	write := func(name, typ string, series map[string]float64) {
		if len(series) == 0 {
			return
		}
		fmt.Fprintf(&b, "# HELP %s operator metric\n# TYPE %s %s\n", name, name, typ)
		keys := make([]string, 0, len(series))
		for k := range series {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s%s %v\n", name, renderLabels(k), series[k])
		}
	}
	write(MetricReconcilesTotal, "counter", c.counters[MetricReconcilesTotal])
	write(MetricRolloutsTotal, "counter", c.counters[MetricRolloutsTotal])
	write(MetricScaleEventsTotal, "counter", c.counters[MetricScaleEventsTotal])
	write(MetricApplyRetriesTotal, "counter", c.counters[MetricApplyRetriesTotal])
	write(MetricReadyReplicas, "gauge", c.gauges[MetricReadyReplicas])
	write(MetricDesiredReplicas, "gauge", c.gauges[MetricDesiredReplicas])
	return b.String()
}

// labelsKey packs alternating key/value label pairs into one
// sortable string; it must be called with an even number of
// arguments. The key is only used as a map key — Render re-expands
// it into Prometheus label syntax.
func labelsKey(labels []string) string {
	var parts []string
	for i := 0; i+1 < len(labels); i += 2 {
		parts = append(parts, labels[i]+"="+labels[i+1])
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// renderLabels expands a labelsKey back into `{k="v",k="v"}` syntax.
func renderLabels(key string) string {
	if key == "" {
		return ""
	}
	var parts []string
	for _, kv := range strings.Split(key, ",") {
		k, v, _ := strings.Cut(kv, "=")
		parts = append(parts, fmt.Sprintf(`%s=%q`, k, v))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
