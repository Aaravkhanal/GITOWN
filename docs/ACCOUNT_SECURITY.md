# Account security

GITOWN supports one-time password recovery, email verification, and TOTP MFA with single-use recovery codes.

## Email delivery

Configure both `GITOWN_SMTP_ADDR` and `GITOWN_SMTP_FROM` before relying on verification or recovery emails. Without them, the durable mail worker marks transactional messages as suppressed. Users see generic responses and cannot use a link that was not delivered.

To require email verification at registration and sign-in, set:

```sh
GITOWN_REQUIRE_VERIFIED_EMAIL=true
```

Existing accounts are considered verified during the migration. New registrations receive a one-time link that expires after 24 hours. The token is stored as a digest, and the account cannot sign in while enforcement is enabled until the link is used. Verification requests return a generic response and are rate limited. A signed-in user can check status and request another link from Account security; public resend is also available from `/verify-email`.

Leave enforcement disabled until SMTP is configured and tested. When disabled, sign-in keeps the existing behavior; signed-in users can still verify their address from Account security.

## Two-step sign-in

Set `GITOWN_SECRET_KEY` to a stable, private deployment secret before enabling MFA. GITOWN encrypts the TOTP secret with AES-GCM under this key. Changing the key without a migration makes already-enrolled authenticator secrets unreadable.

From Account security, confirm the current password, add the displayed key to an authenticator app, and enter a current six-digit code. Setup must be confirmed within 15 minutes. Once enabled, password sign-in creates only a five-minute challenge; it does not create a session until a valid authenticator or recovery code is submitted. TOTP codes have a 30-second period and a ±1-step clock window. A successfully used time step cannot be replayed.

GITOWN generates ten recovery codes after enrollment. They are returned only once and stored as digests. Each can complete one sign-in or authorize disabling MFA, then it is deleted. Disabling MFA requires the password and one authenticator or recovery code and sends an account email notice. Existing sessions remain active when MFA is enabled; users should review their sessions as part of setup.

Use **Regenerate recovery codes** in Account security when a code may have been exposed or the set is running low. The operation requires the current password and a fresh authenticator or unused recovery code. It replaces the full set atomically: every previously issued code stops working, and the new ten codes are displayed only in the successful response. A security email and audit event are recorded.

## Step-up confirmation

High-impact actions (changing credentials or SSH/signing keys; creating or revoking tokens and sessions; changing repository settings, membership, invitations, branch rules, webhooks, deploy keys or installed apps; renaming, archiving or deleting a repository; and starting or cancelling a repository transfer) require fresh authentication from the current browser session. Confirmation lasts ten minutes. The browser asks for the current password and, when MFA is enabled, one current authenticator or unused recovery code. A recovery code used for this confirmation is consumed. Password changes and MFA enrollment/disable require their own current-password proof; password changes also require MFA proof when it is enabled. District secret and webhook changes are also step-up protected.

The step-up timestamp is stored with the hashed server-side session, not in a client-controlled cookie. The confirmation and sensitive action are audited. If MFA is enabled, do not skip the second factor. For API clients, call `POST /api/v1/user/step-up` with the browser session and `{ "current_password": "…", "code": "123456" }` (or `recovery_code`) before retrying the action. Access tokens cannot perform account-security actions.

## Production deployment checklist

Before requiring verified email or relying on account recovery:

1. Configure `GITOWN_SMTP_ADDR` as `host:port` and a valid `GITOWN_SMTP_FROM`. Set `GITOWN_SMTP_USER` and `GITOWN_SMTP_PASSWORD` when the relay requires authentication. Keep credentials in the deployment secret store, never in the repository.
2. Send test verification, password-recovery, new-device, password-change, MFA, recovery-code-rotation, and session-revocation messages through the real relay. Verify delivery, links, sender alignment, and that queued mail failures are visible to operators.
3. Only after successful delivery, set `GITOWN_REQUIRE_VERIFIED_EMAIL=true`. Existing accounts are grandfathered verified by migration; test new-account verification and resend behavior before opening registration.
4. Set a stable, private `GITOWN_SECRET_KEY` and back it up securely. MFA secrets become unreadable if the key is lost or changed. Serve production through HTTPS so secure session cookies are enabled.
5. Set a stable, private `GITOWN_SECRET_KEY`; abuse fingerprints are HMACed with it when configured. Back it up securely and keep it stable across API replicas. Set `GITOWN_OPERATORS` to the minimum comma-separated operator usernames. The read-only `GET /api/v1/operator/security/abuse` view exposes abbreviated fingerprints, route categories, recent authentication-abuse events, and currently blocked buckets; it does not reveal raw emails or IP addresses. The default authentication throttle is 30 attempts per ten minutes for each IP and account bucket, followed by a ten-minute block; the same PostgreSQL counters apply across API replicas.
6. If deployed behind a reverse proxy, set `GITOWN_TRUSTED_PROXIES` to the exact proxy IPs/CIDRs. GITOWN only honors `X-Forwarded-For` when the direct peer belongs to this list and walks the chain from the trusted side; ensure the proxy overwrites or appends the connecting client address. Do not trust arbitrary client-supplied forwarded headers.
7. Test recovery and MFA lockout procedures, session/token revocation, proxy address handling, and alerts before relying on these protections for a public service. Abuse counters are shared in PostgreSQL across API processes and automatically age out; the event view covers the last seven days.

## Password recovery

The login page links to Forgot your password. The API returns the same response for known and unknown email addresses. A matching account receives a one-time link that expires after 30 minutes; requests for the same account have a one-minute cooldown. Only the token digest is stored in the token table. The outbox body containing the link is erased after delivery, suppression, or final delivery failure.

Using the link changes the password, consumes the token, revokes every browser session and personal access token, and records an audit event. GITOWN queues a security notice to the account email. Users must sign in again with the new password.

## Remaining account-security work

The core Phase 1 account controls are implemented. Remaining operational gates are delivery verification against the production SMTP provider, monitored mail-delivery failures, proxy-aware client identity validation, and production abuse-threshold tuning. Do not describe a deployment as verified until those environment-specific checks pass. See the [capability audit](CAPABILITY_AUDIT.md) for product boundaries.
