# Security and deployment boundary

GITOWN v0.1 is for local development on a trusted machine. Do not expose it as a public multi-tenant service yet. Report vulnerabilities privately to the repository owner; avoid publishing credentials or private source code in an issue.

Controls implemented: Argon2id password hashes; random hashed credentials; session/token expiry; HTTP-only same-site cookies; exact-origin JSON mutation checks; owner-only repository writes; private-repository checks on every Git/API access; opaque repository paths; argument-array Git subprocesses; disabled ambient Git configs and hooks; receive fsck/size checks; force-push/deletion rejection; text escaping; request size/time and concurrency bounds; and atomic ref comparison for merges.

Git is a large native parser handling untrusted data. These controls are not a replacement for an OS sandbox, per-user quotas, process memory/CPU limits, patch management, and an independent review. A hostile authenticated user could still consume significant disk/CPU. The local development PostgreSQL cluster uses loopback-only trust authentication and is not appropriate for shared or production hosts.

Before public hosting, implement:

- Verified identity, account recovery, MFA/invitations, session/device management, and abuse controls.
- SSH, collaborator roles, unified branch policies, and reviewed authorization-denial tests.
- Per-user/IP Git request throttling, disk/inode quotas, sandboxed subprocesses, and hardened storage identities.
- Durable post-receive events and auditable security records with retention and integrity controls.
- TLS at the edge, secure cookies, trusted proxy configuration, HSTS, and a nonce-based production Content Security Policy. The current frontend policy permits inline/eval scripts for development compatibility.
- Coordinated backups, automated reconciliation, resource/health alerts, tested RPO/RTO, and incident runbooks.

Secrets: commit no `.env`, tokens, database dumps, or `.data/`. Tokens are shown once and expire after 30 days. Never store a token in a Git remote URL. Use the OS Git credential helper when you need persistent local credentials.

No user workflow execution, custom hooks, HTML previews, import-by-URL, or hosted runners are supported in this release.
