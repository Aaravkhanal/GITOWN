"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  ArrowLeft,
  ArrowRight,
  Archive,
  BookOpen,
  Check,
  ChevronRight,
  CircleDot,
  Code2,
  File,
  Folder,
  FolderGit2,
  GitBranch,
  GitCommitHorizontal,
  GitMerge,
  GitPullRequest,
  Globe2,
  History,
  LayoutGrid,
  LockKeyhole,
  Plus,
  Save,
  Settings,
  Sparkles,
  Terminal,
  Trash2,
  UserPlus,
  Users,
} from "lucide-react";
import {
  api,
  patch,
  post,
  put,
  remove,
  destroy,
  date,
  repoPath,
  type Repo,
  type SparkState,
  type TopicState,
  type Commit,
  type Tree,
  type Issue,
  type IssueTemplate,
  type IssueDependencies,
  type IssueComment,
  type IssueAssignees,
  type Milestone,
  type Label,
  type Pull,
  type PullDetail,
  type PullComment,
  type PullReview,
  type BranchRule,
  type RepositoryMember,
} from "@/lib/api";
import {
  Avatar,
  Badge,
  CopyButton,
  ErrorMessage,
  Loading,
  useData,
} from "./ui";
import { BoardView } from "./board";

export function RepositoryPage({
  owner,
  name,
  tab,
  number,
}: {
  owner: string;
  name: string;
  tab: string;
  number?: string;
}) {
  const endpoint = `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`;
  const [version, setVersion] = useState(0);
  const repo = useData<{ repository: Repo; branches: string[] }>(
    endpoint,
    version,
  );
  const [branch, setBranch] = useState("");
  const [path, setPath] = useState("");
  const [clone, setClone] = useState(false);
  useEffect(() => {
    if (repo.data && !branch)
      setBranch(
        repo.data.branches.includes(repo.data.repository.default_branch)
          ? repo.data.repository.default_branch
          : repo.data.branches[0] || "main",
      );
  }, [repo.data, branch]);
  if (repo.loading) return <Loading />;
  if (repo.error || !repo.data)
    return (
      <>
        <ErrorMessage error={repo.error} />
        <Link href="/" className="button">
          Back to workspace
        </Link>
      </>
    );
  const r = repo.data.repository;
  const basePath = repoPath(r);
  return (
    <>
      <div className="breadcrumb">
        <Link href="/">Workspace</Link>
        <ChevronRight size={12} />
        <Link href={`/u/${owner}`}>{owner}</Link>
        <ChevronRight size={12} />
        {name}
      </div>
      <div className="repository-heading">
        <span className="repo-large-icon">
          <FolderGit2 size={26} />
        </span>
        <div>
          <div className="repository-title">
            <h1>
              <span>{owner} / </span>
              {name}
            </h1>
            <Badge>
              {r.visibility === "private" ? (
                <LockKeyhole size={11} />
              ) : (
                <Globe2 size={11} />
              )}
              {r.visibility}
            </Badge>
            {r.archived && <Badge>archived</Badge>}
          </div>
          <p>{r.description || "A home for your next great idea."}</p>
        </div>
        <div className="toolbar-actions">
          <SparkButton endpoint={endpoint} />
          <button className="button" onClick={() => setClone(!clone)}>
            <Terminal size={16} />
            Clone repository
          </button>
        </div>
      </div>
      {r.archived && (
        <div className="archive-banner">
          <Archive size={17} /> This repository is archived and read-only.
        </div>
      )}
      <RepositoryTopics
        endpoint={endpoint}
        canManage={r.can_manage}
        archived={r.archived}
        settings={tab === "settings"}
      />
      {clone && (
        <div className="clone-panel panel">
          <div>
            <strong>
              Clone with HTTP{r.clone_url.startsWith("https") ? "S" : ""}
            </strong>
            <span className="muted small-text">
              Use an access token when Git asks for your password.
            </span>
          </div>
          <div className="copy-field">
            <code>git clone {r.clone_url}</code>
            <CopyButton text={`git clone ${r.clone_url}`} />
          </div>
          <Link href="/settings/tokens">
            Manage access tokens <ArrowRight size={13} />
          </Link>
        </div>
      )}
      <nav className="repo-tabs" aria-label="Repository navigation">
        {[
          { key: "code", icon: Code2, label: "Code", href: basePath },
          {
            key: "issues",
            icon: CircleDot,
            label: "Issues",
            href: `${basePath}/issues`,
          },
          {
            key: "board",
            icon: LayoutGrid,
            label: "Board",
            href: `${basePath}/board`,
          },
          {
            key: "pulls",
            icon: GitPullRequest,
            label: "Unite requests",
            href: `${basePath}/pulls`,
          },
          {
            key: "commits",
            icon: History,
            label: "Commits",
            href: `${basePath}/commits`,
          },
          ...(r.can_manage
            ? [
                {
                  key: "settings",
                  icon: Settings,
                  label: "Settings",
                  href: `${basePath}/settings`,
                },
              ]
            : []),
        ].map((item) => (
          <Link
            href={item.href}
            className={tab === item.key ? "active" : ""}
            key={item.key}
          >
            <item.icon size={17} />
            {item.label}
          </Link>
        ))}
      </nav>
      {(tab === "code" || tab === "commits") && (
        <div className="repo-toolbar">
          <label className="branch-select">
            <GitBranch size={16} />
            <select
              aria-label="Current branch"
              value={branch}
              onChange={(e) => {
                setBranch(e.target.value);
                setPath("");
              }}
            >
              {repo.data.branches.length ? (
                repo.data.branches.map((b) => <option key={b}>{b}</option>)
              ) : (
                <option>main</option>
              )}
            </select>
          </label>
          <span className="muted small-text">
            {repo.data.branches.length} branch
            {repo.data.branches.length !== 1 ? "es" : ""}
          </span>
          <span className="toolbar-spacer" />
          <button
            className="text-button"
            onClick={() => setVersion((v) => v + 1)}
          >
            Refresh
          </button>
        </div>
      )}
      {tab === "code" ? (
        repo.data.branches.length ? (
          <CodeBrowser
            endpoint={endpoint}
            repo={r}
            branch={branch}
            path={path}
            setPath={setPath}
          />
        ) : (
          <EmptyRepository repo={r} />
        )
      ) : tab === "commits" ? (
        <CommitList endpoint={endpoint} branch={branch} />
      ) : tab === "issues" ? (
        <IssueList
          endpoint={endpoint}
          canTriage={r.can_triage}
          canComment={r.can_comment}
        />
      ) : tab === "board" ? (
        <BoardView endpoint={endpoint} canTriage={r.can_triage} />
      ) : tab === "pulls" && number ? (
        <PullRequestDetail endpoint={endpoint} number={number} repo={r} />
      ) : tab === "pulls" ? (
        <PullRequestList
          endpoint={endpoint}
          repo={r}
          branches={repo.data.branches}
        />
      ) : tab === "settings" && r.can_manage ? (
        <RepositorySettings
          endpoint={endpoint}
          repo={r}
          branches={repo.data.branches}
        />
      ) : (
        <div className="empty-state">
          <h2>Page not found</h2>
          <Link href={basePath}>Back to code</Link>
        </div>
      )}
    </>
  );
}

function SparkButton({ endpoint }: { endpoint: string }) {
  const [version, setVersion] = useState(0);
  const state = useData<SparkState>(`${endpoint}/spark`, version);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function toggle() {
    if (!state.data || busy) return;
    setBusy(true);
    setError("");
    try {
      await put(`${endpoint}/spark`, { sparked: !state.data.sparked });
      setVersion((value) => value + 1);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "Could not update Spark.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <div>
      <button
        className="button"
        onClick={toggle}
        disabled={busy || !state.data}
        aria-pressed={state.data?.sparked || false}
      >
        <Sparkles size={16} /> {state.data?.sparked ? "Sparked" : "Spark"} ·{" "}
        {state.data?.count ?? 0}
      </button>
      {error && (
        <span className="error-text" role="alert">
          {error}
        </span>
      )}
    </div>
  );
}

function RepositoryTopics({
  endpoint,
  canManage,
  archived,
  settings,
}: {
  endpoint: string;
  canManage: boolean;
  archived: boolean;
  settings: boolean;
}) {
  const [version, setVersion] = useState(0);
  const topics = useData<TopicState>(`${endpoint}/topics`, version);
  const [draft, setDraft] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <section aria-label="Repository topics">
      {!!topics.data?.topics.length && (
        <div className="repo-meta">
          {topics.data.topics.map((topic) => (
            <Link
              key={topic}
              href={`/explore?topic=${encodeURIComponent(topic)}`}
            >
              <Badge>{topic}</Badge>
            </Link>
          ))}
        </div>
      )}
      {settings && canManage && !archived && (
        <div className="panel settings-form">
          <h2>Topics</h2>
          <p>
            Help builders discover this repository. Add up to ten
            comma-separated topics.
          </p>
          <label>
            Repository topics
            <input
              aria-label="Repository topics"
              value={draft ?? topics.data?.topics.join(", ") ?? ""}
              onChange={(event) => setDraft(event.target.value)}
              placeholder="go, open-source, developer-tools"
            />
          </label>
          <ErrorMessage error={error} />
          <button
            className="button"
            disabled={busy || !topics.data}
            onClick={async () => {
              setBusy(true);
              setError("");
              try {
                await put(`${endpoint}/topics`, {
                  topics: (draft ?? topics.data?.topics.join(",") ?? "")
                    .split(",")
                    .map((topic) => topic.trim())
                    .filter(Boolean),
                });
                setDraft(null);
                setVersion((value) => value + 1);
              } catch (cause) {
                setError(
                  cause instanceof Error
                    ? cause.message
                    : "Could not save topics.",
                );
              } finally {
                setBusy(false);
              }
            }}
          >
            Save topics
          </button>
        </div>
      )}
    </section>
  );
}

function CodeBrowser({
  endpoint,
  repo,
  branch,
  path,
  setPath,
}: {
  endpoint: string;
  repo: Repo;
  branch: string;
  path: string;
  setPath: (path: string) => void;
}) {
  const [version, setVersion] = useState(0);
  const [editing, setEditing] = useState(false);
  const [editPath, setEditPath] = useState("");
  const [draft, setDraft] = useState("");
  const [editError, setEditError] = useState("");
  const [saving, setSaving] = useState(false);
  const [showFileHistory, setShowFileHistory] = useState(false);
  const query = `?ref=${encodeURIComponent(branch)}&path=${encodeURIComponent(path)}`;
  const tree = useData<Tree>(
    branch ? `${endpoint}/tree${query}` : null,
    version,
  );
  const commits = useData<Commit[]>(
    branch ? `${endpoint}/commits?ref=${encodeURIComponent(branch)}` : null,
    version,
  );
  const readme = useData<Tree>(
    tree.data?.entries.find((e) => e.name.toLowerCase() === "readme.md")
      ? `${endpoint}/tree?ref=${encodeURIComponent(branch)}&path=${encodeURIComponent((path ? path + "/" : "") + tree.data.entries.find((e) => e.name.toLowerCase() === "readme.md")!.name)}`
      : null,
    version,
  );
  const fileHistory = useData<Commit[]>(
    showFileHistory && path
      ? `${endpoint}/commits?ref=${encodeURIComponent(branch)}&path=${encodeURIComponent(path)}`
      : null,
    version,
  );
  useEffect(() => {
    setShowFileHistory(false);
    setEditing(false);
  }, [branch, path]);
  const latest = commits.data?.[0];
  return (
    <div className="code-columns">
      <section>
        {path && (
          <div className="file-breadcrumb">
            <button onClick={() => setPath("")}>{repo.name}</button>
            {path.split("/").map((part, i, all) => (
              <span key={i}>
                <ChevronRight size={13} />
                <button onClick={() => setPath(all.slice(0, i + 1).join("/"))}>
                  {part}
                </button>
              </span>
            ))}
          </div>
        )}
        <div className="panel file-panel">
          <div className="latest-commit">
            {latest ? (
              <>
                <Avatar name={latest.author} small />
                <strong>{latest.author}</strong>
                <span className="commit-message">{latest.message}</span>
                <Link href={`${repoPath(repo)}/commits`}>
                  <code>{latest.sha.slice(0, 7)}</code>
                </Link>
                <span className="muted small-text">{date(latest.date)}</span>
                {repo.can_write && !repo.archived && (
                  <button
                    className="text-button"
                    onClick={() => {
                      setEditPath(
                        path && tree.data?.content === undefined
                          ? `${path}/`
                          : "",
                      );
                      setDraft("");
                      setEditError("");
                      setEditing(true);
                    }}
                  >
                    New file
                  </button>
                )}
              </>
            ) : (
              <span className="muted">Repository files</span>
            )}
          </div>
          <ErrorMessage error={tree.error} />
          {tree.loading ? (
            <Loading />
          ) : editing ? (
            <form
              className="web-editor"
              onSubmit={async (event) => {
                event.preventDefault();
                setSaving(true);
                setEditError("");
                const data = new FormData(event.currentTarget);
                try {
                  await api(`${endpoint}/contents`, {
                    method: "PUT",
                    body: JSON.stringify({
                      branch,
                      path: editPath,
                      content: draft,
                      message: data.get("message"),
                      expected_head: tree.data?.sha,
                    }),
                  });
                  setEditing(false);
                  setPath(editPath);
                  setVersion((value) => value + 1);
                } catch (error) {
                  setEditError((error as Error).message);
                } finally {
                  setSaving(false);
                }
              }}
            >
              <ErrorMessage error={editError} />
              <div className="editor-heading">
                <label>
                  File path
                  <input
                    value={editPath}
                    onChange={(event) => setEditPath(event.target.value)}
                    maxLength={4096}
                    required
                    autoFocus
                  />
                </label>
              </div>
              <label>
                File contents
                <textarea
                  className="code-editor"
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                  maxLength={524288}
                  rows={18}
                />
              </label>
              <label>
                Commit message
                <input name="message" maxLength={200} required />
              </label>
              <div className="form-actions">
                <button
                  type="button"
                  className="button"
                  onClick={() => setEditing(false)}
                >
                  Cancel
                </button>
                <button
                  className="button primary"
                  disabled={saving || !editPath.trim()}
                >
                  <Save size={15} /> {saving ? "Saving…" : "Save commit"}
                </button>
              </div>
            </form>
          ) : tree.data?.content !== undefined ? (
            <div className="blob-view">
              <div className="blob-header">
                <File size={14} />
                <span>{path.split("/").pop()}</span>
                <span className="muted">
                  {tree.data.content.split("\n").length} lines
                </span>
                <CopyButton
                  text={tree.data.content}
                  label="Copy file contents"
                />
                <a
                  className="button small-button"
                  href={`/api/v1${endpoint}/raw?ref=${encodeURIComponent(branch)}&path=${encodeURIComponent(path)}`}
                  target="_blank"
                  rel="noreferrer"
                >
                  Raw
                </a>
                <button
                  className="button small-button"
                  onClick={() => setShowFileHistory((value) => !value)}
                >
                  <History size={14} /> History
                </button>
                {repo.can_write && !repo.archived && (
                  <>
                    <button
                      className="button small-button"
                      onClick={() => {
                        setEditPath(path);
                        setDraft(tree.data!.content || "");
                        setEditError("");
                        setEditing(true);
                      }}
                    >
                      Edit
                    </button>
                    <button
                      className="button danger small-button"
                      disabled={saving}
                      onClick={async () => {
                        if (
                          !window.confirm(
                            `Delete ${path} from ${branch}? This creates a new commit and preserves its history.`,
                          )
                        )
                          return;
                        setSaving(true);
                        setEditError("");
                        try {
                          await api(`${endpoint}/contents`, {
                            method: "DELETE",
                            body: JSON.stringify({
                              branch,
                              path,
                              message: `Delete ${path}`,
                              expected_head: tree.data?.sha,
                            }),
                          });
                          setPath(path.split("/").slice(0, -1).join("/"));
                          setVersion((value) => value + 1);
                        } catch (deleteError) {
                          setEditError((deleteError as Error).message);
                        } finally {
                          setSaving(false);
                        }
                      }}
                    >
                      <Trash2 size={14} /> Delete
                    </button>
                  </>
                )}
              </div>
              {showFileHistory ? (
                <div className="file-history">
                  <ErrorMessage error={fileHistory.error} />
                  {fileHistory.loading ? (
                    <Loading />
                  ) : fileHistory.data?.length ? (
                    fileHistory.data.map((commit) => (
                      <div className="commit-row" key={commit.sha}>
                        <span className="commit-symbol">
                          <GitCommitHorizontal size={18} />
                        </span>
                        <div>
                          <strong>{commit.message}</strong>
                          <p>
                            {commit.author} changed this file{" "}
                            {date(commit.date)}
                          </p>
                        </div>
                        <code>{commit.sha.slice(0, 7)}</code>
                      </div>
                    ))
                  ) : (
                    <p className="muted padded">No file history found.</p>
                  )}
                </div>
              ) : (
                <pre>
                  {tree.data.content.split("\n").map((line, i) => (
                    <span className="code-line" key={i}>
                      <span className="line-number" aria-hidden="true">
                        {i + 1}
                      </span>
                      <code>{line || " "}</code>
                    </span>
                  ))}
                </pre>
              )}
            </div>
          ) : tree.data?.binary ? (
            <div className="empty-state">
              <File size={26} />
              <h3>Binary file</h3>
              <p>
                Download the raw file or clone the repository to open it
                locally.
              </p>
              <a
                className="button"
                href={`/api/v1${endpoint}/raw?ref=${encodeURIComponent(branch)}&path=${encodeURIComponent(path)}`}
              >
                Download raw file
              </a>
            </div>
          ) : (
            <div className="file-list">
              {path && (
                <button
                  className="file-row"
                  onClick={() =>
                    setPath(path.split("/").slice(0, -1).join("/"))
                  }
                >
                  <Folder size={17} />
                  <strong>..</strong>
                </button>
              )}
              {tree.data?.entries
                .slice()
                .sort((a, b) =>
                  a.type === b.type
                    ? a.name.localeCompare(b.name)
                    : a.type === "tree"
                      ? -1
                      : 1,
                )
                .map((entry) => (
                  <button
                    className="file-row"
                    key={entry.name}
                    disabled={entry.type === "commit"}
                    onClick={() =>
                      setPath((path ? path + "/" : "") + entry.name)
                    }
                  >
                    {entry.type === "tree" ? (
                      <Folder size={17} className="folder-icon" />
                    ) : (
                      <File size={17} />
                    )}
                    <span>{entry.name}</span>
                    <span className="file-type">
                      {entry.type === "tree"
                        ? "Directory"
                        : entry.type === "commit"
                          ? "Submodule"
                          : "File"}
                    </span>
                    <ChevronRight size={13} />
                  </button>
                ))}
            </div>
          )}
        </div>
        {readme.data?.content && (
          <article className="panel readme-panel">
            <div className="panel-header">
              <BookOpen size={16} />
              README.md<span>Plain text</span>
            </div>
            <pre>{readme.data.content}</pre>
          </article>
        )}
      </section>
      <aside className="about-repo">
        <h2>About this repository</h2>
        <p>
          {repo.description ||
            "Your project’s story starts with its first commit."}
        </p>
        <span>
          <Globe2 size={15} />
          {repo.visibility === "private"
            ? "Private repository"
            : "Public repository"}
        </span>
        <span>
          <GitBranch size={15} />
          Default branch: {repo.default_branch}
        </span>
        <span>
          <History size={15} />
          Created {date(repo.created_at)}
        </span>
        <hr />
        <h3>Ready to contribute?</h3>
        <p>Clone your repository, make a change, and push a new branch.</p>
        <Link className="text-link" href={`${repoPath(repo)}/pulls`}>
          Open a unite request <ArrowRight size={14} />
        </Link>
        <div className="mini-tip">
          <Terminal size={16} />
          <code>git checkout -b my-idea</code>
        </div>
      </aside>
    </div>
  );
}

function EmptyRepository({ repo }: { repo: Repo }) {
  const command = `git clone ${repo.clone_url}\ncd ${repo.name}\ngit switch -c main\necho "# ${repo.name}" > README.md\ngit add README.md\ngit commit -m "Initial commit"\ngit push -u origin main`;
  return (
    <div className="panel empty-repository">
      <span className="empty-icon">
        <Terminal size={28} />
      </span>
      <h2>Ready for your first commit.</h2>
      <p>Bring this repository to life from your terminal.</p>
      <div className="command-block">
        <CopyButton text={command} />
        <pre>{command}</pre>
      </div>
      <p>
        Use your username and a{" "}
        <Link href="/settings/tokens">personal access token</Link> when
        prompted.
      </p>
    </div>
  );
}

function IssueTemplateSettings({
  endpoint,
  archived,
}: {
  endpoint: string;
  archived: boolean;
}) {
  const [version, setVersion] = useState(0);
  const templates = useData<IssueTemplate[]>(
    `${endpoint}/issue-templates`,
    version,
  );
  const [draft, setDraft] = useState<IssueTemplate[] | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const current = draft ?? templates.data ?? [];
  function update(index: number, field: keyof IssueTemplate, value: string) {
    setDraft(
      current.map((template, position) =>
        position === index ? { ...template, [field]: value } : template,
      ),
    );
  }
  return (
    <section className="panel form-panel" aria-label="Issue templates">
      <h2>Issue templates</h2>
      <p>
        Create up to ten starting points for recurring work. Anyone with
        issue-creation permission can use them.
      </p>
      <ErrorMessage error={error || templates.error} />
      {notice && (
        <p role="status" className="green-text">
          {notice}
        </p>
      )}
      {current.map((template, index) => (
        <div className="panel form-panel" key={index}>
          <label>
            Template name
            <input
              aria-label={`Template ${index + 1} name`}
              value={template.name}
              maxLength={80}
              onChange={(event) => update(index, "name", event.target.value)}
            />
          </label>
          <label>
            Suggested title
            <input
              aria-label={`Template ${index + 1} title`}
              value={template.title}
              maxLength={200}
              onChange={(event) => update(index, "title", event.target.value)}
            />
          </label>
          <label>
            Suggested description
            <textarea
              aria-label={`Template ${index + 1} body`}
              value={template.body}
              maxLength={10000}
              rows={4}
              onChange={(event) => update(index, "body", event.target.value)}
            />
          </label>
          <button
            className="button"
            type="button"
            disabled={busy || archived}
            onClick={() =>
              setDraft(current.filter((_, position) => position !== index))
            }
          >
            Remove template
          </button>
        </div>
      ))}
      <div className="form-actions">
        <button
          className="button"
          type="button"
          disabled={busy || archived || current.length >= 10}
          onClick={() =>
            setDraft([...current, { name: "", title: "", body: "" }])
          }
        >
          Add issue template
        </button>
        <button
          className="button primary"
          type="button"
          disabled={busy || archived || !templates.data}
          onClick={async () => {
            setBusy(true);
            setError("");
            setNotice("");
            try {
              await put(`${endpoint}/issue-templates`, { templates: current });
              setDraft(null);
              setVersion((value) => value + 1);
              setNotice("Issue templates saved.");
            } catch (cause) {
              setError(
                cause instanceof Error
                  ? cause.message
                  : "Could not save issue templates.",
              );
            } finally {
              setBusy(false);
            }
          }}
        >
          Save issue templates
        </button>
      </div>
    </section>
  );
}

function RepositorySettings({
  endpoint,
  repo,
  branches,
}: {
  endpoint: string;
  repo: Repo;
  branches: string[];
}) {
  const [visibility, setVisibility] = useState(repo.visibility);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const router = useRouter();
  return (
    <section className="settings-layout">
      <div className="section-heading">
        <div>
          <h2>Repository settings</h2>
          <p>Control how this repository appears to you and other users.</p>
        </div>
      </div>
      <form
        className="panel settings-form"
        onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          setMessage("");
          const data = new FormData(event.currentTarget);
          try {
            await patch<Repo>(endpoint, {
              description: data.get("description"),
              visibility,
            });
            setMessage("Repository settings saved.");
          } catch (saveError) {
            setError((saveError as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <label>
          Description
          <textarea
            name="description"
            defaultValue={repo.description}
            maxLength={500}
            rows={4}
          />
        </label>
        <div>
          <span className="label-text">Visibility</span>
          <div className="visibility-options compact">
            {(["private", "public"] as const).map((option) => (
              <label
                className={`radio-card ${visibility === option ? "chosen" : ""}`}
                key={option}
              >
                <input
                  type="radio"
                  name="visibility"
                  value={option}
                  checked={visibility === option}
                  onChange={() => setVisibility(option)}
                />
                {option === "private" ? (
                  <LockKeyhole size={18} />
                ) : (
                  <Globe2 size={18} />
                )}
                <span>
                  <strong>{option}</strong>
                  {option === "private"
                    ? "Only you can see and clone this repository."
                    : "Anyone can view and clone this repository."}
                </span>
              </label>
            ))}
          </div>
        </div>
        {error && <div className="form-error">{error}</div>}
        {message && <div className="success-box">{message}</div>}
        <button className="button primary" disabled={busy} type="submit">
          <Save size={16} />
          {busy ? "Saving..." : "Save settings"}
        </button>
      </form>
      {branches.length > 0 && (
        <BranchRuleSettings
          endpoint={endpoint}
          branches={branches}
          defaultBranch={repo.default_branch}
        />
      )}
      <IssueTemplateSettings endpoint={endpoint} archived={repo.archived} />
      <CollaboratorSettings endpoint={endpoint} owner={repo.owner} />
      <div className="panel lifecycle-settings">
        <div className="section-heading">
          <div>
            <h2>Repository lifecycle</h2>
            <p>
              Rename, archive, restore, or schedule this repository for
              deletion.
            </p>
          </div>
        </div>
        <form
          className="rename-form"
          onSubmit={async (event) => {
            event.preventDefault();
            setBusy(true);
            setError("");
            const data = new FormData(event.currentTarget);
            try {
              const updated = await post<Repo>(`${endpoint}/rename`, {
                name: data.get("name"),
              });
              router.push(`${repoPath(updated)}/settings`);
            } catch (renameError) {
              setError((renameError as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <label>
            Repository name
            <input name="name" defaultValue={repo.name} required />
          </label>
          <button className="button" disabled={busy} type="submit">
            Rename repository
          </button>
        </form>
        <div className="lifecycle-row">
          <div>
            <strong>
              {repo.archived ? "Unarchive repository" : "Archive repository"}
            </strong>
            <span>
              {repo.archived
                ? "Allow pushes, issues, and merges again."
                : "Keep the code readable while blocking pushes and collaboration changes."}
            </span>
          </div>
          <button
            className="button"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setError("");
              try {
                await post<Repo>(
                  `${endpoint}/${repo.archived ? "unarchive" : "archive"}`,
                  {},
                );
                window.location.reload();
              } catch (archiveError) {
                setError((archiveError as Error).message);
                setBusy(false);
              }
            }}
            type="button"
          >
            <Archive size={16} /> {repo.archived ? "Unarchive" : "Archive"}
          </button>
        </div>
        <div className="lifecycle-row danger-zone">
          <div>
            <strong>Delete repository</strong>
            <span>Hide it immediately. You can restore it for 30 days.</span>
          </div>
          <button
            className="button danger"
            disabled={busy}
            onClick={async () => {
              const confirmation = window.prompt(
                `Type ${repo.name} to schedule deletion.`,
              );
              if (confirmation === null) return;
              setBusy(true);
              setError("");
              try {
                await destroy<{ deleted: boolean }>(endpoint, { confirmation });
                router.push("/");
              } catch (deleteError) {
                setError((deleteError as Error).message);
                setBusy(false);
              }
            }}
            type="button"
          >
            <Trash2 size={16} /> Delete repository
          </button>
        </div>
      </div>
    </section>
  );
}

function BranchRuleSettings({
  endpoint,
  branches,
  defaultBranch,
}: {
  endpoint: string;
  branches: string[];
  defaultBranch: string;
}) {
  const [branch, setBranch] = useState(
    branches.includes(defaultBranch) ? defaultBranch : branches[0],
  );
  const [version, setVersion] = useState(0);
  const rule = useData<BranchRule>(
    `${endpoint}/branch-rules?branch=${encodeURIComponent(branch)}`,
    version,
  );
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  return (
    <form
      className="panel settings-form"
      onSubmit={async (event) => {
        event.preventDefault();
        setBusy(true);
        setMessage("");
        setError("");
        const data = new FormData(event.currentTarget);
        try {
          await put<BranchRule>(
            `${endpoint}/branch-rules?branch=${encodeURIComponent(branch)}`,
            {
              required_approvals: Number(data.get("required_approvals")),
              block_changes_requested:
                data.get("block_changes_requested") === "on",
            },
          );
          setMessage("Branch rule saved.");
          setVersion((value) => value + 1);
        } catch (saveError) {
          setError((saveError as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <div className="section-heading">
        <div>
          <h2>
            <GitBranch size={18} /> Merge guard
          </h2>
          <p>Require fresh Unite approvals before changes enter a branch.</p>
        </div>
      </div>
      <label>
        Protected branch
        <select
          value={branch}
          onChange={(event) => {
            setBranch(event.target.value);
            setMessage("");
            setError("");
          }}
        >
          {branches.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      </label>
      {rule.loading ? (
        <Loading />
      ) : rule.error ? (
        <ErrorMessage error={rule.error} />
      ) : (
        <>
          <label>
            Required approvals
            <input
              key={`${branch}-${version}`}
              name="required_approvals"
              type="number"
              min="0"
              max="10"
              defaultValue={rule.data?.required_approvals ?? 0}
              required
            />
          </label>
          <label className="checkbox-row">
            <input
              key={`block-${branch}-${version}`}
              name="block_changes_requested"
              type="checkbox"
              defaultChecked={rule.data?.block_changes_requested ?? true}
            />
            Block merging while a current review requests changes
          </label>
          {error && <div className="form-error">{error}</div>}
          {message && <div className="success-box">{message}</div>}
          <button className="button primary" disabled={busy} type="submit">
            <Save size={16} /> {busy ? "Saving..." : "Save merge guard"}
          </button>
        </>
      )}
    </form>
  );
}

const collaboratorRoles = ["read", "triage", "write", "maintain"] as const;

function CollaboratorSettings({
  endpoint,
  owner,
}: {
  endpoint: string;
  owner: string;
}) {
  const [version, setVersion] = useState(0);
  const members = useData<RepositoryMember[]>(`${endpoint}/members`, version);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = () => setVersion((value) => value + 1);
  return (
    <div className="panel collaborator-settings">
      <div className="section-heading">
        <div>
          <h2>
            <Users size={18} /> Collaborators
          </h2>
          <p>
            Grant repository access to existing GITOWN users. You remain the
            owner.
          </p>
        </div>
      </div>
      <form
        className="collaborator-form"
        onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          setMessage("");
          const form = event.currentTarget;
          const data = new FormData(form);
          try {
            await post<RepositoryMember>(`${endpoint}/members`, {
              username: data.get("username"),
              role: data.get("role"),
            });
            form.reset();
            setMessage("Collaborator added.");
            refresh();
          } catch (memberError) {
            setError((memberError as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <label>
          Username
          <input name="username" placeholder="gitown-user" required />
        </label>
        <label>
          Role
          <select name="role" defaultValue="read">
            {collaboratorRoles.map((role) => (
              <option key={role} value={role}>
                {role[0].toUpperCase() + role.slice(1)}
              </option>
            ))}
          </select>
        </label>
        <button className="button primary" disabled={busy} type="submit">
          <UserPlus size={16} /> {busy ? "Adding..." : "Add collaborator"}
        </button>
      </form>
      <p className="muted small-text role-help">
        Read can clone. Triage can manage issues. Write and Maintain can also
        push and merge. Only {owner} can manage access and visibility.
      </p>
      {error && <div className="form-error">{error}</div>}
      {message && <div className="success-box">{message}</div>}
      <ErrorMessage error={members.error} />
      {members.loading ? (
        <Loading />
      ) : members.data?.length ? (
        <div className="member-list">
          {members.data.map((member) => (
            <div className="member-row" key={member.username}>
              <Avatar name={member.display_name || member.username} small />
              <div>
                <strong>{member.display_name || member.username}</strong>
                <span>@{member.username}</span>
              </div>
              <select
                aria-label={`Role for ${member.username}`}
                value={member.role}
                onChange={async (event) => {
                  setError("");
                  setMessage("");
                  try {
                    await patch<RepositoryMember>(
                      `${endpoint}/members/${encodeURIComponent(member.username)}`,
                      { role: event.target.value },
                    );
                    setMessage(`Updated @${member.username}.`);
                    refresh();
                  } catch (memberError) {
                    setError((memberError as Error).message);
                    refresh();
                  }
                }}
              >
                {collaboratorRoles.map((role) => (
                  <option key={role} value={role}>
                    {role[0].toUpperCase() + role.slice(1)}
                  </option>
                ))}
              </select>
              <button
                aria-label={`Remove ${member.username}`}
                className="icon-button danger-icon"
                onClick={async () => {
                  setError("");
                  setMessage("");
                  try {
                    await remove<{ removed: boolean }>(
                      `${endpoint}/members/${encodeURIComponent(member.username)}`,
                    );
                    setMessage(`Removed @${member.username}.`);
                    refresh();
                  } catch (memberError) {
                    setError((memberError as Error).message);
                  }
                }}
                type="button"
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
        </div>
      ) : (
        <div className="empty-inline">No collaborators yet.</div>
      )}
    </div>
  );
}

function CommitList({
  endpoint,
  branch,
}: {
  endpoint: string;
  branch: string;
}) {
  const commits = useData<Commit[]>(
    `${endpoint}/commits?ref=${encodeURIComponent(branch)}`,
  );
  return (
    <>
      <div className="section-heading">
        <h2>Commit history</h2>
        <span className="muted small-text">Latest 30 commits</span>
      </div>
      <ErrorMessage error={commits.error} />
      {commits.loading ? (
        <Loading />
      ) : commits.data?.length ? (
        <div className="panel">
          {commits.data.map((commit) => (
            <div className="commit-row" key={commit.sha}>
              <span className="commit-symbol">
                <GitCommitHorizontal size={20} />
              </span>
              <div>
                <strong>{commit.message}</strong>
                <p>
                  <Avatar name={commit.author} small />
                  {commit.author}
                  <span>committed {date(commit.date)}</span>
                </p>
              </div>
              <code>{commit.sha.slice(0, 7)}</code>
              <CopyButton text={commit.sha} label="Copy commit SHA" />
            </div>
          ))}
        </div>
      ) : (
        <div className="empty-state">
          <GitCommitHorizontal size={30} />
          <h3>No commits yet</h3>
          <p>Push your first commit to start the story.</p>
        </div>
      )}
    </>
  );
}

function IssueList({
  endpoint,
  canTriage,
  canComment,
}: {
  endpoint: string;
  canTriage: boolean;
  canComment: boolean;
}) {
  const [version, setVersion] = useState(0);
  const issues = useData<Issue[]>(`${endpoint}/issues`, version);
  const [showForm, setShowForm] = useState(false);
  const [showLabelForm, setShowLabelForm] = useState(false);
  const [showMilestoneForm, setShowMilestoneForm] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [filter, setFilter] = useState("open");
  const [expanded, setExpanded] = useState<number | null>(null);
  const labels = useData<Label[]>(`${endpoint}/labels`, version);
  const milestones = useData<Milestone[]>(`${endpoint}/milestones`, version);
  const templates = useData<IssueTemplate[]>(
    `${endpoint}/issue-templates`,
    version,
  );
  const [issueTitle, setIssueTitle] = useState("");
  const [issueBody, setIssueBody] = useState("");
  const filtered = issues.data?.filter((i) => i.state === filter) || [];
  return (
    <>
      <div className="section-heading">
        <div className="state-filters">
          <button
            className={filter === "open" ? "active" : ""}
            onClick={() => setFilter("open")}
          >
            <CircleDot size={16} />
            Open{" "}
            <span>
              {issues.data?.filter((i) => i.state === "open").length || 0}
            </span>
          </button>
          <button
            className={filter === "closed" ? "active" : ""}
            onClick={() => setFilter("closed")}
          >
            <Check size={16} />
            Closed
          </button>
        </div>
        {canTriage && (
          <div className="heading-actions">
            <button
              className="button small-button"
              onClick={() => setShowMilestoneForm(!showMilestoneForm)}
            >
              <Plus size={15} />
              New milestone
            </button>
            <button
              className="button small-button"
              onClick={() => setShowLabelForm(!showLabelForm)}
            >
              <Plus size={15} />
              New label
            </button>
            <button
              className="button primary small-button"
              onClick={() => setShowForm(!showForm)}
            >
              <Plus size={15} />
              New issue
            </button>
          </div>
        )}
      </div>
      <ErrorMessage
        error={error || issues.error || labels.error || milestones.error}
      />
      {showMilestoneForm && (
        <form
          className="panel form-panel inline-form"
          onSubmit={async (event) => {
            event.preventDefault();
            setBusy(true);
            setError("");
            const data = new FormData(event.currentTarget);
            try {
              await post(`${endpoint}/milestones`, {
                title: data.get("title"),
                description: data.get("description"),
                due_date: data.get("due_date"),
              });
              setShowMilestoneForm(false);
              setVersion((value) => value + 1);
            } catch (saveError) {
              setError((saveError as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <h2>Set a milestone.</h2>
          <label>
            Milestone title
            <input name="title" maxLength={200} required />
          </label>
          <label>
            Description
            <textarea name="description" maxLength={10000} rows={3} />
          </label>
          <label>
            Due date optional
            <input name="due_date" type="date" />
          </label>
          <div className="form-actions">
            <button
              type="button"
              className="button"
              onClick={() => setShowMilestoneForm(false)}
            >
              Cancel
            </button>
            <button disabled={busy} className="button primary">
              {busy ? "Creating…" : "Create milestone"}
            </button>
          </div>
        </form>
      )}
      {!!milestones.data?.length && (
        <section className="panel label-catalog" aria-label="Milestones">
          {milestones.data.map((milestone) => (
            <span className="label-catalog-item" key={milestone.id}>
              <Badge kind={milestone.state === "open" ? "green" : ""}>
                {milestone.title}
              </Badge>
              <span>
                {milestone.open_issues} open · {milestone.closed_issues} closed
                {milestone.due_date ? ` · Due ${milestone.due_date}` : ""}
              </span>
              {canTriage && (
                <button
                  disabled={busy}
                  aria-label={`${milestone.state === "open" ? "Close" : "Reopen"} milestone ${milestone.title}`}
                  onClick={async () => {
                    setBusy(true);
                    setError("");
                    try {
                      await put(`${endpoint}/milestones/${milestone.id}`, {
                        title: milestone.title,
                        description: milestone.description,
                        due_date: milestone.due_date || "",
                        state: milestone.state === "open" ? "closed" : "open",
                      });
                      setVersion((value) => value + 1);
                    } catch (saveError) {
                      setError((saveError as Error).message);
                    } finally {
                      setBusy(false);
                    }
                  }}
                >
                  {milestone.state === "open" ? "Close" : "Reopen"}
                </button>
              )}
            </span>
          ))}
        </section>
      )}
      {showLabelForm && (
        <form
          className="panel form-panel inline-form label-form"
          onSubmit={async (event) => {
            event.preventDefault();
            setBusy(true);
            setError("");
            const data = new FormData(event.currentTarget);
            try {
              await post(`${endpoint}/labels`, {
                name: data.get("name"),
                color: data.get("color"),
                description: data.get("description"),
              });
              setShowLabelForm(false);
              setVersion((value) => value + 1);
            } catch (error) {
              setError((error as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <h2>Create a reusable label.</h2>
          <div className="form-grid">
            <label>
              Name
              <input name="name" maxLength={50} required />
            </label>
            <label>
              Color
              <input
                name="color"
                defaultValue="2f9e44"
                pattern="#?[0-9a-fA-F]{6}"
                maxLength={7}
                required
              />
            </label>
          </div>
          <label>
            Description
            <input name="description" maxLength={200} />
          </label>
          <div className="form-actions">
            <button
              type="button"
              className="button"
              onClick={() => setShowLabelForm(false)}
            >
              Cancel
            </button>
            <button disabled={busy} className="button primary">
              {busy ? "Creating…" : "Create label"}
            </button>
          </div>
        </form>
      )}
      {!!labels.data?.length && (
        <div className="label-catalog panel">
          {labels.data.map((label) => (
            <span className="label-catalog-item" key={label.id}>
              <LabelChip label={label} />
              {label.description && <span>{label.description}</span>}
              {canTriage && (
                <button
                  aria-label={`Delete label ${label.name}`}
                  onClick={async () => {
                    setBusy(true);
                    setError("");
                    try {
                      await remove(`${endpoint}/labels/${label.id}`);
                      setVersion((value) => value + 1);
                    } catch (error) {
                      setError((error as Error).message);
                    } finally {
                      setBusy(false);
                    }
                  }}
                >
                  ×
                </button>
              )}
            </span>
          ))}
        </div>
      )}
      {showForm && (
        <form
          className="panel form-panel inline-form"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setError("");
            const data = new FormData(e.currentTarget);
            try {
              await post(`${endpoint}/issues`, {
                title: data.get("title"),
                body: data.get("body"),
              });
              setShowForm(false);
              setIssueTitle("");
              setIssueBody("");
              setVersion((v) => v + 1);
              setFilter("open");
            } catch (error) {
              setError((error as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <h2>Give your idea a starting point.</h2>
          {!!templates.data?.length && (
            <label>
              Start from template
              <select
                aria-label="Issue template"
                defaultValue=""
                onChange={(event) => {
                  const selected = templates.data?.find(
                    (template) => template.name === event.target.value,
                  );
                  if (selected) {
                    setIssueTitle(selected.title);
                    setIssueBody(selected.body);
                  }
                }}
              >
                <option value="">Blank issue</option>
                {templates.data.map((template) => (
                  <option key={template.name} value={template.name}>
                    {template.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          <label>
            Title
            <input
              name="title"
              value={issueTitle}
              onChange={(event) => setIssueTitle(event.target.value)}
              placeholder="What needs to happen?"
              maxLength={200}
              required
              autoFocus
            />
          </label>
          <label>
            Description
            <textarea
              name="body"
              value={issueBody}
              onChange={(event) => setIssueBody(event.target.value)}
              placeholder="Add context, a plan, or steps to reproduce…"
              maxLength={20000}
              rows={5}
            />
          </label>
          <div className="form-actions">
            <button
              type="button"
              className="button"
              onClick={() => setShowForm(false)}
            >
              Cancel
            </button>
            <button disabled={busy} className="button primary">
              {busy ? "Creating…" : "Create issue"}
            </button>
          </div>
        </form>
      )}
      {issues.loading ? (
        <Loading />
      ) : filtered.length ? (
        <div className="panel issue-list">
          {filtered.map((issue) => (
            <article className="issue-item" key={issue.id}>
              <div className="issue-title-row">
                <CircleDot
                  className={issue.state === "open" ? "green-text" : "muted"}
                  size={20}
                />
                <div>
                  <button
                    className="issue-title"
                    onClick={() =>
                      setExpanded(
                        expanded === issue.number ? null : issue.number,
                      )
                    }
                  >
                    {issue.title}
                  </button>
                  <p>
                    #{issue.number} opened {date(issue.created_at)} by{" "}
                    {issue.author}
                  </p>
                </div>
                <Badge kind={issue.state === "open" ? "green" : ""}>
                  {issue.state}
                </Badge>
              </div>
              {expanded === issue.number && (
                <div className="issue-body">
                  <p>{issue.body || "No description provided."}</p>
                  <IssueComments
                    endpoint={endpoint}
                    issueNumber={issue.number}
                    canComment={canComment}
                    onComment={() => setVersion((value) => value + 1)}
                  />
                  <IssueLabels
                    endpoint={endpoint}
                    issueNumber={issue.number}
                    labels={labels.data || []}
                    canTriage={canTriage}
                  />
                  <IssueAssigneePicker
                    endpoint={endpoint}
                    issueNumber={issue.number}
                    canTriage={canTriage}
                  />
                  <IssueDependenciesPicker
                    endpoint={endpoint}
                    issueNumber={issue.number}
                    issues={issues.data || []}
                    canTriage={canTriage}
                  />
                  <IssueSubscription
                    endpoint={endpoint}
                    issueNumber={issue.number}
                    canSubscribe={canComment}
                    refreshVersion={version}
                  />
                  <IssueMilestonePicker
                    endpoint={endpoint}
                    issueNumber={issue.number}
                    milestones={milestones.data || []}
                    canTriage={canTriage}
                    onChange={() => setVersion((value) => value + 1)}
                  />
                  {canTriage && (
                    <button
                      disabled={busy}
                      className="button small-button"
                      onClick={async () => {
                        setBusy(true);
                        setError("");
                        try {
                          await api(`${endpoint}/issues/${issue.number}`, {
                            method: "PATCH",
                            body: JSON.stringify({
                              state: issue.state === "open" ? "closed" : "open",
                            }),
                          });
                          setVersion((v) => v + 1);
                        } catch (error) {
                          setError((error as Error).message);
                        } finally {
                          setBusy(false);
                        }
                      }}
                    >
                      {issue.state === "open" ? "Close issue" : "Reopen issue"}
                    </button>
                  )}
                </div>
              )}
            </article>
          ))}
        </div>
      ) : (
        <div className="empty-state panel">
          <CircleDot size={30} />
          <h3>No {filter} issues</h3>
          <p>A place for bugs, ideas, and the things you want to build next.</p>
        </div>
      )}
    </>
  );
}

function IssueMilestonePicker({
  endpoint,
  issueNumber,
  milestones,
  canTriage,
  onChange,
}: {
  endpoint: string;
  issueNumber: number;
  milestones: Milestone[];
  canTriage: boolean;
  onChange: () => void;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const path = `${endpoint}/issues/${issueNumber}/milestone`;
  const selected = useData<{ milestone: Milestone | null }>(path, version);
  return (
    <section className="issue-labels" aria-label="Issue milestone">
      <h4>Milestone</h4>
      <ErrorMessage error={error || selected.error} />
      {canTriage ? (
        <label className="label-picker">
          Assign milestone
          <select
            value={selected.data?.milestone?.id || ""}
            onChange={async (event) => {
              setError("");
              try {
                await put(path, { milestone_id: event.target.value || null });
                setVersion((value) => value + 1);
                onChange();
              } catch (saveError) {
                setError((saveError as Error).message);
              }
            }}
          >
            <option value="">No milestone</option>
            {milestones
              .filter(
                (milestone) =>
                  milestone.state === "open" ||
                  milestone.id === selected.data?.milestone?.id,
              )
              .map((milestone) => (
                <option value={milestone.id} key={milestone.id}>
                  {milestone.title}
                </option>
              ))}
          </select>
        </label>
      ) : (
        <span className="muted small-text">
          {selected.data?.milestone?.title || "No milestone"}
        </span>
      )}
    </section>
  );
}

function IssueDependenciesPicker({
  endpoint,
  issueNumber,
  issues,
  canTriage,
}: {
  endpoint: string;
  issueNumber: number;
  issues: Issue[];
  canTriage: boolean;
}) {
  const [version, setVersion] = useState(0);
  const dependencies = useData<IssueDependencies>(
    `${endpoint}/issues/${issueNumber}/dependencies`,
    version,
  );
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const selected =
    dependencies.data?.blocked_by.map((item) => item.number) || [];
  async function save(numbers: number[]) {
    setBusy(true);
    setError("");
    try {
      await put(`${endpoint}/issues/${issueNumber}/dependencies`, {
        blocked_by: numbers,
      });
      setVersion((value) => value + 1);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "Could not update blockers.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <div
      className="issue-meta-section"
      aria-label={`Dependencies for issue #${issueNumber}`}
    >
      <strong>Dependencies</strong>
      <ErrorMessage error={error || dependencies.error} />
      {dependencies.data?.blocked_by.length ? (
        <p>
          Blocked by{" "}
          {dependencies.data.blocked_by.map((item) => (
            <span key={item.number} className="label-catalog-item">
              #{item.number} {item.title} ({item.state}){" "}
              {canTriage && (
                <button
                  type="button"
                  disabled={busy}
                  aria-label={`Remove blocker #${item.number}`}
                  onClick={() =>
                    save(selected.filter((number) => number !== item.number))
                  }
                >
                  ×
                </button>
              )}
            </span>
          ))}
        </p>
      ) : (
        <p className="muted">No blockers.</p>
      )}
      {!!dependencies.data?.blocks.length && (
        <p>
          Blocks{" "}
          {dependencies.data.blocks
            .map((item) => `#${item.number} ${item.title}`)
            .join(", ")}
        </p>
      )}
      {canTriage && (
        <select
          aria-label={`Add blocker to issue #${issueNumber}`}
          disabled={busy || !dependencies.data || selected.length >= 20}
          value=""
          onChange={(event) => {
            if (event.target.value)
              save([...selected, Number(event.target.value)]);
          }}
        >
          <option value="">Add blocker…</option>
          {issues
            .filter(
              (issue) =>
                issue.number !== issueNumber &&
                !selected.includes(issue.number),
            )
            .map((issue) => (
              <option key={issue.number} value={issue.number}>
                #{issue.number} {issue.title}
              </option>
            ))}
        </select>
      )}
    </div>
  );
}

function IssueSubscription({
  endpoint,
  issueNumber,
  canSubscribe,
  refreshVersion,
}: {
  endpoint: string;
  issueNumber: number;
  canSubscribe: boolean;
  refreshVersion: number;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const path = `${endpoint}/issues/${issueNumber}/subscription`;
  const subscription = useData<{ subscribed: boolean }>(
    path,
    version + refreshVersion,
  );
  if (!canSubscribe) return null;
  return (
    <section className="issue-labels" aria-label="Issue updates">
      <h4>Updates</h4>
      <ErrorMessage error={error || subscription.error} />
      <p className="muted small-text">
        Get inbox updates when someone comments or changes this issue.
      </p>
      <button
        className="button small-button"
        disabled={subscription.loading}
        onClick={async () => {
          setError("");
          try {
            await put(path, { subscribed: !subscription.data?.subscribed });
            setVersion((value) => value + 1);
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        {subscription.data?.subscribed ? "Unfollow issue" : "Follow issue"}
      </button>
    </section>
  );
}

function IssueAssigneePicker({
  endpoint,
  issueNumber,
  canTriage,
}: {
  endpoint: string;
  issueNumber: number;
  canTriage: boolean;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const path = `${endpoint}/issues/${issueNumber}/assignees`;
  const people = useData<IssueAssignees>(path, version);
  const save = async (usernames: string[]) => {
    setError("");
    try {
      await put<IssueAssignees>(path, { usernames });
      setVersion((value) => value + 1);
    } catch (saveError) {
      setError((saveError as Error).message);
    }
  };
  return (
    <section className="issue-labels" aria-label="Issue assignees">
      <h4>Assignees</h4>
      <ErrorMessage error={error || people.error} />
      <div className="assigned-labels">
        {people.data?.assigned.map((user) => (
          <span className="assigned-label" key={user.username}>
            <Badge>@{user.username}</Badge>
            {canTriage && (
              <button
                aria-label={`Unassign ${user.username}`}
                onClick={() =>
                  save(
                    people
                      .data!.assigned.filter(
                        (item) => item.username !== user.username,
                      )
                      .map((item) => item.username),
                  )
                }
              >
                ×
              </button>
            )}
          </span>
        ))}
        {!people.loading && !people.data?.assigned.length && (
          <span className="muted small-text">Nobody assigned.</span>
        )}
      </div>
      {canTriage && !!people.data?.available.length && (
        <label className="label-picker">
          Assign person
          <select
            value=""
            onChange={(event) => {
              if (!event.target.value || !people.data) return;
              void save([
                ...people.data.assigned.map((item) => item.username),
                event.target.value,
              ]);
            }}
          >
            <option value="">Choose a collaborator…</option>
            {people.data.available.map((user) => (
              <option key={user.username} value={user.username}>
                {user.display_name} (@{user.username})
              </option>
            ))}
          </select>
        </label>
      )}
    </section>
  );
}

function LabelChip({ label }: { label: Label }) {
  const foreground = readableLabelText(label.color);
  return (
    <span
      className="label-chip"
      style={{ backgroundColor: `#${label.color}`, color: foreground }}
      title={label.description || label.name}
    >
      {label.name}
    </span>
  );
}

function readableLabelText(color: string) {
  const red = Number.parseInt(color.slice(0, 2), 16);
  const green = Number.parseInt(color.slice(2, 4), 16);
  const blue = Number.parseInt(color.slice(4, 6), 16);
  return red * 299 + green * 587 + blue * 114 > 150000 ? "#152018" : "#ffffff";
}

function IssueLabels({
  endpoint,
  issueNumber,
  labels,
  canTriage,
}: {
  endpoint: string;
  issueNumber: number;
  labels: Label[];
  canTriage: boolean;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const path = `${endpoint}/issues/${issueNumber}/labels`;
  const assigned = useData<Label[]>(path, version);
  const assignedIDs = new Set(assigned.data?.map((label) => label.id));
  const available = labels.filter((label) => !assignedIDs.has(label.id));
  return (
    <section className="issue-labels" aria-label="Issue labels">
      <ErrorMessage error={error || assigned.error} />
      <div className="assigned-labels">
        {assigned.data?.map((label) => (
          <span key={label.id} className="assigned-label">
            <LabelChip label={label} />
            {canTriage && (
              <button
                aria-label={`Remove label ${label.name}`}
                onClick={async () => {
                  setError("");
                  try {
                    await remove(`${path}/${label.id}`);
                    setVersion((value) => value + 1);
                  } catch (error) {
                    setError((error as Error).message);
                  }
                }}
              >
                ×
              </button>
            )}
          </span>
        ))}
      </div>
      {canTriage && available.length > 0 && (
        <label className="label-picker">
          Add label
          <select
            value=""
            onChange={async (event) => {
              if (!event.target.value) return;
              setError("");
              try {
                await post(path, { label_id: event.target.value });
                setVersion((value) => value + 1);
              } catch (error) {
                setError((error as Error).message);
              }
            }}
          >
            <option value="">Choose a label…</option>
            {available.map((label) => (
              <option value={label.id} key={label.id}>
                {label.name}
              </option>
            ))}
          </select>
        </label>
      )}
    </section>
  );
}

function IssueComments({
  endpoint,
  issueNumber,
  canComment,
  onComment,
}: {
  endpoint: string;
  issueNumber: number;
  canComment: boolean;
  onComment: () => void;
}) {
  const [version, setVersion] = useState(0);
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const path = `${endpoint}/issues/${issueNumber}/comments`;
  const comments = useData<IssueComment[]>(path, version);
  return (
    <section className="issue-comments" aria-label="Issue discussion">
      <h4>Discussion</h4>
      <ErrorMessage error={error || comments.error} />
      {comments.loading ? (
        <Loading />
      ) : comments.data?.length ? (
        <div className="comment-list">
          {comments.data.map((comment) => (
            <article className="issue-comment" key={comment.id}>
              <div>
                <strong>@{comment.author}</strong>
                <span>{date(comment.created_at)}</span>
              </div>
              <p>{comment.body}</p>
            </article>
          ))}
        </div>
      ) : (
        <p className="muted small-text">No comments yet.</p>
      )}
      {canComment && (
        <form
          className="comment-form"
          onSubmit={async (event) => {
            event.preventDefault();
            setBusy(true);
            setError("");
            try {
              await post(path, { body });
              setBody("");
              setVersion((value) => value + 1);
              onComment();
            } catch (error) {
              setError((error as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <label>
            Add a comment
            <textarea
              value={body}
              onChange={(event) => setBody(event.target.value)}
              maxLength={10000}
              rows={3}
              required
            />
          </label>
          <button
            className="button primary small-button"
            disabled={busy || !body.trim()}
          >
            {busy ? "Commenting…" : "Comment"}
          </button>
        </form>
      )}
    </section>
  );
}

function PullRequestList({
  endpoint,
  repo,
  branches,
}: {
  endpoint: string;
  repo: Repo;
  branches: string[];
}) {
  const pulls = useData<Pull[]>(`${endpoint}/pulls`);
  const [showForm, setShowForm] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const router = useRouter();
  return (
    <>
      <div className="section-heading">
        <h2>
          Unite requests{" "}
          <span className="count">{pulls.data?.length || 0}</span>
        </h2>
        {repo.can_write && (
          <button
            className="button primary small-button"
            onClick={() => setShowForm(!showForm)}
          >
            <Plus size={15} />
            New unite request
          </button>
        )}
      </div>
      <ErrorMessage error={error || pulls.error} />
      {showForm &&
        (branches.length < 2 ? (
          <div className="info-box">
            <GitBranch size={20} />
            <div>
              <strong>Push a second branch first.</strong>
              <p>
                Create a branch locally, commit your changes, and run{" "}
                <code>git push -u origin your-branch</code>. Refresh this page
                to compare it with {repo.default_branch}.
              </p>
            </div>
          </div>
        ) : (
          <form
            className="panel form-panel inline-form"
            onSubmit={async (e) => {
              e.preventDefault();
              setBusy(true);
              setError("");
              const data = new FormData(e.currentTarget);
              try {
                const pull = await post<Pull>(`${endpoint}/pulls`, {
                  title: data.get("title"),
                  body: data.get("body"),
                  base_branch: data.get("base_branch"),
                  head_branch: data.get("head_branch"),
                });
                router.push(`${repoPath(repo)}/pulls/${pull.number}`);
              } catch (error) {
                setError((error as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            <h2>Bring your changes together.</h2>
            <div className="two-fields">
              <label>
                Base branch
                <select name="base_branch" defaultValue={repo.default_branch}>
                  {branches.map((b) => (
                    <option key={b}>{b}</option>
                  ))}
                </select>
              </label>
              <label>
                Compare branch
                <select
                  name="head_branch"
                  defaultValue={branches.find((b) => b !== repo.default_branch)}
                >
                  {branches.map((b) => (
                    <option key={b}>{b}</option>
                  ))}
                </select>
              </label>
            </div>
            <label>
              Title
              <input
                name="title"
                placeholder="What does this change do?"
                maxLength={200}
                required
              />
            </label>
            <label>
              Description
              <textarea
                name="body"
                placeholder="Give your changes some context…"
                rows={4}
                maxLength={20000}
              />
            </label>
            <div className="form-actions">
              <button
                type="button"
                className="button"
                onClick={() => setShowForm(false)}
              >
                Cancel
              </button>
              <button className="button primary" disabled={busy}>
                <GitPullRequest size={16} />
                {busy ? "Creating…" : "Create unite request"}
              </button>
            </div>
          </form>
        ))}
      {pulls.loading ? (
        <Loading />
      ) : pulls.data?.length ? (
        <div className="panel issue-list">
          {pulls.data.map((pull) => (
            <Link
              className="issue-title-row pull-row"
              href={`${repoPath(repo)}/pulls/${pull.number}`}
              key={pull.id}
            >
              {pull.state === "merged" ? (
                <GitMerge className="purple-text" size={20} />
              ) : (
                <GitPullRequest className="green-text" size={20} />
              )}
              <div>
                <strong>{pull.title}</strong>
                <p>
                  #{pull.number} · {pull.head_branch} → {pull.base_branch} ·{" "}
                  {date(pull.created_at)}
                </p>
              </div>
              <Badge kind={pull.state === "merged" ? "purple" : "green"}>
                {pull.state}
              </Badge>
            </Link>
          ))}
        </div>
      ) : (
        <div className="empty-state panel">
          <GitPullRequest size={30} />
          <h3>Good changes start a conversation.</h3>
          <p>
            Push a branch and open a unite request to compare and merge your
            work.
          </p>
        </div>
      )}
    </>
  );
}

function PullRequestDetail({
  endpoint,
  number,
  repo,
}: {
  endpoint: string;
  number: string;
  repo: Repo;
}) {
  const [version, setVersion] = useState(0);
  const detail = useData<PullDetail>(`${endpoint}/pulls/${number}`, version);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (detail.loading) return <Loading />;
  if (!detail.data) return <ErrorMessage error={detail.error} />;
  const { pull, mergeable, diff, diff_error } = detail.data;
  return (
    <>
      <Link className="back-link" href={`${repoPath(repo)}/pulls`}>
        <ArrowLeft size={15} />
        All unite requests
      </Link>
      <div className="pull-heading">
        <h1>
          {pull.title} <span className="muted">#{pull.number}</span>
        </h1>
        <div>
          <Badge kind={pull.state === "merged" ? "purple" : "green"}>
            <GitPullRequest size={13} />
            {pull.state}
          </Badge>
          <span>
            {pull.author} wants to merge <code>{pull.head_branch}</code> into{" "}
            <code>{pull.base_branch}</code>
          </span>
        </div>
      </div>
      {pull.body && <div className="panel pull-description">{pull.body}</div>}
      <ErrorMessage error={error} />
      <PullDiscussion
        endpoint={endpoint}
        number={number}
        canComment={repo.can_comment}
      />
      <PullReviews
        endpoint={endpoint}
        number={number}
        headSHA={detail.data.head_sha}
        canReview={detail.data.can_review}
      />
      {repo.can_triage &&
        pull.state !== "merged" &&
        pull.state !== "merging" && (
          <div className="pull-actions">
            <button
              className="button small-button"
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                setError("");
                try {
                  await patch(`${endpoint}/pulls/${number}`, {
                    state: pull.state === "open" ? "closed" : "open",
                  });
                  setVersion((value) => value + 1);
                } catch (error) {
                  setError((error as Error).message);
                } finally {
                  setBusy(false);
                }
              }}
            >
              {pull.state === "open"
                ? "Close unite request"
                : "Reopen unite request"}
            </button>
          </div>
        )}
      <div className={`merge-panel ${pull.state === "merged" ? "merged" : ""}`}>
        <span className="merge-icon">
          {pull.state === "merged" ? (
            <GitMerge size={22} />
          ) : (
            <GitBranch size={22} />
          )}
        </span>
        <div>
          <h3>
            {pull.state === "merged"
              ? "Changes successfully merged"
              : pull.state === "merging"
                ? "Merge recovery is pending"
                : mergeable
                  ? "These branches can be merged"
                  : "These branches need attention"}
          </h3>
          <p>
            {pull.state === "merged"
              ? `Merge commit ${pull.merge_sha?.slice(0, 7)} is part of ${pull.base_branch}.`
              : pull.state === "merging"
                ? "Retry to reconcile an interrupted merge with Git storage."
                : mergeable
                  ? "A merge commit will preserve the history of both branches."
                  : "Resolve conflicts locally, push your changes, then refresh."}
          </p>
        </div>
        {repo.can_write &&
          (pull.state === "open" || pull.state === "merging") && (
            <button
              disabled={busy || (!mergeable && pull.state !== "merging")}
              className="button primary"
              onClick={async () => {
                if (
                  !window.confirm(
                    `Merge unite request #${number} into ${pull.base_branch}?`,
                  )
                )
                  return;
                setBusy(true);
                setError("");
                try {
                  await post(`${endpoint}/pulls/${number}/merge`, {
                    head_sha: detail.data!.head_sha,
                    base_sha: detail.data!.base_sha,
                  });
                  setVersion((v) => v + 1);
                } catch (error) {
                  setError((error as Error).message);
                } finally {
                  setBusy(false);
                }
              }}
            >
              <GitMerge size={16} />
              {busy
                ? "Merging…"
                : pull.state === "merging"
                  ? "Recover merge"
                  : "Merge unite request"}
            </button>
          )}
      </div>
      <div className="section-heading">
        <h2>Changes</h2>
        <button
          className="text-button"
          onClick={() => setVersion((v) => v + 1)}
        >
          Refresh comparison
        </button>
      </div>
      {diff_error ? (
        <div className="info-box">{diff_error}</div>
      ) : (
        <div className="panel diff-panel">
          <pre>
            {diff
              ? diff.split("\n").map((line, i) => (
                  <span
                    className={`diff-line ${line.startsWith("+") && !line.startsWith("+++") ? "addition" : line.startsWith("-") && !line.startsWith("---") ? "deletion" : line.startsWith("@@") ? "diff-hunk" : ""}`}
                    key={i}
                  >
                    {line || " "}
                  </span>
                ))
              : "No file changes."}
          </pre>
        </div>
      )}
    </>
  );
}

function PullDiscussion({
  endpoint,
  number,
  canComment,
}: {
  endpoint: string;
  number: string;
  canComment: boolean;
}) {
  const [version, setVersion] = useState(0);
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const path = `${endpoint}/pulls/${number}/comments`;
  const comments = useData<PullComment[]>(path, version);
  return (
    <section
      className="panel issue-comments pull-discussion"
      aria-label="Unite request discussion"
    >
      <h3>Discussion</h3>
      <ErrorMessage error={error || comments.error} />
      {comments.loading ? (
        <Loading />
      ) : comments.data?.length ? (
        <div className="comment-list">
          {comments.data.map((comment) => (
            <article className="issue-comment" key={comment.id}>
              <div>
                <strong>@{comment.author}</strong>
                <span>{date(comment.created_at)}</span>
              </div>
              <p>{comment.body}</p>
            </article>
          ))}
        </div>
      ) : (
        <p className="muted small-text">No discussion yet.</p>
      )}
      {canComment && (
        <form
          className="comment-form"
          onSubmit={async (event) => {
            event.preventDefault();
            setBusy(true);
            setError("");
            try {
              await post(path, { body });
              setBody("");
              setVersion((value) => value + 1);
            } catch (error) {
              setError((error as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <label>
            Add to the discussion
            <textarea
              value={body}
              onChange={(event) => setBody(event.target.value)}
              maxLength={10000}
              rows={3}
              required
            />
          </label>
          <button
            className="button primary small-button"
            disabled={busy || !body.trim()}
          >
            {busy ? "Commenting…" : "Comment"}
          </button>
        </form>
      )}
    </section>
  );
}

function PullReviews({
  endpoint,
  number,
  headSHA,
  canReview,
}: {
  endpoint: string;
  number: string;
  headSHA: string;
  canReview: boolean;
}) {
  const [version, setVersion] = useState(0);
  const [state, setState] = useState<PullReview["state"]>("approved");
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const path = `${endpoint}/pulls/${number}/reviews`;
  const reviews = useData<PullReview[]>(path, version);
  const current = reviews.data?.filter((review) => !review.stale) || [];
  const approvals = current.filter(
    (review) => review.state === "approved",
  ).length;
  const changes = current.filter(
    (review) => review.state === "changes_requested",
  ).length;
  return (
    <section className="panel pull-reviews" aria-label="Formal reviews">
      <div className="review-heading">
        <div>
          <h3>Formal reviews</h3>
          <p className="muted small-text">
            {approvals} current approval{approvals === 1 ? "" : "s"} · {changes}{" "}
            change request{changes === 1 ? "" : "s"}
          </p>
        </div>
        <Badge kind={changes ? "" : approvals ? "green" : ""}>
          {changes
            ? "CHANGES REQUESTED"
            : approvals
              ? "APPROVED"
              : "UNREVIEWED"}
        </Badge>
      </div>
      <ErrorMessage error={error || reviews.error} />
      {reviews.loading ? (
        <Loading />
      ) : reviews.data?.length ? (
        <div className="review-list">
          {reviews.data.map((review) => (
            <article className="review-row" key={review.id}>
              <span className={`review-mark ${review.state}`}>
                {review.state === "approved" ? (
                  <Check size={16} />
                ) : (
                  <CircleDot size={16} />
                )}
              </span>
              <div>
                <strong>@{review.reviewer}</strong>{" "}
                <span>{review.state.replace("_", " ")}</span>
                {review.stale && <Badge>STALE</Badge>}
                {review.body && <p>{review.body}</p>}
              </div>
              <span className="muted small-text">
                {date(review.created_at)}
              </span>
            </article>
          ))}
        </div>
      ) : (
        <p className="muted small-text">No formal reviews yet.</p>
      )}
      {canReview && (
        <form
          className="review-form"
          onSubmit={async (event) => {
            event.preventDefault();
            setBusy(true);
            setError("");
            try {
              await post(path, { state, body, head_sha: headSHA });
              setBody("");
              setVersion((value) => value + 1);
            } catch (reviewError) {
              setError((reviewError as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <label>
            Decision
            <select
              aria-label="Review decision"
              value={state}
              onChange={(event) =>
                setState(event.target.value as PullReview["state"])
              }
            >
              <option value="approved">Approve</option>
              <option value="changes_requested">Request changes</option>
              <option value="commented">Comment</option>
            </select>
          </label>
          <label>
            Review summary
            <textarea
              value={body}
              onChange={(event) => setBody(event.target.value)}
              maxLength={10000}
              rows={3}
              required={state !== "approved"}
            />
          </label>
          <button className="button primary small-button" disabled={busy}>
            {busy ? "Submitting…" : "Submit review"}
          </button>
        </form>
      )}
    </section>
  );
}
