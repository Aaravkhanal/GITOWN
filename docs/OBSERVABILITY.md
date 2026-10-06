# Observability

GITOWN ships process-local request/worker counters, durable queue gauges from PostgreSQL, W3C trace-context correlation, a provisionable Grafana dashboard, Prometheus alerts, and queue operator APIs. These are deployable building blocks, not a hosted monitoring service. See [the operations runbook](OPERATIONS.md).

## Metrics and probes

- `GET /livez` confirms the process can answer requests. It does not depend on PostgreSQL; restart only when this probe fails.
- `GET /readyz` checks PostgreSQL with a one-second deadline. Remove an instance from service when readiness fails; do not use it as a restart signal.
- `GET /metrics` emits Prometheus text for API requests/status/duration/in-flight work, email and webhook worker outcomes, maintenance/cleanup/Routes worker errors, recovered worker claims, Routes schedule-planning outcomes, durable queue counts by fixed queue/status, and oldest waiting age. Queue metric collection is bounded to one second. It reports `gitown_queue_metrics_available 0` if PostgreSQL cannot be queried.
- Request duration includes a fixed-bucket Prometheus histogram for aggregate p50/p95/p99 estimates. Buckets are process-local and intentionally have no path or user labels. The starter p95 alert is a 1-second warning threshold, not a measured or approved product SLO; tune it only after representative deployment load tests.

Queue names/statuses are fixed and contain no user-controlled labels. Counts include failed dead letters and active `sending` claims. `route_jobs{status="ready"}` means a future reviewed executor may be allowed to claim a plan; it is not running or eligible for execution today. Metrics counters reset on process restart and are not aggregated across replicas; the durable queue gauges come from PostgreSQL and are shared.

## Trace correlation

The API accepts W3C `traceparent`, creates a child server span, returns the resulting `traceparent`, and emits a structured JSON `http server span` log for sampled traces. Each span includes `trace_id`, `span_id`, request ID, method, status, and duration; it intentionally omits URL paths, query strings, identities, IPs, headers, and bodies. Invalid or unsupported incoming trace context starts a fresh trace. Logs can be correlated by `trace_id` in a log search system.

This is trace-context propagation and log-based server spans, not an OpenTelemetry SDK/OTLP exporter. A centralized collector/backend and spans for individual SQL/Git calls remain follow-up work. Do not infer dependency-level traces from the server span.

## Prometheus and Grafana

The repository includes a scrape configuration, alert rules, a datasource/dashboard provider, and a GITOWN dashboard under `ops/`. Merge the scrape job and rules into your Prometheus deployment and mount the dashboard/provisioning files into Grafana. The sample scrape target `gitown-api:8080` must match your service DNS and listen port. Configure an alert receiver (email, PagerDuty, or your chosen system) in Prometheus/Alertmanager; receiver credentials are deployment secrets and are not stored in GITOWN.

Restrict `/metrics` to the monitoring network at your reverse proxy if operational metadata should not be public. Do not add repository, user, IP, email, token, workflow, or URL labels: they create high cardinality and can expose private data.

## Boundaries

Metrics reset at process restart except queue state, which is queried from PostgreSQL. Email/webhook attempt counters are per-process and delivery is at-least-once: a worker can stop after a remote system accepted a request but before the database records success. Email claims have a ten-minute lease and webhook claims a three-minute lease to exceed their serial batch deadlines; stale claims are retried or dead-lettered. The database/API are not a replacement for backups, restore drills, independent alert routing, or a security review.

Routes never executes repository-provided commands. No dashboard metric, runner heartbeat, job marked `ready`, or stored resource limit should be interpreted as an execution capability. Do not expose untrusted workloads or enable a runner until the sandbox gate in [Routes](ROUTES.md) has passed independent review.
