# Private pilot deployment

This is a self-hosted, invitation-coordinated pilot recipe, not approval for public multi-tenant hosting. Keep access behind a VPN/private ingress ACL and admit only named testers. This repository cannot create a server, DNS record, certificate, SMTP account, or backup destination on your behalf; those remain operator-provided. Do not deploy the example secrets or expose the development Compose setup.

## Required operator inputs

- A patched Linux host, Docker Engine and Compose v2.24+, Gitown release checkout pinned to a commit, and an unused private Docker subnet.
- A private DNS name and VPN interface IPv4 address. The host firewall/VPN ACL must allow port 443 only from the pilot group. The Compose overlay binds Caddy to that private address, not `0.0.0.0`.
- A trusted TLS certificate whose SAN matches the DNS name, stored as `fullchain.pem` and `privkey.pem` in a host directory readable by Caddy. Use your private PKI or certificate automation; never commit the key.
- A real SMTP relay and sender address. Verify delivery before enabling onboarding or relying on password recovery.
- An off-host encrypted backup destination, a private alert/monitoring operator, and a documented incident contact.

## Prepare secrets and configuration

Create the repository-storage and TLS directories with restrictive ownership/permissions. Copy the sample into the ignored local secrets file, then replace every placeholder:

```sh
cp .env.pilot.example .env.pilot
chmod 600 .env.pilot
```

Generate different random values for the database, GITOWN encryption key, and Grafana password; for example, run `openssl rand -hex 32` three times. Keep `.env.pilot` owner-readable only and store it in your deployment secret manager as well as on the host. `GITOWN_SECRET_KEY` must remain stable and be backed up securely: losing or changing it makes encrypted MFA, district, route, and webhook secrets unreadable.

Set `PILOT_BIND_IP` to the host's private/VPN IPv4 interface address. Choose a `PILOT_DOCKER_SUBNET` that overlaps neither host routes nor other Docker networks; assign `PILOT_PROXY_IP` inside it. Trust exactly that proxy address as `GITOWN_TRUSTED_PROXIES`, not the whole network. Set `PILOT_TLS_DIR` and `GITOWN_REPOSITORY_PATH` to absolute host paths. Replace the four `PILOT_*_IMAGE` examples with official images verified and pinned to immutable SHA-256 digests. Keep the source checkout and its release identity recorded; build the API and web images from that exact checkout.

The sample is deliberately unusable until placeholders are replaced. Run:

```sh
npm run pilot:check
```

This checks private-only binding, exact proxy trust, HTTPS URLs, verified-email/SMTP pairing, stable-format secrets, TLS files and permissions, digest-pinned monitoring/database/proxy images, and the resolved Compose configuration. It never prints secret values. `npm run pilot:check -- --structure-only` checks only the Compose structure using the example file; it does not validate deployment readiness.

## Start the isolated stack

The pilot overlay removes host-published API/web ports. PostgreSQL remains bound to loopback only so the offline backup tool can reach it. HTTPS is served by Caddy only on `PILOT_BIND_IP:443`; Caddy sends `/git` and health probes to the API, with other browser paths to the web service. Prometheus and Grafana bind only to host loopback (`9090` and `3001`); access them through a host SSH tunnel, not the pilot URL. Prometheus scrapes the API over the private Compose network. The included Prometheus rules and Grafana dashboard are starter monitoring, not a measured SLO. Configure and test an alert receiver before relying on alerts; no receiver is provisioned by this overlay.

After the preflight passes, review the resolved settings without publishing or logging them, then start the stack:

```sh
docker compose --env-file .env.pilot -f compose.yaml -f compose.pilot.yaml config --quiet
docker compose --env-file .env.pilot -f compose.yaml -f compose.pilot.yaml up -d --build
GITOWN_DEPLOY_URL='https://your-private-pilot-domain' npm run check:deployment -- --health
```

The health check probes through HTTPS. Confirm `/readyz`, the Grafana dashboard, Prometheus scrape health, SMTP delivery, and the private TLS certificate from a VPN-authorized client. Verify a non-VPN client cannot reach port 443. Do not enable Routes execution; GITOWN does not currently execute repository-authored commands.

## Controlled account onboarding

The default is `GITOWN_ALLOW_SIGNUP=false` and verified email is required. Before creating the first operator, prove that the firewall/VPN ACL admits only the operator's device. Temporarily set signup to `true`, acknowledge the access restriction, and run:

```sh
PILOT_ONBOARDING_ACL_CONFIRMED=true npm run pilot:check -- --onboarding-window
docker compose --env-file .env.pilot -f compose.yaml -f compose.pilot.yaml up -d api web caddy
```

Register the operator, complete email verification through the real SMTP relay, add the username to `GITOWN_OPERATORS`, then set signup back to `false` and redeploy. Verify `/register` is denied from an authorized VPN client. For each later cohort, invite named users by email first; open a short signup window only while the VPN/firewall ACL is restricted to the approved cohort, ask each person to verify their email and accept the invitation, then turn signup off and verify it is denied again. Registration is not invitation-only in the application, so network allowlisting and short windows are required controls. Never open signup to the general internet for this pilot.

## Offline backup and restore drill

The backup wrapper stops Caddy, web, and API (including API background workers/Git writes), leaves PostgreSQL running, invokes the tested offline backup tool against the bind-mounted repository directory, and attempts to restart the pilot stack even if backup creation fails. Stop any separately deployed SSH Git gateway or other repository writer before running it. Use a new absolute snapshot path and a host with PostgreSQL 17 client tools matching the server major version:

```sh
npm run pilot:backup -- /srv/backups/gitown/pilot-$(date +%Y%m%d-%H%M%S)
```

The resulting snapshot contains sensitive account/token/session hashes and private source. Encrypt it with your approved host/KMS process and copy it off-host; this wrapper does not encrypt, upload, schedule, or prune snapshots. Confirm backup success and that the app stack came back healthy. Run it from a restricted operator shell; never put secrets in shell history or CI logs.

Before inviting testers and on a recurring schedule, restore a recent snapshot into a separate isolated recovery deployment with a new empty database and repository directory. Follow [backup and restore](operations/backup-restore.md), then run `scripts/restore-drill.mjs` against the recovery deployment with a dedicated drill account, short-lived read-only Git token, known private repository, issue, and Unite request. Record the report, backup age, measured RPO/RTO, restore build and operator acceptance. The CI restore test is not evidence that this host's backup destination or restore path works.

## Rollout and stop conditions

1. Apply a reviewed release commit and record its SHA; take a coordinated backup before migrations.
2. Validate HTTPS, private ingress, SMTP verification/recovery notices, operator access, monitoring visibility and the restore path before adding testers.
3. Start with a small named cohort; keep registration closed outside allowlisted enrollment windows. Review abuse/queue alerts and backup outcomes daily during the pilot.
4. Stop onboarding and isolate ingress on unexpected private-data access, unverified proxy identity, lost encryption key, failed restore, repeated dead letters, or resource exhaustion. Preserve logs/backups for investigation without copying secrets into tickets.

This procedure does not waive the separate independent security review, deployment-environment restore evidence, representative capacity testing, or other [public-beta gates](RELEASE_GATES.md). Keep the product private and do not set `GITOWN_PUBLIC_BETA=true` for this pilot.
