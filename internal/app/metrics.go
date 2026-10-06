package app

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type workerMetrics struct {
	mailAttempts      atomic.Uint64
	mailSuccess       atomic.Uint64
	mailFailures      atomic.Uint64
	webhookAttempts   atomic.Uint64
	webhookSuccess    atomic.Uint64
	webhookFailures   atomic.Uint64
	recoveredLeases   atomic.Uint64
	routeScheduleRuns atomic.Uint64
	routeScheduleFail atomic.Uint64
	maintenanceFail   atomic.Uint64
	authCleanupFail   atomic.Uint64
	routeExpiryFail   atomic.Uint64
}

// metricsHandler exposes only process-local aggregate API metrics. It avoids
// paths, usernames, repository names, and other unbounded or sensitive labels.
func (a *App) metricsHandler(w http.ResponseWriter, r *http.Request) {
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
	fmt.Fprintln(w, "# HELP gitown_http_request_duration_seconds API request duration in seconds.")
	fmt.Fprintln(w, "# TYPE gitown_http_request_duration_seconds histogram")
	for index, bound := range httpDurationBuckets {
		fmt.Fprintf(w, "gitown_http_request_duration_seconds_bucket{le=\"%g\"} %d\n", bound, a.metrics.durationBuckets[index].Load())
	}
	fmt.Fprintf(w, "gitown_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", a.metrics.requests.Load())
	fmt.Fprintf(w, "gitown_http_request_duration_seconds_sum %.9f\n", float64(a.metrics.duration.Load())/1e9)
	fmt.Fprintf(w, "gitown_http_request_duration_seconds_count %d\n", a.metrics.requests.Load())
	fmt.Fprintln(w, "# HELP gitown_http_requests_in_flight Current API requests being handled by this process.")
	fmt.Fprintln(w, "# TYPE gitown_http_requests_in_flight gauge")
	fmt.Fprintf(w, "gitown_http_requests_in_flight %d\n", a.metrics.inFlight.Load())
	writeWorkerMetrics(w, a)
	if a.db == nil {
		fmt.Fprintln(w, "gitown_queue_metrics_available 0")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	summaries, err := a.queueSummaries(ctx)
	if err != nil {
		fmt.Fprintln(w, "gitown_queue_metrics_available 0")
		return
	}
	fmt.Fprintln(w, "gitown_queue_metrics_available 1")
	fmt.Fprintln(w, "# HELP gitown_queue_items Current durable queue items by fixed queue and status.")
	fmt.Fprintln(w, "# TYPE gitown_queue_items gauge")
	fmt.Fprintln(w, "# HELP gitown_queue_oldest_pending_seconds Age of the oldest item awaiting work.")
	fmt.Fprintln(w, "# TYPE gitown_queue_oldest_pending_seconds gauge")
	for _, queue := range summaries {
		for status, count := range queue.Counts {
			fmt.Fprintf(w, "gitown_queue_items{queue=%q,status=%q} %d\n", queue.Queue, status, count)
		}
		fmt.Fprintf(w, "gitown_queue_oldest_pending_seconds{queue=%q} %f\n", queue.Queue, queue.OldestPendingSeconds)
	}
}

func writeWorkerMetrics(w http.ResponseWriter, a *App) {
	counters := []struct {
		name  string
		value uint64
	}{
		{"gitown_worker_attempts_total{queue=\"email\",outcome=\"attempted\"}", a.workers.mailAttempts.Load()},
		{"gitown_worker_attempts_total{queue=\"email\",outcome=\"sent\"}", a.workers.mailSuccess.Load()},
		{"gitown_worker_attempts_total{queue=\"email\",outcome=\"failed\"}", a.workers.mailFailures.Load()},
		{"gitown_worker_attempts_total{queue=\"webhooks\",outcome=\"attempted\"}", a.workers.webhookAttempts.Load()},
		{"gitown_worker_attempts_total{queue=\"webhooks\",outcome=\"delivered\"}", a.workers.webhookSuccess.Load()},
		{"gitown_worker_attempts_total{queue=\"webhooks\",outcome=\"failed\"}", a.workers.webhookFailures.Load()},
		{"gitown_worker_recovered_claims_total", a.workers.recoveredLeases.Load()},
		{"gitown_routes_schedule_runs_total{outcome=\"queued\"}", a.workers.routeScheduleRuns.Load()},
		{"gitown_routes_schedule_runs_total{outcome=\"failed\"}", a.workers.routeScheduleFail.Load()},
		{"gitown_worker_errors_total{worker=\"maintenance\"}", a.workers.maintenanceFail.Load()},
		{"gitown_worker_errors_total{worker=\"abuse_cleanup\"}", a.workers.authCleanupFail.Load()},
		{"gitown_worker_errors_total{worker=\"route_expiry\"}", a.workers.routeExpiryFail.Load()},
	}
	fmt.Fprintln(w, "# HELP gitown_worker_attempts_total Background outbox work outcomes since process start.")
	fmt.Fprintln(w, "# TYPE gitown_worker_attempts_total counter")
	for _, counter := range counters[:6] {
		fmt.Fprintf(w, "%s %d\n", counter.name, counter.value)
	}
	fmt.Fprintln(w, "# HELP gitown_worker_recovered_claims_total In-flight outbox claims recovered after their lease expired.")
	fmt.Fprintln(w, "# TYPE gitown_worker_recovered_claims_total counter")
	fmt.Fprintf(w, "%s %d\n", counters[6].name, counters[6].value)
	fmt.Fprintln(w, "# HELP gitown_routes_schedule_runs_total Routes schedule planning outcomes; these do not execute jobs.")
	fmt.Fprintln(w, "# TYPE gitown_routes_schedule_runs_total counter")
	for _, counter := range counters[7:9] {
		fmt.Fprintf(w, "%s %d\n", counter.name, counter.value)
	}
	fmt.Fprintln(w, "# HELP gitown_worker_errors_total Background worker operation errors since process start.")
	fmt.Fprintln(w, "# TYPE gitown_worker_errors_total counter")
	for _, counter := range counters[9:] {
		fmt.Fprintf(w, "%s %d\n", counter.name, counter.value)
	}
}
