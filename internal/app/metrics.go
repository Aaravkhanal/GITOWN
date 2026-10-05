package app

import (
	"fmt"
	"net/http"
)

// metricsHandler exposes only process-local aggregate API metrics. It avoids
// paths, usernames, repository names, and other unbounded or sensitive labels.
func (a *App) metricsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "# HELP gitown_http_requests_total Total API requests handled by this process.")
	fmt.Fprintln(w, "# TYPE gitown_http_requests_total counter")
	fmt.Fprintf(w, "gitown_http_requests_total %d\n", a.metrics.requests.Load())
	fmt.Fprintln(w, "# HELP gitown_http_responses_total API responses by fixed HTTP status class.")
	fmt.Fprintln(w, "# TYPE gitown_http_responses_total counter")
	for class := 1; class <= 5; class++ {
		fmt.Fprintf(w, "gitown_http_responses_total{status_class=\"%dxx\"} %d\n", class, a.metrics.status[class].Load())
	}
	fmt.Fprintln(w, "# HELP gitown_http_request_duration_seconds_total Cumulative API request duration in seconds.")
	fmt.Fprintln(w, "# TYPE gitown_http_request_duration_seconds_total counter")
	fmt.Fprintf(w, "gitown_http_request_duration_seconds_total %.9f\n", float64(a.metrics.duration.Load())/1e9)
	fmt.Fprintln(w, "# HELP gitown_http_requests_in_flight Current API requests being handled by this process.")
	fmt.Fprintln(w, "# TYPE gitown_http_requests_in_flight gauge")
	fmt.Fprintf(w, "gitown_http_requests_in_flight %d\n", a.metrics.inFlight.Load())
}
