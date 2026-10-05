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

## Password recovery

The login page links to Forgot your password. The API returns the same response for known and unknown email addresses. A matching account receives a one-time link that expires after 30 minutes; requests for the same account have a one-minute cooldown. Only the token digest is stored in the token table. The outbox body containing the link is erased after delivery, suppression, or final delivery failure.

Using the link changes the password, consumes the token, revokes every browser session and personal access token, and records an audit event. GITOWN queues a security notice to the account email. Users must sign in again with the new password.

## Remaining account-security work

General step-up authentication for destructive actions, recovery-code regeneration, richer security-event notifications, and durable account/IP abuse decisions have not shipped. See the [capability audit](CAPABILITY_AUDIT.md) for the remaining implementation order.
