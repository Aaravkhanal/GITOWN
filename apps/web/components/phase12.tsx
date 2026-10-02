"use client";

import Link from "next/link";
import { useState } from "react";
import { MessagesSquare, Shield, GitMerge } from "lucide-react";
import { post, put, remove, repoPath, type Repo } from "@/lib/api";
import { ErrorMessage, Loading, useData } from "@/components/ui";
import { SafeMarkdown } from "@/components/ecosystem";

export function RouteDesk({
  endpoint,
  canManage,
}: {
  endpoint: string;
  canManage: boolean;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [token, setToken] = useState("");
  const secrets = useData<{ items: { name: string }[] }>(
    canManage ? `${endpoint}/routes/secrets` : null,
    version,
  );
  const runners = useData<{
    items: { kind: string; name: string; execution_enabled: boolean; reason?: string; online?: boolean }[];
    execution_enabled: boolean;
  }>(`${endpoint}/routes/runners`, version);
  const caches = useData<{ items: { key: string; size_bytes: number }[]; quota_bytes: number }>(
    `${endpoint}/routes/caches`,
    version,
  );
  return (
    <section className="routes-platform">
      <h3>Runners, secrets, and cache</h3>
      <p className="muted small-text">
        Hosted and self-hosted runners can be recorded, and secrets stay encrypted.
        No runner receives a workflow command. Execution stays off until the
        sandbox has been reviewed. Network access cannot be set to open.
      </p>
      <ErrorMessage error={error || runners.error} />
      {runners.data?.items.map((runner) => (
        <p key={`${runner.kind}-${runner.name}`}>
          <strong>{runner.name}</strong> · {runner.kind}
          {runner.online ? " · online" : ""} · execution off
          {runner.reason ? ` — ${runner.reason}` : ""}
        </p>
      ))}
      {canManage && (
        <form
          className="inline-form"
          onSubmit={async (e) => {
            e.preventDefault();
            setError("");
            const data = new FormData(e.currentTarget);
            try {
              const created = await post<{ token: string }>(`${endpoint}/routes/runners`, {
                name: data.get("name"),
                cpu_millis: 1000,
                memory_mb: 512,
                disk_mb: 1024,
                network: "restricted",
                labels: [],
              });
              setToken(created.token);
              setVersion((v) => v + 1);
              e.currentTarget.reset();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <label>
            Self-hosted runner name
            <input name="name" required pattern="[a-z0-9][a-z0-9-]{0,39}" />
          </label>
          <button className="button small-button" type="submit">
            Register runner
          </button>
        </form>
      )}
      {token && (
        <p>
          Runner token, shown once: <code>{token}</code>
        </p>
      )}
      {canManage && (
        <form
          className="inline-form"
          onSubmit={async (e) => {
            e.preventDefault();
            setError("");
            const data = new FormData(e.currentTarget);
            try {
              await put(`${endpoint}/routes/secrets/${data.get("name")}`, {
                value: data.get("value"),
              });
              setVersion((v) => v + 1);
              e.currentTarget.reset();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <label>
            Secret name
            <input name="name" required pattern="[A-Z][A-Z0-9_]{0,63}" />
          </label>
          <label>
            Secret value
            <input name="value" type="password" required />
          </label>
          <button className="button small-button" type="submit">
            Store secret
          </button>
        </form>
      )}
      <ErrorMessage error={secrets.error} />
      {secrets.data?.items.map((secret) => (
        <p key={secret.name}>
          {secret.name}{" "}
          <button
            className="button small-button"
            type="button"
            onClick={async () => {
              await remove(`${endpoint}/routes/secrets/${secret.name}`);
              setVersion((v) => v + 1);
            }}
          >
            Delete
          </button>
        </p>
      ))}
      <p className="muted small-text">
        Cache entries: {caches.data?.items.length || 0}. Quota{" "}
        {caches.data?.quota_bytes || 0} bytes. A future runner would fill this;
        maintainers can store a cache object from the API.
      </p>
    </section>
  );
}

export function TownHallPanel({ endpoint, repo }: { endpoint: string; repo: Repo }) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [open, setOpen] = useState<number | null>(null);
  const list = useData<{ items: { number: number; title: string; category: string; state: string; author: string }[] }>(
    `${endpoint}/discussions`,
    version,
  );
  const detail = useData<{ title: string; body: string; comments: { id: string; body: string; author: string }[] }>(
    open ? `${endpoint}/discussions/${open}` : null,
    version,
  );
  return (
    <section className="panel">
      <h2>
        <MessagesSquare size={18} /> Town Hall
      </h2>
      <p className="muted small-text">Discussions stay with the repository. They are not issues and they are not unite requests.</p>
      <ErrorMessage error={error || list.error} />
      {repo.can_comment && !repo.archived && (
        <form
          className="inline-form"
          onSubmit={async (e) => {
            e.preventDefault();
            const data = new FormData(e.currentTarget);
            setError("");
            try {
              await post(`${endpoint}/discussions`, {
                title: data.get("title"),
                body: data.get("body"),
                category: data.get("category"),
              });
              setVersion((v) => v + 1);
              e.currentTarget.reset();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <label>
            Title
            <input name="title" required maxLength={200} />
          </label>
          <label>
            Category
            <select name="category" defaultValue="general">
              <option value="general">General</option>
              <option value="ideas">Ideas</option>
              <option value="announcements">Announcements</option>
              <option value="q-and-a">Q&A</option>
            </select>
          </label>
          <label>
            Body
            <textarea name="body" required maxLength={20000} rows={4} />
          </label>
          <button className="button primary small-button" type="submit">
            Start discussion
          </button>
        </form>
      )}
      {list.loading ? (
        <Loading />
      ) : (
        list.data?.items.map((item) => (
          <button className="button small-button" type="button" key={item.number} onClick={() => setOpen(item.number)}>
            #{item.number} {item.title} · {item.category} · {item.state}
          </button>
        ))
      )}
      {open && detail.data && (
        <article>
          <h3>{detail.data.title}</h3>
          <SafeMarkdown text={detail.data.body} />
          {detail.data.comments.map((comment) => (
            <p key={comment.id}>
              <strong>{comment.author}</strong> <SafeMarkdown text={comment.body} />
            </p>
          ))}
          {repo.can_comment && !repo.archived && (
            <form
              onSubmit={async (e) => {
                e.preventDefault();
                const data = new FormData(e.currentTarget);
                try {
                  await post(`${endpoint}/discussions/${open}/comments`, { body: data.get("body") });
                  setVersion((v) => v + 1);
                  e.currentTarget.reset();
                } catch (saveError) {
                  setError((saveError as Error).message);
                }
              }}
            >
              <textarea name="body" required rows={3} />
              <button className="button small-button" type="submit">
                Comment
              </button>
            </form>
          )}
        </article>
      )}
    </section>
  );
}

export function SupplyPanel({ endpoint, repo }: { endpoint: string; repo: Repo }) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const graph = useData<{ items: { manifest: string; ecosystem: string; name: string; version: string }[] }>(
    `${endpoint}/dependency-graph`,
    version,
  );
  const findings = useData<{ items: { id: string; path: string; line: number; marker: string; state: string }[] }>(
    repo.can_write ? `${endpoint}/secret-findings` : null,
    version,
  );
  const advisories = useData<{ items: { code: string; severity: string; summary: string; state: string }[] }>(
    `${endpoint}/advisories`,
    version,
  );
  const alerts = useData<{ items: { id: string; code: string; package_name: string; installed_version: string; state: string }[] }>(
    repo.can_write ? `${endpoint}/vulnerability-alerts` : null,
    version,
  );
  const envs = useData<{ items: { id: string; status: string; image: string }[]; execution_enabled: boolean }>(
    `${endpoint}/dev-environments`,
    version,
  );
  const owners = useData<{ items: { pattern: string; owners: string[] }[] }>(`${endpoint}/codeowners`, version);
  return (
    <section className="panel">
      <h2>
        <Shield size={18} /> Supply and workshop
      </h2>
      <p className="muted small-text">
        Dependency graph, secret markers, advisories, and code owners are read from the default branch.
        A development environment records <code>.gitown/dev.yml</code> and stays pending until sandbox review. It does not start a machine.
      </p>
      <ErrorMessage error={error} />
      {repo.can_write && !repo.archived && (
        <button
          className="button small-button"
          type="button"
          onClick={async () => {
            setError("");
            try {
              await post(`${endpoint}/supply-chain/scan`, {});
              setVersion((v) => v + 1);
            } catch (scanError) {
              setError((scanError as Error).message);
            }
          }}
        >
          Scan default branch
        </button>
      )}
      <h3>Dependency graph</h3>
      {graph.data?.items.length ? (
        graph.data.items.map((item) => (
          <p key={`${item.manifest}-${item.name}`}>
            {item.ecosystem} {item.name} {item.version} <span className="muted">({item.manifest})</span>
          </p>
        ))
      ) : (
        <p className="muted">No scanned dependencies yet.</p>
      )}
      <h3>Secret findings</h3>
      {findings.data?.items.map((item) => (
        <p key={item.id}>
          {item.path}:{item.line} matched {item.marker} ({item.state})
        </p>
      ))}
      <h3>Advisories</h3>
      {advisories.data?.items.map((item) => (
        <p key={item.code}>
          {item.code} · {item.severity} · {item.state} — {item.summary}
        </p>
      ))}
      {repo.can_maintain && !repo.archived && (
        <form
          className="inline-form"
          onSubmit={async (e) => {
            e.preventDefault();
            const data = new FormData(e.currentTarget);
            try {
              await post(`${endpoint}/advisories`, {
                severity: data.get("severity"),
                summary: data.get("summary"),
                package_name: data.get("package_name"),
                ecosystem: data.get("ecosystem"),
                patched_version: data.get("patched_version"),
                state: "published",
              });
              setVersion((v) => v + 1);
              e.currentTarget.reset();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <input name="package_name" placeholder="package" required />
          <select name="ecosystem" defaultValue="go">
            <option value="go">go</option>
            <option value="npm">npm</option>
            <option value="pypi">pypi</option>
          </select>
          <select name="severity" defaultValue="medium">
            <option>low</option>
            <option>medium</option>
            <option>high</option>
            <option>critical</option>
          </select>
          <input name="patched_version" placeholder="patched version" />
          <input name="summary" placeholder="summary" required />
          <button className="button small-button" type="submit">
            Publish advisory
          </button>
        </form>
      )}
      <h3>Vulnerability alerts</h3>
      {alerts.data?.items.map((item) => (
        <p key={item.id}>
          {item.code} · {item.package_name} {item.installed_version} ({item.state})
        </p>
      ))}
      <h3>Code owners</h3>
      {owners.data?.items.map((rule) => (
        <p key={rule.pattern}>
          <code>{rule.pattern}</code> {rule.owners.map((name) => `@${name}`).join(" ")}
        </p>
      ))}
      <h3>Development environments</h3>
      <p className="muted small-text">Execution enabled: {String(envs.data?.execution_enabled ?? false)}</p>
      {envs.data?.items.map((item) => (
        <p key={item.id}>
          {item.image || "no image"} · {item.status}
        </p>
      ))}
      {repo.can_write && !repo.archived && (
        <button
          className="button small-button"
          type="button"
          onClick={async () => {
            setError("");
            try {
              await post(`${endpoint}/dev-environments`, {});
              setVersion((v) => v + 1);
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          Request environment
        </button>
      )}
    </section>
  );
}

export function MergeQueuePanel({ endpoint, repo }: { endpoint: string; repo: Repo }) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const queue = useData<{ items: { number: number; title: string; position: number }[] }>(
    `${endpoint}/merge-queue`,
    version,
  );
  if (!repo.can_write && !queue.data?.items.length) return null;
  return (
    <section className="panel">
      <h3>
        <GitMerge size={16} /> Merge queue
      </h3>
      <p className="muted small-text">
        While the queue has a waiting request, only the one at the front can merge. This does not run checks; existing branch rules still apply.
      </p>
      <ErrorMessage error={error || queue.error} />
      {queue.data?.items.map((item) => (
        <p key={item.number}>
          {item.position}.{" "}
          <Link href={`${repoPath(repo)}/pulls/${item.number}`}>
            #{item.number} {item.title}
          </Link>
          {repo.can_write && !repo.archived && (
            <button
              className="button small-button"
              type="button"
              onClick={async () => {
                try {
                  await post(`${endpoint}/merge-queue/${item.number}/dequeue`, {});
                  setVersion((v) => v + 1);
                } catch (saveError) {
                  setError((saveError as Error).message);
                }
              }}
            >
              Remove
            </button>
          )}
        </p>
      ))}
      {repo.can_write && !repo.archived && (
        <form
          className="inline-form"
          onSubmit={async (e) => {
            e.preventDefault();
            const data = new FormData(e.currentTarget);
            try {
              await post(`${endpoint}/merge-queue`, { number: Number(data.get("number")) });
              setVersion((v) => v + 1);
              e.currentTarget.reset();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <label>
            Unite request number
            <input name="number" type="number" min={1} required />
          </label>
          <button className="button small-button" type="submit">
            Add to queue
          </button>
        </form>
      )}
    </section>
  );
}

export function SnippetsPage() {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const list = useData<{ items: { id: string; title: string; owner: string; visibility: string }[] }>(
    "/snippets",
    version,
  );
  const snippet = useData<{ title: string; filename: string; content: string; owner: string }>(
    selected ? `/snippets/${selected}` : null,
    version,
  );
  return (
    <section>
      <div className="page-heading">
        <div>
          <h1>Snippets</h1>
          <p>Small standalone files. Public snippets are readable by anyone.</p>
        </div>
      </div>
      <ErrorMessage error={error || list.error} />
      <form
        className="panel inline-form"
        onSubmit={async (e) => {
          e.preventDefault();
          const data = new FormData(e.currentTarget);
          setError("");
          try {
            await post("/snippets", {
              title: data.get("title"),
              filename: data.get("filename"),
              content: data.get("content"),
              visibility: data.get("visibility"),
            });
            setVersion((v) => v + 1);
            e.currentTarget.reset();
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        <input name="title" placeholder="Title" required maxLength={120} />
        <input name="filename" placeholder="notes.txt" required />
        <select name="visibility" defaultValue="public">
          <option value="public">Public</option>
          <option value="private">Private</option>
        </select>
        <textarea name="content" required rows={5} maxLength={65536} />
        <button className="button primary" type="submit">
          Save snippet
        </button>
      </form>
      {list.loading ? (
        <Loading />
      ) : (
        list.data?.items.map((item) => (
          <button className="button small-button" type="button" key={item.id} onClick={() => setSelected(item.id)}>
            {item.title} · {item.owner} · {item.visibility}
          </button>
        ))
      )}
      {snippet.data && (
        <article className="panel">
          <h2>
            {snippet.data.title} <span className="muted">{snippet.data.filename}</span>
          </h2>
          <pre>{snippet.data.content}</pre>
        </article>
      )}
    </section>
  );
}

export function MobileDevices() {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  return (
    <section className="panel">
      <h2>Mobile notifications</h2>
      <p className="muted small-text">
        Register a device to pull your unread inbox. GITOWN does not send a push through Apple or Google in this version.
      </p>
      <ErrorMessage error={error} />
      <form
        className="inline-form"
        onSubmit={async (e) => {
          e.preventDefault();
          const data = new FormData(e.currentTarget);
          setError("");
          try {
            const created = await post<{ token: string }>("/user/devices", {
              name: data.get("name"),
              platform: data.get("platform"),
            });
            setToken(created.token);
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        <input name="name" placeholder="Phone name" required />
        <select name="platform" defaultValue="ios">
          <option value="ios">iOS</option>
          <option value="android">Android</option>
          <option value="web">Web</option>
        </select>
        <button className="button small-button" type="submit">
          Register device
        </button>
      </form>
      {token && (
        <p>
          Device token, shown once: <code>{token}</code>
        </p>
      )}
    </section>
  );
}

export function Backers({
  username,
  currentUsername,
}: {
  username: string;
  currentUsername?: string;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const pledges = useData<{ items: { sponsor: string; amount_cents: number; message: string }[]; charges: boolean }>(
    `/users/${encodeURIComponent(username)}/sponsorships`,
    version,
  );
  return (
    <section className="panel">
      <h2>Backers</h2>
      <p className="muted small-text">
        Pledges are public records. Charges: {String(pledges.data?.charges ?? false)}. GITOWN does not bill a card.
      </p>
      <ErrorMessage error={error || pledges.error} />
      {pledges.data?.items.map((item) => (
        <p key={item.sponsor}>
          {item.sponsor} pledged ${(item.amount_cents / 100).toFixed(2)} {item.message}
        </p>
      ))}
      {currentUsername && currentUsername !== username && (
        <form
          className="inline-form"
          onSubmit={async (e) => {
            e.preventDefault();
            const data = new FormData(e.currentTarget);
            setError("");
            try {
              await post(`/users/${encodeURIComponent(username)}/sponsorships`, {
                amount_cents: Math.round(Number(data.get("dollars")) * 100),
                message: data.get("message") || "",
                public: true,
              });
              setVersion((v) => v + 1);
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <label>
            Pledge in dollars
            <input name="dollars" type="number" min={0} step="0.01" required />
          </label>
          <input name="message" placeholder="Message" maxLength={280} />
          <button className="button small-button" type="submit">
            Record pledge
          </button>
        </form>
      )}
    </section>
  );
}
