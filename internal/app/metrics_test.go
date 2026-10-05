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

	recorder := httptest.NewRecorder()
	app.metricsHandler(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"gitown_http_requests_total 2",
		"gitown_http_responses_total{status_class=\"2xx\"} 1",
		"gitown_http_responses_total{status_class=\"5xx\"} 1",
		"gitown_http_request_duration_seconds_total 2.000000000",
		"gitown_http_requests_in_flight 2",
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
