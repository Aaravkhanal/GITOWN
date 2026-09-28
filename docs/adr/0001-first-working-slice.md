# ADR 0001: Ship a single-node development alpha

Status: accepted for this implementation.

Keep the blueprint's Next.js/TypeScript frontend, Go backend, PostgreSQL database, and native Git binaries. Use one backend process, with boundaries for credentials, configuration, storage, and HTTP workflows. Redis, object storage, queues, and CI runners have no required job in this slice, so they are deferred.

Use opaque random UUIDv4 repository identifiers. They provide collision-resistant filesystem paths independent of user-controlled names. Ordered UUIDv7 IDs from the blueprint can be introduced later if database locality warrants it.

Only the owner can write. Public repositories can be read anonymously; private repositories are available only to their owner. This intentionally avoids presenting collaborator roles before their authorization matrix is implemented.

Smart HTTP uses the system `git-http-backend` with explicitly constructed CGI variables. The application authorizes both ref advertisement and RPC endpoints. The server strips ambient Git configuration, does not use a shell for Git commands, disables repository hooks through controlled configuration, and never checks out or executes hosted code. Platform pre-receive hooks and branch rules will be added together in a future milestone.

Git rejects non-fast-forward updates and deletions globally in this slice. Merge commits use `merge-tree`, `commit-tree`, and compare-and-swap `update-ref`. PostgreSQL stores merge intent before the ref update so interrupted operations can be retried without creating a different merge result.

README and source views render plain text through React escaping. Rich Markdown, syntax highlighting, and HTML previews are deferred until their sanitization, content policy, and limits are designed.

The frontend and API use same-origin browser requests through a Next.js rewrite. JSON mutations require the configured exact Origin. Git uses separate token authentication, never browser cookies. TLS and secure session cookies are required for non-local origins; local HTTP is for development only.

No public deployment, open-source license selection, collaborator invitation, or external service account has been created. GITOWN's original repository and commit identity remain the owner's.

References: [Git HTTP backend](https://git-scm.com/docs/git-http-backend), [Git update-ref](https://git-scm.com/docs/git-update-ref), [Next.js rewrites](https://nextjs.org/docs/app/api-reference/config/next-config-js/rewrites).
