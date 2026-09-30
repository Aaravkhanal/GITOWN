"use client";

import Link from "next/link";
import { useState } from "react";
import { patch, post, put, remove, repoPath, type Repo } from "@/lib/api";
import { ErrorMessage, Loading, useData } from "@/components/ui";
import { BoardView } from "@/components/board";

type Collection = {
  owner: string;
  slug: string;
  title: string;
  repositories: number;
  featured?: boolean;
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
  owner?: string;
  repo_creation: string;
  base_permission: string;
  allow_public?: boolean;
  allow_outside_collaborators?: boolean;
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
  const [helpOffset, setHelpOffset] = useState(0);
  const tasks = useData<{ items: Task[]; has_more: boolean }>(
    `/search/tasks?kind=help&offset=${helpOffset}`,
  );
  const [firstOffset, setFirstOffset] = useState(0);
  const firstTasks = useData<{ items: Task[]; has_more: boolean }>(
    `/search/tasks?kind=first&offset=${firstOffset}`,
  );
  const [collectionsOffset, setCollectionsOffset] = useState(0);
  const collections = useData<{ items: Collection[]; has_more: boolean }>(
    `/collections?offset=${collectionsOffset}`,
  );
  const featured = useData<{ items: Collection[] }>("/collections?featured=1");
  const [topicsOffset, setTopicsOffset] = useState(0);
  const topics = useData<{
    items: { topic: string; repositories: number }[];
    has_more: boolean;
  }>(`/topics?offset=${topicsOffset}`);
  const [query, setQuery] = useState("");
  const [codePath, setCodePath] = useState<string | null>(null);
  const code = useData<{
    ranked?: boolean;
    items: {
      owner: string;
      repository: string;
      path: string;
      line: number;
      snippet: string;
      rank?: number;
    }[];
  }>(codePath);
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
                {repo.language ? (
                  <span className="muted"> {repo.language}</span>
                ) : null}
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
        {(helpOffset > 0 || tasks.data?.has_more) && (
          <div className="form-actions">
            <button
              className="button small-button"
              disabled={helpOffset === 0}
              onClick={() => setHelpOffset(Math.max(0, helpOffset - 25))}
            >
              Previous
            </button>
            <button
              className="button small-button"
              disabled={!tasks.data?.has_more}
              onClick={() => setHelpOffset(helpOffset + 25)}
            >
              Next
            </button>
          </div>
        )}
        <h2>Good first task</h2>
        <ErrorMessage error={firstTasks.error} />
        {firstTasks.loading ? (
          <Loading />
        ) : (
          <ul>
            {(firstTasks.data?.items || []).map((task) => (
              <li key={`${task.owner}/${task.repository}#${task.number}`}>
                <Link href={`/repos/${task.owner}/${task.repository}/issues`}>
                  {task.owner}/{task.repository}#{task.number}
                </Link>{" "}
                {task.title}
              </li>
            ))}
          </ul>
        )}
        {(firstOffset > 0 || firstTasks.data?.has_more) && (
          <div className="form-actions">
            <button
              className="button small-button"
              disabled={firstOffset === 0}
              onClick={() => setFirstOffset(Math.max(0, firstOffset - 25))}
            >
              Previous
            </button>
            <button
              className="button small-button"
              disabled={!firstTasks.data?.has_more}
              onClick={() => setFirstOffset(firstOffset + 25)}
            >
              Next
            </button>
          </div>
        )}
        <h2>Featured collections</h2>
        <p className="muted small-text">
          Operators mark a collection as featured. These are public repository
          lists, not a ranking model.
        </p>
        <ErrorMessage error={featured.error} />
        <ul>
          {(featured.data?.items || []).map((item) => (
            <li key={`featured-${item.owner}/${item.slug}`}>
              <Link href={`/collections/${item.owner}/${item.slug}`}>
                {item.owner}/{item.slug}: {item.title}
              </Link>{" "}
              ({item.repositories})
            </li>
          ))}
        </ul>
        <h2>Topics</h2>
        <ErrorMessage error={topics.error} />
        <ul>
          {(topics.data?.items || []).map((item) => (
            <li key={item.topic}>
              <Link href={`/topics/${item.topic}`}>{item.topic}</Link>{" "}
              <span className="muted">{item.repositories}</span>
            </li>
          ))}
        </ul>
        {(topicsOffset > 0 || topics.data?.has_more) && (
          <div className="form-actions">
            <button
              className="button small-button"
              disabled={topicsOffset === 0}
              onClick={() => setTopicsOffset(Math.max(0, topicsOffset - 50))}
            >
              Previous
            </button>
            <button
              className="button small-button"
              disabled={!topics.data?.has_more}
              onClick={() => setTopicsOffset(topicsOffset + 50)}
            >
              Next
            </button>
          </div>
        )}
        <h2>Community collections</h2>
        <p className="muted small-text">
          <Link href="/collections">Create or browse collections</Link>
        </p>
        <ErrorMessage error={collections.error} />
        <ul>
          {(collections.data?.items || []).map((item) => (
            <li key={`${item.owner}/${item.slug}`}>
              <Link href={`/collections/${item.owner}/${item.slug}`}>
                {item.owner}/{item.slug}: {item.title}
              </Link>{" "}
              ({item.repositories}){item.featured ? " · featured" : ""}
            </li>
          ))}
        </ul>
        {(collectionsOffset > 0 || collections.data?.has_more) && (
          <div className="form-actions">
            <button
              className="button small-button"
              disabled={collectionsOffset === 0}
              onClick={() =>
                setCollectionsOffset(Math.max(0, collectionsOffset - 20))
              }
            >
              Previous
            </button>
            <button
              className="button small-button"
              disabled={!collections.data?.has_more}
              onClick={() => setCollectionsOffset(collectionsOffset + 20)}
            >
              Next
            </button>
          </div>
        )}
        <form
          onSubmit={(event) => {
            event.preventDefault();
            if (query.trim().length >= 2)
              setCodePath(`/search/code?q=${encodeURIComponent(query.trim())}`);
          }}
        >
          <label>
            Search public code
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Public text, 2–80 characters"
            />
          </label>
          <button className="button" type="submit">
            Search code
          </button>
        </form>
        {code.data?.ranked ? (
          <p className="muted small-text">
            Ranked from the capped public text index.
          </p>
        ) : code.data ? (
          <p className="muted small-text">
            Shown from a git grep of recent public repositories. The ranked
            index had no match.
          </p>
        ) : null}
        <ErrorMessage error={code.error} />
        <ul>
          {(code.data?.items || []).map((item) => (
            <li
              key={`${item.owner}/${item.repository}/${item.path}:${item.line}`}
            >
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
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <div className="form-page">
      <h1>Districts</h1>
      <p className="page-description">
        A district is an organization. Crews are teams inside it. Members
        inherit the district base permission on its repositories.
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
            await post<District>("/districts", {
              slug: String(data.get("slug") || ""),
              name: String(data.get("name") || ""),
              visibility: String(data.get("visibility") || "public"),
            });
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
              <Link href={`/districts/${item.slug}`}>{item.name}</Link>{" "}
              <span className="muted">
                {item.slug} · {item.visibility}
                {item.role ? ` · ${item.role}` : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// DistrictDetail is the district's own linkable page: a public district
// renders for any visitor, and a private one returns the same "not found"
// the API gives a non-member so its existence isn't revealed.
export function DistrictDetail({
  slug,
  currentUsername,
}: {
  slug: string;
  currentUsername?: string;
}) {
  const [version, setVersion] = useState(0);
  const district = useData<District>(
    `/districts/${encodeURIComponent(slug)}`,
    version,
  );
  const repos = useData<{ items: Repo[] }>(
    `/districts/${encodeURIComponent(slug)}/repos`,
    version,
  );
  if (district.loading) return <Loading />;
  if (!district.data)
    return (
      <ErrorMessage
        error={
          district.error ||
          "District not found, or it is private and you are not a member."
        }
      />
    );
  const current = district.data;
  const role = current.role;
  const canAdmin = role === "owner" || role === "admin";
  const refresh = () => setVersion((value) => value + 1);
  return (
    <div className="form-page">
      <div className="page-heading">
        <div>
          <h1>{current.name}</h1>
          <p className="page-description">
            {slug} · {current.visibility}
            {current.owner ? ` · owned by ${current.owner}` : ""}
            {role ? ` · you are ${role}` : ""}
          </p>
          {current.description && <p>{current.description}</p>}
        </div>
      </div>
      <section className="panel">
        <h2>Repositories</h2>
        <ErrorMessage error={repos.error} />
        {repos.loading ? (
          <Loading />
        ) : repos.data?.items.length ? (
          <ul>
            {repos.data.items.map((repo) => (
              <li key={repo.id}>
                <Link href={repoPath(repo)}>
                  {repo.owner}/{repo.name}
                </Link>{" "}
                <span className="muted">{repo.visibility}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">No repositories yet.</p>
        )}
      </section>
      <DistrictBoard slug={slug} />
      {role && (
        <>
          <DistrictMembers
            slug={slug}
            role={role}
            currentUsername={currentUsername}
            onChange={refresh}
          />
          <DistrictCrews slug={slug} canAdmin={canAdmin} />
        </>
      )}
      {canAdmin && (
        <>
          <DistrictAdmin slug={slug} onChange={refresh} />
          <DistrictSecrets slug={slug} />
        </>
      )}
      {role && <DistrictControls slug={slug} />}
    </div>
  );
}

function DistrictMembers({
  slug,
  role,
  currentUsername,
  onChange,
}: {
  slug: string;
  role: string;
  currentUsername?: string;
  onChange: () => void;
}) {
  const [version, setVersion] = useState(0);
  const members = useData<{
    items: { username: string; display_name: string; role: string }[];
  }>(`/districts/${encodeURIComponent(slug)}/members`, version);
  const [error, setError] = useState("");
  const canAdmin = role === "owner" || role === "admin";
  const refresh = () => {
    setVersion((value) => value + 1);
    onChange();
  };
  return (
    <section className="panel">
      <h2>Members</h2>
      <ErrorMessage error={error || members.error} />
      {members.loading ? (
        <Loading />
      ) : members.data?.items.length ? (
        <ul>
          {members.data.items.map((member) => (
            <li key={member.username}>
              {member.display_name || member.username} · {member.role}
              {canAdmin && member.username !== currentUsername && (
                <>
                  {" "}
                  {role === "owner" && member.role === "member" && (
                    <button
                      className="text-button"
                      onClick={async () => {
                        setError("");
                        try {
                          await post(
                            `/districts/${encodeURIComponent(slug)}/members`,
                            { username: member.username, role: "admin" },
                          );
                          refresh();
                        } catch (caught) {
                          setError((caught as Error).message);
                        }
                      }}
                    >
                      Make admin
                    </button>
                  )}
                  {member.role === "admin" && (
                    <button
                      className="text-button"
                      onClick={async () => {
                        setError("");
                        try {
                          await post(
                            `/districts/${encodeURIComponent(slug)}/members`,
                            { username: member.username, role: "member" },
                          );
                          refresh();
                        } catch (caught) {
                          setError((caught as Error).message);
                        }
                      }}
                    >
                      Remove admin
                    </button>
                  )}{" "}
                  <button
                    className="text-button"
                    onClick={async () => {
                      setError("");
                      try {
                        await remove(
                          `/districts/${encodeURIComponent(slug)}/members/${encodeURIComponent(member.username)}`,
                        );
                        refresh();
                      } catch (caught) {
                        setError((caught as Error).message);
                      }
                    }}
                  >
                    Remove
                  </button>
                </>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No members yet.</p>
      )}
    </section>
  );
}

function DistrictCrews({
  slug,
  canAdmin,
}: {
  slug: string;
  canAdmin: boolean;
}) {
  const [version, setVersion] = useState(0);
  const crews = useData<{
    items: {
      slug: string;
      name: string;
      description: string;
      members: number;
    }[];
  }>(`/districts/${encodeURIComponent(slug)}/crews`, version);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [error, setError] = useState("");
  const refresh = () => setVersion((value) => value + 1);
  return (
    <section className="panel">
      <h2>Crews</h2>
      <ErrorMessage error={error || crews.error} />
      {crews.loading ? (
        <Loading />
      ) : crews.data?.items.length ? (
        <ul>
          {crews.data.items.map((crew) => (
            <li key={crew.slug}>
              <button
                className="text-button"
                onClick={() =>
                  setExpanded(expanded === crew.slug ? null : crew.slug)
                }
              >
                {crew.name}
              </button>{" "}
              <span className="muted">
                {crew.slug} · {crew.members} member
                {crew.members === 1 ? "" : "s"}
              </span>
              {canAdmin && (
                <>
                  {" "}
                  <button
                    className="text-button"
                    onClick={async () => {
                      setError("");
                      try {
                        await remove(
                          `/districts/${encodeURIComponent(slug)}/crews/${encodeURIComponent(crew.slug)}`,
                        );
                        if (expanded === crew.slug) setExpanded(null);
                        refresh();
                      } catch (caught) {
                        setError((caught as Error).message);
                      }
                    }}
                  >
                    Delete crew
                  </button>
                </>
              )}
              {expanded === crew.slug && (
                <CrewMembers
                  slug={slug}
                  crew={crew.slug}
                  canAdmin={canAdmin}
                  onChange={refresh}
                />
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No crews yet.</p>
      )}
    </section>
  );
}

function CrewMembers({
  slug,
  crew,
  canAdmin,
  onChange,
}: {
  slug: string;
  crew: string;
  canAdmin: boolean;
  onChange: () => void;
}) {
  const [version, setVersion] = useState(0);
  const members = useData<{
    items: { username: string; display_name: string }[];
  }>(
    `/districts/${encodeURIComponent(slug)}/crews/${encodeURIComponent(crew)}/members`,
    version,
  );
  const [error, setError] = useState("");
  const refresh = () => {
    setVersion((value) => value + 1);
    onChange();
  };
  return (
    <div className="panel">
      <ErrorMessage error={error || members.error} />
      {members.loading ? (
        <Loading />
      ) : members.data?.items.length ? (
        <ul>
          {members.data.items.map((member) => (
            <li key={member.username}>
              {member.display_name || member.username}
              {canAdmin && (
                <>
                  {" "}
                  <button
                    className="text-button"
                    onClick={async () => {
                      setError("");
                      try {
                        await remove(
                          `/districts/${encodeURIComponent(slug)}/crews/${encodeURIComponent(crew)}/members/${encodeURIComponent(member.username)}`,
                        );
                        refresh();
                      } catch (caught) {
                        setError((caught as Error).message);
                      }
                    }}
                  >
                    Remove
                  </button>
                </>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No crew members yet.</p>
      )}
      {canAdmin && (
        <form
          className="inline-form"
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            setError("");
            try {
              await post(
                `/districts/${encodeURIComponent(slug)}/crews/${encodeURIComponent(crew)}/members`,
                { username: String(data.get("username") || "") },
              );
              event.currentTarget.reset();
              refresh();
            } catch (caught) {
              setError((caught as Error).message);
            }
          }}
        >
          <input name="username" placeholder="builder" required />
          <button className="button small-button">Add to crew</button>
        </form>
      )}
    </div>
  );
}

function DistrictSecrets({ slug }: { slug: string }) {
  const [version, setVersion] = useState(0);
  const secrets = useData<{ items: { name: string; created_at: string }[] }>(
    `/districts/${encodeURIComponent(slug)}/secrets`,
    version,
  );
  const [error, setError] = useState("");
  const refresh = () => setVersion((value) => value + 1);
  return (
    <section className="panel">
      <h2>Secrets</h2>
      <p className="muted small-text">
        Encrypted at rest and never shown again after they are stored. Stored
        for future use by Routes automation; nothing in GITOWN consumes them
        yet.
      </p>
      <ErrorMessage error={error || secrets.error} />
      {secrets.loading ? (
        <Loading />
      ) : secrets.data?.items.length ? (
        <ul>
          {secrets.data.items.map((item) => (
            <li key={item.name}>
              {item.name}{" "}
              <button
                className="text-button"
                onClick={async () => {
                  setError("");
                  try {
                    await remove(
                      `/districts/${encodeURIComponent(slug)}/secrets/${encodeURIComponent(item.name)}`,
                    );
                    refresh();
                  } catch (caught) {
                    setError((caught as Error).message);
                  }
                }}
              >
                Delete
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No secrets yet.</p>
      )}
      <form
        className="inline-form"
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setError("");
          try {
            await post(`/districts/${encodeURIComponent(slug)}/secrets`, {
              name: String(data.get("name") || ""),
              value: String(data.get("value") || ""),
            });
            event.currentTarget.reset();
            refresh();
          } catch (caught) {
            setError((caught as Error).message);
          }
        }}
      >
        <input name="name" placeholder="SECRET_NAME" required />
        <input name="value" placeholder="value" type="password" required />
        <button className="button small-button">Store secret</button>
      </form>
    </section>
  );
}

function DistrictBoard({ slug }: { slug: string }) {
  const [open, setOpen] = useState(false);
  const district = useData<District>(
    open ? `/districts/${encodeURIComponent(slug)}` : null,
  );
  return (
    <section aria-label="District board">
      <button
        className="button small-button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {open ? "Hide district board" : "Show district board"}
      </button>
      {open && (
        <BoardView
          key={slug}
          district={encodeURIComponent(slug)}
          canTriage={!!district.data?.role}
        />
      )}
    </section>
  );
}

function DistrictControls({ slug }: { slug: string }) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const district = useData<District>(
    `/districts/${encodeURIComponent(slug)}`,
    version,
  );
  const role = district.data?.role;
  const canAdmin = role === "owner" || role === "admin";
  const usage = useData<{
    repositories: number;
    storage_bytes: number;
    members: number;
    secrets: number;
    open_invoices: number;
    charges: boolean;
  }>(canAdmin ? `/districts/${encodeURIComponent(slug)}/usage` : null, version);
  const invoices = useData<{
    charges: boolean;
    items: {
      id: string;
      period: string;
      amount_cents: number;
      status: string;
    }[];
  }>(
    canAdmin ? `/districts/${encodeURIComponent(slug)}/invoices` : null,
    version,
  );
  if (district.loading) return <Loading />;
  if (!district.data)
    return <ErrorMessage error={district.error || "District unavailable."} />;
  const current = district.data;
  return (
    <div>
      <h3>Policy</h3>
      <p className="muted small-text">
        Public repositories are {current.allow_public ? "allowed" : "blocked"}.
        Collaborators from outside the district are{" "}
        {current.allow_outside_collaborators ? "allowed" : "blocked"}.
        Repository creation is limited to {current.repo_creation}. Members
        receive {current.base_permission} on district repositories.
      </p>
      <ErrorMessage error={error || usage.error || invoices.error} />
      {current.role === "owner" && (
        <form
          className="form-panel"
          key={`${current.description}-${current.visibility}-${current.allow_public}-${current.allow_outside_collaborators}-${current.repo_creation}-${current.base_permission}`}
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            setError("");
            try {
              await patch(`/districts/${encodeURIComponent(slug)}`, {
                description: String(
                  data.get("description") ?? current.description,
                ),
                visibility: String(
                  data.get("visibility") || current.visibility,
                ),
                allow_public: data.get("allow_public") === "on",
                allow_outside_collaborators: data.get("allow_outside") === "on",
                repo_creation: String(
                  data.get("repo_creation") || current.repo_creation,
                ),
                base_permission: String(
                  data.get("base_permission") || current.base_permission,
                ),
              });
              setVersion((value) => value + 1);
            } catch (caught) {
              setError((caught as Error).message);
            }
          }}
        >
          <label>
            Description
            <textarea
              name="description"
              maxLength={500}
              rows={3}
              defaultValue={current.description}
            />
          </label>
          <label>
            Visibility
            <select name="visibility" defaultValue={current.visibility}>
              <option value="public">Public</option>
              <option value="private">Private</option>
            </select>
          </label>
          <label className="checkbox-label">
            <input
              name="allow_public"
              type="checkbox"
              defaultChecked={current.allow_public !== false}
            />
            <span>Allow public repositories</span>
          </label>
          <label className="checkbox-label">
            <input
              name="allow_outside"
              type="checkbox"
              defaultChecked={current.allow_outside_collaborators !== false}
            />
            <span>Allow collaborators from outside the district</span>
          </label>
          <label>
            Who can create repositories
            <select
              name="repo_creation"
              defaultValue={current.repo_creation || "admin"}
            >
              <option value="owner">Owner</option>
              <option value="admin">Admins</option>
              <option value="member">Members</option>
            </select>
          </label>
          <label>
            Base permission
            <select
              name="base_permission"
              defaultValue={current.base_permission || "read"}
            >
              <option value="none">None</option>
              <option value="read">Read</option>
              <option value="triage">Triage</option>
              <option value="write">Write</option>
            </select>
          </label>
          <button className="button">Save policy</button>
        </form>
      )}
      {canAdmin && (
        <section>
          <h3>Usage and invoices</h3>
          <p className="muted small-text">
            This ledger records amounts an operator enters. GITOWN does not
            charge a card.
            {usage.data
              ? ` ${usage.data.repositories} repositories, ${usage.data.storage_bytes} bytes, ${usage.data.members} members, ${usage.data.secrets} secrets, ${usage.data.open_invoices} open invoices.`
              : ""}
          </p>
          <p>
            <a
              className="button"
              href={`/api/v1/districts/${encodeURIComponent(slug)}/audit?download=1`}
            >
              Download audit CSV
            </a>
          </p>
          <ul>
            {(invoices.data?.items || []).map((item) => (
              <li key={item.id}>
                {item.period} · {item.amount_cents} cents · {item.status}
                {item.status === "open" && (
                  <button
                    className="text-button"
                    onClick={async () => {
                      setError("");
                      try {
                        await post(
                          `/districts/${encodeURIComponent(slug)}/invoices/${item.id}/pay`,
                          {},
                        );
                        setVersion((value) => value + 1);
                      } catch (caught) {
                        setError((caught as Error).message);
                      }
                    }}
                  >
                    Mark paid
                  </button>
                )}
              </li>
            ))}
          </ul>
          <form
            className="form-panel"
            onSubmit={async (event) => {
              event.preventDefault();
              const data = new FormData(event.currentTarget);
              setError("");
              try {
                await post(`/districts/${encodeURIComponent(slug)}/invoices`, {
                  period: String(data.get("period") || ""),
                  amount_cents: Number(data.get("amount_cents") || 0),
                });
                setVersion((value) => value + 1);
                event.currentTarget.reset();
              } catch (caught) {
                setError((caught as Error).message);
              }
            }}
          >
            <p className="muted small-text">
              Recording or paying an invoice requires your username in
              GITOWN_OPERATORS.
            </p>
            <label>
              Period
              <input
                name="period"
                required
                pattern="[0-9]{4}-[0-9]{2}"
                placeholder="YYYY-MM"
              />
            </label>
            <label>
              Amount in cents
              <input
                name="amount_cents"
                type="number"
                min={0}
                max={100000000}
                required
                defaultValue={0}
              />
            </label>
            <button className="button">Record invoice</button>
          </form>
        </section>
      )}
    </div>
  );
}

export function TopicsPage({ topic }: { topic?: string }) {
  const [catalogOffset, setCatalogOffset] = useState(0);
  const catalog = useData<{
    items: { topic: string; repositories: number }[];
    has_more: boolean;
  }>(topic ? null : `/topics?offset=${catalogOffset}`);
  const [pageOffset, setPageOffset] = useState(0);
  const page = useData<{ topic: string; items: Repo[]; has_more: boolean }>(
    topic ? `/topics/${encodeURIComponent(topic)}?offset=${pageOffset}` : null,
  );
  return (
    <div className="form-page">
      <h1>{topic ? `Topic · ${topic}` : "Topics"}</h1>
      <p className="page-description">
        Topics group public repositories. A topic page lists the public
        repositories that carry that label.
      </p>
      <ErrorMessage error={catalog.error || page.error} />
      {topic ? (
        page.loading ? (
          <Loading />
        ) : (
          <>
            <ul>
              {(page.data?.items || []).map((repo) => (
                <li key={repo.id}>
                  <Link href={repoPath(repo)}>
                    {repo.owner}/{repo.name}
                  </Link>
                  {repo.language ? (
                    <span className="muted"> {repo.language}</span>
                  ) : null}
                </li>
              ))}
            </ul>
            {(pageOffset > 0 || page.data?.has_more) && (
              <div className="form-actions">
                <button
                  className="button"
                  disabled={pageOffset === 0}
                  onClick={() => setPageOffset(Math.max(0, pageOffset - 30))}
                >
                  Previous
                </button>
                <button
                  className="button"
                  disabled={!page.data?.has_more}
                  onClick={() => setPageOffset(pageOffset + 30)}
                >
                  Next
                </button>
              </div>
            )}
          </>
        )
      ) : catalog.loading ? (
        <Loading />
      ) : (
        <>
          <ul>
            {(catalog.data?.items || []).map((item) => (
              <li key={item.topic}>
                <Link href={`/topics/${item.topic}`}>{item.topic}</Link>{" "}
                <span className="muted">{item.repositories} repositories</span>
              </li>
            ))}
          </ul>
          {(catalogOffset > 0 || catalog.data?.has_more) && (
            <div className="form-actions">
              <button
                className="button"
                disabled={catalogOffset === 0}
                onClick={() =>
                  setCatalogOffset(Math.max(0, catalogOffset - 50))
                }
              >
                Previous
              </button>
              <button
                className="button"
                disabled={!catalog.data?.has_more}
                onClick={() => setCatalogOffset(catalogOffset + 50)}
              >
                Next
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

type CollectionDetailData = {
  owner: string;
  slug: string;
  title: string;
  description: string;
  created_at: string;
  items: Repo[];
};

export function CollectionsPage({
  currentUsername,
}: {
  currentUsername?: string;
}) {
  const [version, setVersion] = useState(0);
  const [offset, setOffset] = useState(0);
  const collections = useData<{ items: Collection[]; has_more: boolean }>(
    `/collections?offset=${offset}`,
    version,
  );
  const [error, setError] = useState("");
  return (
    <div className="form-page">
      <h1>Collections</h1>
      <p className="page-description">
        A collection is a curated, public list of repositories. Anyone signed in
        can start one; an operator can mark a collection as featured so it
        appears on the Explore page.
      </p>
      {currentUsername && (
        <form
          className="panel form-panel"
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            setError("");
            try {
              await post<{ owner: string; slug: string }>("/collections", {
                slug: String(data.get("slug") || ""),
                title: String(data.get("title") || ""),
                description: String(data.get("description") || ""),
              });
              setVersion((value) => value + 1);
              event.currentTarget.reset();
            } catch (caught) {
              setError((caught as Error).message);
            }
          }}
        >
          <ErrorMessage error={error} />
          <label>
            Slug
            <input name="slug" required pattern="[a-z0-9][a-z0-9-]{0,38}" />
          </label>
          <label>
            Title
            <input name="title" required maxLength={80} />
          </label>
          <label>
            Description
            <input name="description" maxLength={300} />
          </label>
          <button className="button primary">Create collection</button>
        </form>
      )}
      <ErrorMessage error={collections.error} />
      {collections.loading ? (
        <Loading />
      ) : (
        <ul>
          {(collections.data?.items || []).map((item) => (
            <li key={`${item.owner}/${item.slug}`}>
              <Link href={`/collections/${item.owner}/${item.slug}`}>
                {item.owner}/{item.slug}: {item.title}
              </Link>{" "}
              <span className="muted">
                {item.repositories} repositories
                {item.featured ? " · featured" : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
      {(offset > 0 || collections.data?.has_more) && (
        <div className="form-actions">
          <button
            className="button"
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - 20))}
          >
            Previous
          </button>
          <button
            className="button"
            disabled={!collections.data?.has_more}
            onClick={() => setOffset(offset + 20)}
          >
            Next
          </button>
        </div>
      )}
    </div>
  );
}

export function CollectionDetail({
  owner,
  slug,
  currentUsername,
}: {
  owner: string;
  slug: string;
  currentUsername?: string;
}) {
  const [version, setVersion] = useState(0);
  const detail = useData<CollectionDetailData>(
    `/collections/${encodeURIComponent(owner)}/${encodeURIComponent(slug)}`,
    version,
  );
  const [error, setError] = useState("");
  const isCurator = currentUsername === owner;
  if (detail.loading) return <Loading />;
  if (!detail.data)
    return <ErrorMessage error={detail.error || "Collection not found."} />;
  return (
    <div className="form-page">
      <h1>{detail.data.title}</h1>
      <p className="page-description">
        {owner}/{slug}
        {detail.data.description ? ` · ${detail.data.description}` : ""}
      </p>
      <ErrorMessage error={error} />
      {isCurator && (
        <form
          className="panel form-panel"
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            setError("");
            try {
              await post(
                `/collections/${encodeURIComponent(owner)}/${encodeURIComponent(slug)}/items`,
                { repository: String(data.get("repository") || "") },
              );
              setVersion((value) => value + 1);
              event.currentTarget.reset();
            } catch (caught) {
              setError((caught as Error).message);
            }
          }}
        >
          <label>
            Add a public repository (owner/name)
            <input name="repository" required placeholder="owner/name" />
          </label>
          <button className="button primary">Add to collection</button>
        </form>
      )}
      <ul>
        {detail.data.items.map((repo) => (
          <li key={repo.id}>
            <Link href={repoPath(repo)}>
              {repo.owner}/{repo.name}
            </Link>
            {repo.language ? (
              <span className="muted"> {repo.language}</span>
            ) : null}
            {isCurator && (
              <button
                className="text-button"
                onClick={async () => {
                  setError("");
                  try {
                    await remove(
                      `/collections/${encodeURIComponent(owner)}/${encodeURIComponent(slug)}/items/${encodeURIComponent(repo.owner)}/${encodeURIComponent(repo.name)}`,
                    );
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
      {!detail.data.items.length && (
        <p className="muted">No repositories in this collection yet.</p>
      )}
    </div>
  );
}

function DistrictAdmin({
  slug,
  onChange,
}: {
  slug: string;
  onChange: () => void;
}) {
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
        A crate is a package record. Unscoped npm publish, packument, and
        tarball requests are served at /npm. OCI blob and manifest requests are
        served at /v2. Publishing checks a fixed pattern list: private-key
        headers, token prefixes, and a few dangerous command strings. That list
        is not a malware engine, and these endpoints are not a full npm registry
        or a container registry.
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
          <input
            name="retention"
            type="number"
            min={1}
            max={100}
            defaultValue={20}
          />
        </label>
        <button className="button primary">Create crate</button>
      </form>
      {crates.loading ? (
        <Loading />
      ) : (
        <ul>
          {(crates.data?.items || []).map((item) => (
            <li key={item.name}>
              {item.owner}/{item.name} · {item.visibility} · keep{" "}
              {item.retention}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function SSHKeysPage() {
  const [version, setVersion] = useState(0);
  const [signingVersion, setSigningVersion] = useState(0);
  const keys = useData<{ items: SSHKey[] }>("/user/ssh-keys", version);
  const signing = useData<{ items: SSHKey[] }>(
    "/user/signing-keys",
    signingVersion,
  );
  const [error, setError] = useState("");
  const [line, setLine] = useState("");
  return (
    <div className="form-page">
      <h1>SSH keys</h1>
      <p className="page-description">
        Add a public key, then install the authorized_keys line on an sshd that
        forces <code>gitown ssh-shell</code>. GITOWN does not embed an SSH
        server. The gateway trusts the fingerprint argument supplied by that
        forced command.
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
            const created = await post<{ authorized_keys: string }>(
              "/user/ssh-keys",
              {
                title: String(data.get("title") || ""),
                public_key: String(data.get("public_key") || ""),
              },
            );
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
      <h2>Signing keys</h2>
      <p className="page-description">
        A signing key verifies drop provenance with ssh-keygen. The same public
        key may also be an authentication key. Verification uses the keys you
        register here. It is not Sigstore and it does not consult a global
        keyring.
      </p>
      <ErrorMessage error={signing.error} />
      <form
        className="panel form-panel"
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setError("");
          try {
            await post("/user/signing-keys", {
              title: String(data.get("title") || ""),
              public_key: String(data.get("public_key") || ""),
            });
            setSigningVersion((value) => value + 1);
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
        <button className="button primary">Add signing key</button>
      </form>
      {signing.loading ? (
        <Loading />
      ) : (
        <ul>
          {(signing.data?.items || []).map((item) => (
            <li key={item.id}>
              {item.title} <code>{item.fingerprint}</code>{" "}
              <button
                className="text-button"
                onClick={async () => {
                  setError("");
                  try {
                    await remove(`/user/signing-keys/${item.id}`);
                    setSigningVersion((value) => value + 1);
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

export function DropsPanel({
  endpoint,
  canWrite,
}: {
  endpoint: string;
  canWrite: boolean;
}) {
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
        A drop is a release. You can attach it to a tag that already exists, or
        create an annotated tag from a branch while publishing. If you supply an
        SSH signature, GITOWN verifies it against a signing key registered by
        the publisher and records that result. A note without a signature stays
        unverified. This is not Sigstore.
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
                signature: String(data.get("signature") || ""),
                branch: String(data.get("branch") || ""),
                create_tag: data.get("create_tag") === "on",
                draft: data.get("draft") === "on",
                prerelease: data.get("prerelease") === "on",
              });
              setTag(created.tag);
              setVersion((value) => value + 1);
            } catch (caught) {
              setError((caught as Error).message);
            }
          }}
        >
          <label>
            Tag
            <input name="tag" required />
          </label>
          <label>
            Title
            <input name="title" required maxLength={200} />
          </label>
          <label className="checkbox-label">
            <input name="create_tag" type="checkbox" />
            <span>
              Create the annotated tag from a branch if it does not exist
            </span>
          </label>
          <label>
            Branch
            <input name="branch" placeholder="Default branch" />
          </label>
          <label>
            Provenance note
            <input
              name="provenance"
              maxLength={4000}
              placeholder="Text that the signature covers"
            />
          </label>
          <label>
            SSH signature
            <textarea
              name="signature"
              rows={4}
              placeholder="Optional. Verified against your signing keys."
            />
          </label>
          <label className="checkbox-label">
            <input name="draft" type="checkbox" />
            <span>Draft</span>
          </label>
          <label className="checkbox-label">
            <input name="prerelease" type="checkbox" />
            <span>Prerelease</span>
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
          <p>
            {detail.data.provenance_verified
              ? "Signature verified against an SSH signing key registered by the publisher."
              : "Provenance is not verified. Add a signature and a registered SSH signing key to verify it."}
          </p>
          {detail.data.provenance && <p>{detail.data.provenance}</p>}
          <pre>{detail.data.body}</pre>
          <ul>
            {(detail.data.assets || []).map((asset) => (
              <li key={asset.name}>
                <a
                  href={`/api/v1${endpoint}/drops/${encodeURIComponent(tag)}/assets/${encodeURIComponent(asset.name)}`}
                >
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
                        await remove(
                          `${endpoint}/drops/${encodeURIComponent(tag)}/assets/${encodeURIComponent(asset.name)}`,
                        );
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
                  setError(
                    payload?.error?.message ||
                      `Upload failed (${response.status}).`,
                  );
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
                  await put(`${endpoint}/drops/${encodeURIComponent(tag)}`, {
                    draft: false,
                  });
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
