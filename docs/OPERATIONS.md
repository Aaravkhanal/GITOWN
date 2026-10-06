# Operations runbook

This runbook covers the current development-alpha service. Configure TLS, backups, log retention, and alert delivery in the deployment environment. Never use production credentials in test or support output.

## First response

1. Check `/livez`, then `/readyz`. A live-but-not-ready process should be removed from service while PostgreSQL connectivity is investigated; restarting it does not repair a database outage.
2. Search structured server logs by `trace_id` or `request_id` from the response headers. `http server span` logs omit request paths and payloads. Do not ask users to paste tokens, cookies, recovery codes, webhook bodies, or SMTP credentials.
3. Open the provisioned GITOWN Grafana dashboard and inspect API error rate, queue counts/age, worker outcomes, and recovered claims. Confirm Prometheus can scrape the service and that Alertmanager has a configured receiver.
4. Queue operator APIs require a signed-in username in `GITOWN_OPERATORS`. `GET /api/v1/operator/queues` exposes counts plus the most recent 50 failed email and webhook items with bounded error text. It omits recipient addresses, webhook URLs, request payloads, and webhook response bodies.

## Queue recovery

Email and webhook outboxes use `pending`, `sending`, and terminal `sent`/`success` or `failed` states. Claims are committed before network I/O; email claims have a ten-minute lease to cover a serial batch of ten 45-second SMTP attempts, and webhook claims have a three-minute lease to cover a serial batch of ten ten-second requests. A worker restart leaves rows in `sending`; a subsequent worker tick increments the attempt and returns them to `pending`, or marks them `failed` once the configured maximum is reached. Backoff is exponential and capped. `failed` is the dead-letter state.

Delivery is at-least-once. If a worker stops after SMTP/webhook acceptance and before it stores success, recovery can send the same notification again. The batch leases exceed current per-request deadlines; raising a transport deadline or batch size requires reviewing the matching lease at the same time.

Inspect dead letters through the operator endpoint. Fix the underlying SMTP configuration or receiver first. Retry one recoverable item with `POST /api/v1/operator/queues/{email|webhooks}/{id}/retry`; the operation atomically resets the attempt counter, makes the item due immediately, and writes an audit event. Password-recovery and email-verification bodies are erased after terminal failure for security, so those email items cannot be replayed; trigger a fresh user-requested recovery/verification email instead. Do not reset queue rows directly in SQL except during a documented incident procedure.

If a dead letter recurs, preserve its bounded `last_error`, queue ID, trace/request IDs when available, and timestamps. Do not include payloads, addresses, webhook secrets, or response bodies in a public issue. Redrive only after confirming the receiver/configuration is safe; webhook retries may repeat a previously accepted event.

## Routes safety boundary

Routes is a parser, planner, approvals/control-plane, artifact/cache metadata, and schedule queue only. `run` and `uses` strings stay opaque. `ready` means that the plan's declared dependency/approval policy is satisfied; it does not mean a runner may currently execute it. The hosted runner is offline and self-hosted heartbeat returns no jobs. Keep runner registrations isolated and do not connect an executor to these endpoints.

Execution remains disabled until independent review proves immutable checkout, filesystem/network isolation, ephemeral scoped credentials, secret redaction, live CPU/memory/disk limits, and process-tree cancellation. A missing item means no execution. Do not use the API server, Git subprocess, a shell wrapper, or a CI convenience path to execute repository-authored content.

## Backups and restore

Follow [backup and restore](operations/backup-restore.md). Database metadata and Git/LFS storage must represent the same point in time; stop writes or use a deployment-level coordinated snapshot. Practice restore into a separate empty database and storage directory before relying on the backup.

## Load checks and SLOs

Run `npm run load:check` only against a disposable deployment. The check makes bounded, unauthenticated GET requests to `/livez`, `/readyz`, and public repository search, and reports client-observed throughput and p50/p95/p99 latency. Defaults are 100 requests at concurrency 10; hard caps are 10,000 requests and concurrency 100. CI runs a small 90-request smoke check. Neither establishes multi-instance capacity or a service SLO. Before public beta, run a separately approved soak/failure exercise on isolated infrastructure, record capacity and saturation, and tune thresholds from measurements. Never point the load check at production.

## Migration rollout and rollback

Take and verify a coordinated database + Git-storage backup before schema changes. Apply migrations with one deployment rollout; startup uses a PostgreSQL advisory transaction lock so concurrent instances do not apply the same migration simultaneously. Migration files are immutable after release: each applied file is recorded with a SHA-256 checksum, and startup fails closed if a checked migration differs. Existing deployments adopt checksums once when this guard is first introduced; this cannot prove old file history before adoption.

Migrations are forward-only; there are no automatic `down` migrations. Prefer rolling the application forward with a corrective migration. Rolling back an application binary is supported only if its code remains compatible with the already-applied schema. If data or schema must be reverted, restore the pre-deployment backup into a separate database and matching repository-storage snapshot, verify it with the restore drill, and only then make an operator-controlled traffic decision. Do not run ad hoc reverse SQL in production.
