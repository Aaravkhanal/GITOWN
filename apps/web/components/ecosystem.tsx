"use client";

import Link from "next/link";
import { useState } from "react";
import { post, put, remove, repoPath, type Repo } from "@/lib/api";
import { ErrorMessage, Loading, useData } from "@/components/ui";

type Collection = {
  owner: string;
  slug: string;
  title: string;
  repositories: number;
};
type Task = {
  owner: string;
  repository: string;
  number: number;
  title: string;
  label: string;
};
type District = {
  slug: string;
  name: string;
  description: string;
  visibility: string;
  role?: string;
  repo_creation: string;
  base_permission: string;
};
type Crate = {
  name: string;
  description: string;
  visibility: string;
  owner: string;
  retention: number;
};
type SSHKey = {
  id: string;
  title: string;
  fingerprint: string;
  created_at: string;
};
type DropItem = {
  tag: string;
  title: string;
  draft: boolean;
  prerelease: boolean;
};
type DropAsset = {
  name: string;
  sha256: string;
  size_bytes: number;
  download_count: number;
};

export function ExploreMore() {
  const recommendations = useData<{ items: Repo[]; personalized: boolean }>(
    "/search/recommendations",
  );
  const tasks = useData<{ items: Task[] }>("/search/tasks?kind=help");
  const collections = useData<{ items: Collection[] }>("/collections");
  const [query, setQuery] = useState("");
  const [codePath, setCodePath] = useState<string | null>(null);
  const code = useData<{ items: { owner: string; repository: string; path: string; line: number; snippet: string }[] }>(
    codePath,
  );
  return (
    <div className="dashboard-columns">
      <section className="panel">
        <h2>Recommended</h2>
        <p className="muted small-text">
          {recommendations.data?.personalized
            ? "Public repositories that share a topic with work you own or Sparked."
            : "Trending public repositories from the last 30 days."}
        </p>
        <ErrorMessage error={recommendations.error} />
        {recommendations.loading ? (
          <Loading />
        ) : (
          <ul>
            {(recommendations.data?.items || []).map((repo) => (
              <li key={repo.id}>
                <Link href={repoPath(repo)}>
                  {repo.owner}/{repo.name}
                </Link>
                {repo.language ? <span className="muted"> {repo.language}</span> : null}
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="panel">
        <h2>Help wanted</h2>
        <ErrorMessage error={tasks.error} />
        {tasks.loading ? (
          <Loading />
        ) : (
          <ul>
            {(tasks.data?.items || []).map((task) => (
              <li key={`${task.owner}/${task.repository}#${task.number}`}>
                <Link href={`/repos/${task.owner}/${task.repository}/issues`}>
                  {task.owner}/{task.repository}#{task.number}
                </Link>{" "}
                {task.title}
              </li>
            ))}
          </ul>
        )}
        <h2>Community collections</h2>
        <ErrorMessage error={collections.error} />
        <ul>
          {(collections.data?.items || []).map((item) => (
            <li key={`${item.owner}/${item.slug}`}>
              {item.owner}/{item.slug}: {item.title} ({item.repositories})
            </li>
          ))}
        </ul>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            if (query.trim().length >= 2) setCodePath(`/search/code?q=${encodeURIComponent(query.trim())}`);
          }}
        >
          <label>
            Search public code
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Fixed text, 2–80 characters" />
          </label>
          <button className="button" type="submit">
            Search code
          </button>
        </form>
        <ErrorMessage error={code.error} />
        <ul>
          {(code.data?.items || []).map((item) => (
            <li key={`${item.owner}/${item.repository}/${item.path}:${item.line}`}>
              <Link href={`/repos/${item.owner}/${item.repository}`}>
                {item.owner}/{item.repository}
              </Link>{" "}
              {item.path}:{item.line} {item.snippet}
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

export function DistrictsPage() {
  const [version, setVersion] = useState(0);
  const districts = useData<{ items: District[] }>("/districts", version);
  const [slug, setSlug] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const selected = useData<{ items: Repo[] }>(slug ? `/districts/${slug}/repos` : null, version);
  return (
    <div className="form-page">
      <h1>Districts</h1>
      <p className="page-description">
        A district is an organization. Crews are teams inside it. Members inherit the district base permission on its repositories.
      </p>
      <ErrorMessage error={error || districts.error} />
      <form
        className="panel form-panel"
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setBusy(true);
          setError("");
          try {
            const created = await post<District>("/districts", {
              slug: String(data.get("slug") || ""),
              name: String(data.get("name") || ""),
              visibility: String(data.get("visibility") || "public"),
            });
            setSlug(created.slug);
            setVersion((value) => value + 1);
            event.currentTarget.reset();
          } catch (caught) {
            setError((caught as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <label>
          Slug
          <input name="slug" required pattern="[a-z0-9][a-z0-9-]{0,38}" />
        </label>
        <label>
          Name
          <input name="name" required maxLength={80} />
        </label>
        <label>
          Visibility
          <select name="visibility" defaultValue="public">
            <option value="public">Public</option>
            <option value="private">Private</option>
          </select>
        </label>
        <button className="button primary" disabled={busy}>
          Create district
        </button>
      </form>
      {districts.loading ? (
        <Loading />
      ) : (
        <ul>
          {(districts.data?.items || []).map((item) => (
            <li key={item.slug}>
              <button className="text-button" onClick={() => setSlug(item.slug)}>
                {item.name}
              </button>{" "}
              <span className="muted">
                {item.slug} · {item.visibility}
                {item.role ? ` · ${item.role}` : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
      {slug && (
        <section className="panel">
          <h2>{slug}</h2>
          <ErrorMessage error={selected.error} />
          <ul>
            {(selected.data?.items || []).map((repo) => (
              <li key={repo.id}>
                <Link href={repoPath(repo)}>
                  {repo.owner}/{repo.name}
                </Link>{" "}
                <span className="muted">{repo.visibility}</span>
              </li>
            ))}
          </ul>
          <DistrictAdmin slug={slug} onChange={() => setVersion((value) => value + 1)} />
        </section>
      )}
    </div>
  );
}

function DistrictAdmin({ slug, onChange }: { slug: string; onChange: () => void }) {
  const [error, setError] = useState("");
  return (
    <form
      className="form-panel"
      onSubmit={async (event) => {
        event.preventDefault();
        const data = new FormData(event.currentTarget);
        setError("");
        try {
          const kind = String(data.get("kind"));
          if (kind === "member") {
            await post(`/districts/${slug}/members`, {
              username: String(data.get("username") || ""),
              role: String(data.get("role") || "member"),
            });
          } else {
            await post(`/districts/${slug}/crews`, {
              slug: String(data.get("crew") || ""),
              name: String(data.get("crew") || ""),
            });
          }
          onChange();
          event.currentTarget.reset();
        } catch (caught) {
          setError((caught as Error).message);
        }
      }}
    >
      <ErrorMessage error={error} />
      <label>
        Action
        <select name="kind" defaultValue="member">
          <option value="member">Add member</option>
          <option value="crew">Create crew</option>
        </select>
      </label>
      <label>
        Username
        <input name="username" placeholder="builder" />
      </label>
      <label>
        Role
        <select name="role" defaultValue="member">
          <option value="member">Member</option>
          <option value="admin">Admin</option>
        </select>
      </label>
      <label>
        Crew slug
        <input name="crew" placeholder="reviewers" />
      </label>
      <button className="button">Save</button>
    </form>
  );
}

export function CratesPage() {
  const [version, setVersion] = useState(0);
  const crates = useData<{ items: Crate[] }>("/crates", version);
  const [error, setError] = useState("");
  return (
    <div className="form-page">
      <h1>Crates</h1>
      <p className="page-description">
        A crate is a package record labeled npm. This is not the npm wire protocol and it does not scan for malware. Publishing rejects a few private-key headers and token prefixes.
      </p>
      <ErrorMessage error={error || crates.error} />
      <form
        className="panel form-panel"
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setError("");
          try {
            await post("/crates", {
              name: String(data.get("name") || ""),
              description: String(data.get("description") || ""),
              visibility: String(data.get("visibility") || "public"),
              retention: Number(data.get("retention") || 20),
            });
            setVersion((value) => value + 1);
            event.currentTarget.reset();
          } catch (caught) {
            setError((caught as Error).message);
          }
        }}
      >
        <label>
          Name
          <input name="name" required pattern="[a-z0-9][a-z0-9._-]{0,60}" />
        </label>
        <label>
          Description
          <input name="description" maxLength={500} />
        </label>
        <label>
          Visibility
          <select name="visibility" defaultValue="public">
            <option value="public">Public</option>
            <option value="private">Private</option>
          </select>
        </label>
        <label>
          Retention
          <input name="retention" type="number" min={1} max={100} defaultValue={20} />
        </label>
        <button className="button primary">Create crate</button>
      </form>
      {crates.loading ? (
        <Loading />
      ) : (
        <ul>
          {(crates.data?.items || []).map((item) => (
            <li key={item.name}>
              {item.owner}/{item.name} · {item.visibility} · keep {item.retention}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function SSHKeysPage() {
  const [version, setVersion] = useState(0);
  const keys = useData<{ items: SSHKey[] }>("/user/ssh-keys", version);
  const [error, setError] = useState("");
  const [line, setLine] = useState("");
  return (
    <div className="form-page">
      <h1>SSH keys</h1>
      <p className="page-description">
        Add a public key, then install the authorized_keys line on an sshd that forces <code>gitown ssh-shell</code>. GITOWN does not embed an SSH server. The gateway trusts the fingerprint argument supplied by that forced command.
      </p>
      <ErrorMessage error={error || keys.error} />
      {line && (
        <pre className="panel">
          <code>{line}</code>
        </pre>
      )}
      <form
        className="panel form-panel"
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setError("");
          try {
            const created = await post<{ authorized_keys: string }>("/user/ssh-keys", {
              title: String(data.get("title") || ""),
              public_key: String(data.get("public_key") || ""),
            });
            setLine(created.authorized_keys);
            setVersion((value) => value + 1);
            event.currentTarget.reset();
          } catch (caught) {
            setError((caught as Error).message);
          }
        }}
      >
        <label>
          Title
          <input name="title" required maxLength={80} />
        </label>
        <label>
          Public key
          <textarea name="public_key" required rows={4} />
        </label>
        <button className="button primary">Add SSH key</button>
      </form>
      {keys.loading ? (
        <Loading />
      ) : (
        <ul>
          {(keys.data?.items || []).map((item) => (
            <li key={item.id}>
              {item.title} <code>{item.fingerprint}</code>{" "}
              <button
                className="text-button"
                onClick={async () => {
                  setError("");
                  try {
                    await remove(`/user/ssh-keys/${item.id}`);
                    setVersion((value) => value + 1);
                  } catch (caught) {
                    setError((caught as Error).message);
                  }
                }}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function DropsPanel({ endpoint, canWrite }: { endpoint: string; canWrite: boolean }) {
  const [version, setVersion] = useState(0);
  const [tag, setTag] = useState("");
  const [error, setError] = useState("");
  const drops = useData<{ items: DropItem[] }>(`${endpoint}/drops`, version);
  const detail = useData<{
    title: string;
    body: string;
    provenance: string;
    provenance_verified: boolean;
    draft: boolean;
    assets: DropAsset[];
    changelog: string[];
  }>(tag ? `${endpoint}/drops/${encodeURIComponent(tag)}` : null, version);
  return (
    <section>
      <p className="muted small-text">
        A drop is a release for a Git tag that already exists. Provenance is publisher-supplied text and is not a verified signature.
      </p>
      <ErrorMessage error={error || drops.error || detail.error} />
      {canWrite && (
        <form
          className="panel form-panel"
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            setError("");
            try {
              const created = await post<{ tag: string }>(`${endpoint}/drops`, {
                tag: String(data.get("tag") || ""),
                title: String(data.get("title") || ""),
                provenance: String(data.get("provenance") || ""),
              });
              setTag(created.tag);
              setVersion((value) => value + 1);
            } catch (caught) {
              setError((caught as Error).message);
            }
          }}
        >
          <label>
            Existing tag
            <input name="tag" required />
          </label>
          <label>
            Title
            <input name="title" required maxLength={200} />
          </label>
          <label>
            Provenance note
            <input name="provenance" maxLength={4000} placeholder="Publisher-supplied. Not verified." />
          </label>
          <button className="button primary">Publish drop</button>
        </form>
      )}
      {drops.loading ? (
        <Loading />
      ) : (
        <ul>
          {(drops.data?.items || []).map((item) => (
            <li key={item.tag}>
              <button className="text-button" onClick={() => setTag(item.tag)}>
                {item.tag}
              </button>{" "}
              {item.title}
              {item.prerelease ? " · prerelease" : ""}
              {item.draft ? " · draft" : ""}
            </li>
          ))}
        </ul>
      )}
      {detail.data && tag && (
        <article className="panel">
          <h2>{detail.data.title}</h2>
          <p>{detail.data.provenance_verified ? "Verified" : "Provenance is not verified."}</p>
          {detail.data.provenance && <p>{detail.data.provenance}</p>}
          <pre>{detail.data.body}</pre>
          <ul>
            {(detail.data.assets || []).map((asset) => (
              <li key={asset.name}>
                <a href={`/api/v1${endpoint}/drops/${encodeURIComponent(tag)}/assets/${encodeURIComponent(asset.name)}`}>
                  {asset.name}
                </a>{" "}
                <span className="muted">
                  {asset.download_count} downloads · {asset.sha256}
                </span>
                {canWrite && (
                  <button
                    className="text-button"
                    onClick={async () => {
                      setError("");
                      try {
                        await remove(`${endpoint}/drops/${encodeURIComponent(tag)}/assets/${encodeURIComponent(asset.name)}`);
                        setVersion((value) => value + 1);
                      } catch (caught) {
                        setError((caught as Error).message);
                      }
                    }}
                  >
                    Remove
                  </button>
                )}
              </li>
            ))}
          </ul>
          {canWrite && (
            <form
              onSubmit={async (event) => {
                event.preventDefault();
                const data = new FormData(event.currentTarget);
                const file = data.get("file");
                if (!(file instanceof File) || file.size === 0) return;
                setError("");
                const response = await fetch(
                  `/api/v1${endpoint}/drops/${encodeURIComponent(tag)}/assets?name=${encodeURIComponent(file.name)}`,
                  {
                    method: "POST",
                    credentials: "same-origin",
                    headers: { "Content-Type": "application/octet-stream" },
                    body: file,
                  },
                );
                if (!response.ok) {
                  const payload = await response.json().catch(() => null);
                  setError(payload?.error?.message || `Upload failed (${response.status}).`);
                  return;
                }
                setVersion((value) => value + 1);
              }}
            >
              <input name="file" type="file" />
              <button className="button">Upload asset</button>
            </form>
          )}
          {canWrite && (
            <button
              className="text-button"
              onClick={async () => {
                setError("");
                try {
                  await put(`${endpoint}/drops/${encodeURIComponent(tag)}`, { draft: false });
                  setVersion((value) => value + 1);
                } catch (caught) {
                  setError((caught as Error).message);
                }
              }}
            >
              Mark published
            </button>
          )}
        </article>
      )}
    </section>
  );
}
