package app

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsHandlerReportsAggregateCounters(t *testing.T) {
	app := &App{}
	app.metrics.inFlight.Store(2)
	app.metrics.record(201, 1500*time.Millisecond)
	app.metrics.record(503, 500*time.Millisecond)
	app.workers.mailAttempts.Store(4)
	app.workers.recoveredLeases.Store(2)
	app.workers.routeExpiryFail.Store(1)

	recorder := httptest.NewRecorder()
	app.metricsHandler(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"gitown_http_requests_total 2",
		"gitown_http_responses_total{status_class=\"2xx\"} 1",
		"gitown_http_responses_total{status_class=\"5xx\"} 1",
		"gitown_http_request_duration_seconds_total 2.000000000",
		"gitown_http_requests_in_flight 2",
		"gitown_worker_attempts_total{queue=\"email\",outcome=\"attempted\"} 4",
		"gitown_worker_recovered_claims_total 2",
		"gitown_queue_metrics_available 0",
		"gitown_worker_errors_total{worker=\"route_expiry\"} 1",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics output missing %q", expected)
		}
	}
	if recorder.Code != 200 || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("unexpected response: status=%d content-type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	if strings.Contains(body, "username") || strings.Contains(body, "repository") || strings.Contains(body, "/api/v1/") {
		t.Fatalf("metrics contain high-cardinality or private labels: %s", body)
	}
}
