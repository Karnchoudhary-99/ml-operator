package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
)

func seededCollector() *metrics.Collector {
	c := metrics.NewCollector()
	c.Inc(metrics.MetricReconcilesTotal, 3, "result", "available")
	c.Set(metrics.MetricReadyReplicas, 2, "deployment", "default/iris-mlp")
	return c
}

func TestMetricsEndpointServesExposition(t *testing.T) {
	h := Handler(seededCollector())

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("Content-Type = %q, want Prometheus exposition type", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`# TYPE ml_operator_reconciles_total counter`,
		`ml_operator_reconciles_total{result="available"} 3`,
		`# TYPE ml_operator_ready_replicas gauge`,
		`ml_operator_ready_replicas{deployment="default/iris-mlp"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsEndpointReflectsLiveValues(t *testing.T) {
	c := seededCollector()
	h := Handler(c)

	// A scrape after more reconciles must see the new counts.
	c.Inc(metrics.MetricReconcilesTotal, 1, "result", "degraded")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `ml_operator_reconciles_total{result="degraded"} 1`) {
		t.Errorf("second scrape did not reflect the new counter:\n%s", rec.Body.String())
	}
}

func TestMetricsEndpointRejectsNonGet(t *testing.T) {
	h := Handler(seededCollector())
	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	h := Handler(seededCollector())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok\n" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "ok\n")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	h := Handler(seededCollector())
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
