# Observability and probes

The API process exposes three probe routes:

- `GET /livez` confirms the process can answer requests. It deliberately does not depend on PostgreSQL, so an orchestrator should restart the process only when this probe fails.
- `GET /readyz` checks PostgreSQL with a one-second deadline. `GET /healthz` remains an equivalent compatibility route. Remove an instance from service when readiness fails; do not use readiness as a restart signal.
- `GET /metrics` emits Prometheus text exposition for process-local aggregate API request count, response counts by fixed HTTP status class, cumulative duration, and current in-flight count.

Metrics are intentionally limited to `/api/` requests. They do not include URLs, account/repository identifiers, IP addresses, or request bodies, avoiding secrets and unbounded label cardinality. Counters reset when the process restarts and are not aggregated across replicas; this is a starter operational signal, not a durable monitoring backend. Restrict external access to `/metrics` at the reverse proxy if the deployment considers operational metadata sensitive.

There is no OpenTelemetry exporter, trace propagation, worker/queue metrics, dashboard, alert policy, or centralized storage yet. Public beta remains gated on those deployment-level controls, an isolated executor for untrusted code (currently not supported), restore drills, abuse monitoring, and an independent security review. Do not expose GITOWN to untrusted public workloads on the basis of these probes alone.
