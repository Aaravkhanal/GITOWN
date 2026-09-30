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
  MessageSquarePlus,
  Package,
  Plus,
  Save,
  Settings,
  Sparkles,
  Terminal,
  Trash2,
  Users,
} from "lucide-react";
import { DropsPanel } from "@/components/ecosystem";
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
import { WatchMenu } from "./watch";
import {
  CommentHistory,
  IssueEditor,
  IssueFilterBar,
  IssueFormFields,
  IssueReferencesPanel,
  IssueTransfer,
  LinkedText,
  TemplateFieldsEditor,
  Pagination,
  SubIssuesPanel,
  emptyIssueFilter,
  formValues,
  issuesPerPage,
  usePagedIssues,
  type IssueFilter,
} from "./issues";
import {
  CommentEdit,
  IssuePlanning,
  OwnerDelivery,
  PullChecks,
  PullTimeline,
  RepositoryAccess,
  RepositoryPermissionSummary,
  SubscriptionMode,
  UnitePanel,
  type LineDraft,
} from "./collaboration";

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
              {r.visibility !== "public" ? (
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
          <WatchMenu endpoint={endpoint} />
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
          {r.ssh_clone_url && (
            <div className="copy-field">
              <code>git clone {r.ssh_clone_url}</code>
              <CopyButton text={`git clone ${r.ssh_clone_url}`} />
            </div>
          )}
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
          {
            key: "drops",
            icon: Package,
            label: "Drops",
            href: `${basePath}/drops`,
          },
          ...(r.can_manage || r.can_maintain
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
      ) : tab === "drops" ? (
        <DropsPanel endpoint={endpoint} canWrite={r.can_write && !r.archived} />
      ) : tab === "issues" && number ? (
        <IssueDetail endpoint={endpoint} repo={r} number={number} />
      ) : tab === "issues" ? (
        <IssueList
          endpoint={endpoint}
          repo={r}
          canTriage={r.can_triage}
          canComment={r.can_comment}
        />
      ) : tab === "board" ? (
        <BoardView
          endpoint={endpoint}
          canTriage={r.can_triage && !r.archived}
          canConfigure={r.can_write && !r.archived}
        />
      ) : tab === "pulls" && number ? (
        <PullRequestDetail endpoint={endpoint} number={number} repo={r} />
      ) : tab === "pulls" ? (
        <PullRequestList
          endpoint={endpoint}
          repo={r}
          branches={repo.data.branches}
        />
      ) : tab === "settings" && (r.can_manage || r.can_maintain) ? (
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
        {repo.homepage && (
          <a href={repo.homepage} target="_blank" rel="noopener noreferrer">
            <Globe2 size={15} /> Live demo
          </a>
        )}
        {repo.stack && (
          <span>
            <Code2 size={15} /> {repo.stack}
          </span>
        )}
        {repo.visibility !== "private" && (
          <Link className="text-link" href={`${repoPath(repo)}/showcase`}>
            Project showcase <ArrowRight size={14} />
          </Link>
        )}
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
            Kind
            <select
              aria-label={`Template ${index + 1} kind`}
              value={template.kind || "custom"}
              onChange={(event) => update(index, "kind", event.target.value)}
            >
              <option value="custom">Custom</option>
              <option value="bug">Bug</option>
              <option value="feature">Feature</option>
            </select>
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
          <TemplateFieldsEditor
            index={index}
            fields={template.fields || []}
            disabled={busy || archived}
            onChange={(fields) =>
              setDraft(
                current.map((item, position) =>
                  position === index ? { ...item, fields } : item,
                ),
              )
            }
          />
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
            setDraft([
              ...current,
              { name: "", title: "", body: "", kind: "custom" },
            ])
          }
        >
          Add issue template
        </button>
        <button
          className="button"
          type="button"
          disabled={
            busy ||
            archived ||
            current.length >= 9 ||
            (current.some((item) => item.kind === "bug") &&
              current.some((item) => item.kind === "feature"))
          }
          onClick={async () => {
            setError("");
            try {
              const offered = await api<IssueTemplate[]>(
                `${endpoint}/issue-templates?include_defaults=true`,
              );
              const kinds = new Set(current.map((item) => item.kind));
              setDraft([
                ...current,
                ...offered
                  .filter((item) => item.builtin && !kinds.has(item.kind))
                  .map((item) => ({ ...item, builtin: undefined })),
              ]);
            } catch (cause) {
              setError((cause as Error).message);
            }
          }}
        >
          Add default bug and feature forms
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
      <RepositoryPermissionSummary endpoint={endpoint} />
      {repo.can_manage && (
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
      )}
      {branches.length > 0 && (
        <BranchRuleSettings
          endpoint={endpoint}
          branches={branches}
          defaultBranch={repo.default_branch}
        />
      )}
      {repo.can_manage && (
        <IssueTemplateSettings endpoint={endpoint} archived={repo.archived} />
      )}
      {repo.can_manage && (
        <OwnerDelivery
          endpoint={endpoint}
          homepage={repo.homepage}
          stack={repo.stack}
        />
      )}
      {repo.can_manage && <RepositoryAccess endpoint={endpoint} repo={repo} />}
      {repo.can_manage && (
        <CollaboratorSettings endpoint={endpoint} owner={repo.owner} />
      )}
      {repo.can_manage && (
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
      )}
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
              require_unite: data.get("require_unite") === "on",
              require_resolved: data.get("require_resolved") === "on",
              require_up_to_date: data.get("require_up_to_date") === "on",
              restrict_push: data.get("restrict_push") === "on",
              require_signed: data.get("require_signed") === "on",
              require_maintainer_approval:
                data.get("require_maintainer_approval") === "on",
              required_checks: String(data.get("required_checks") || "")
                .split(",")
                .map((item) => item.trim())
                .filter(Boolean),
              required_reviewers: String(data.get("required_reviewers") || "")
                .split(",")
                .map((item) => item.trim())
                .filter(Boolean),
              allow_force_push: data.get("allow_force_push") === "on",
              allow_deletion: data.get("allow_deletion") === "on",
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
          <label className="checkbox-row">
            <input
              key={`unite-${branch}-${version}`}
              name="require_unite"
              type="checkbox"
              defaultChecked={rule.data?.require_unite ?? false}
            />
            Require a Unite request; reject direct Git pushes and browser edits
            to this branch
          </label>
          <label className="checkbox-row">
            <input
              key={`resolved-${branch}-${version}`}
              name="require_resolved"
              type="checkbox"
              defaultChecked={rule.data?.require_resolved ?? false}
            />
            Require every review conversation to be resolved
          </label>
          <label className="checkbox-row">
            <input
              key={`uptodate-${branch}-${version}`}
              name="require_up_to_date"
              type="checkbox"
              defaultChecked={rule.data?.require_up_to_date ?? false}
            />
            Require the branch to contain the latest base commit
          </label>
          <label className="checkbox-row">
            <input
              key={`restrict-${branch}-${version}`}
              name="restrict_push"
              type="checkbox"
              defaultChecked={rule.data?.restrict_push ?? false}
            />
            Only the owner or a maintainer can push or edit this branch
          </label>
          <label className="checkbox-row">
            <input
              key={`signed-${branch}-${version}`}
              name="require_signed"
              type="checkbox"
              defaultChecked={rule.data?.require_signed ?? false}
            />
            Require commits signed with a registered signing key (applies to
            pushes and merges; browser edits are refused)
          </label>
          <label className="checkbox-row">
            <input
              key={`maintainer-${branch}-${version}`}
              name="require_maintainer_approval"
              type="checkbox"
              defaultChecked={rule.data?.require_maintainer_approval ?? false}
            />
            Require an approval from the owner or a maintainer
          </label>
          <label>
            Required check contexts
            <input
              key={`checks-${branch}-${version}`}
              name="required_checks"
              defaultValue={(rule.data?.required_checks || []).join(", ")}
              placeholder="ci, lint"
            />
          </label>
          <label>
            Required reviewers
            <input
              key={`reviewers-${branch}-${version}`}
              name="required_reviewers"
              defaultValue={(rule.data?.required_reviewers || []).join(", ")}
              placeholder="username, crew:reviewers"
            />
          </label>
          <p className="muted small-text">
            Each listed person, or someone in each listed crew, must approve the
            latest commit.
          </p>
          <label className="checkbox-row">
            <input
              key={`force-${branch}-${version}`}
              name="allow_force_push"
              type="checkbox"
              defaultChecked={rule.data?.allow_force_push ?? false}
            />
            Allow force pushes to this branch
          </label>
          <label className="checkbox-row">
            <input
              key={`deletion-${branch}-${version}`}
              name="allow_deletion"
              type="checkbox"
              defaultChecked={rule.data?.allow_deletion ?? false}
            />
            Allow this branch to be deleted
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
  const refresh = () => setVersion((value) => value + 1);
  return (
    <div className="panel collaborator-settings">
      <div className="section-heading">
        <div>
          <h2>
            <Users size={18} /> Collaborators
          </h2>
          <p>
            People who accepted an invitation. Change a role or remove access
            at any time; add people with an invitation above.
          </p>
        </div>
      </div>
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

function IssueCreateForm({
  endpoint,
  onCreated,
  onCancel,
}: {
  endpoint: string;
  onCreated: () => void;
  onCancel: () => void;
}) {
  const templates = useData<IssueTemplate[]>(
    `${endpoint}/issue-templates?include_defaults=true`,
  );
  const [template, setTemplate] = useState<IssueTemplate | null>(null);
  const [issueTitle, setIssueTitle] = useState("");
  const [issueBody, setIssueBody] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const fields = template?.fields || [];
  return (
    <form
      className="panel form-panel inline-form"
      onSubmit={async (event) => {
        event.preventDefault();
        setBusy(true);
        setError("");
        try {
          await post(`${endpoint}/issues`, {
            title: issueTitle,
            body: issueBody,
            ...(fields.length
              ? { template: template!.name, fields: formValues(fields, values) }
              : {}),
          });
          onCreated();
        } catch (createError) {
          setError((createError as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <h2>Give your idea a starting point.</h2>
      <ErrorMessage error={error || templates.error} />
      {!!templates.data?.length && (
        <label>
          Start from template
          <select
            aria-label="Issue template"
            value={template?.name || ""}
            onChange={(event) => {
              const selected =
                templates.data?.find(
                  (item) => item.name === event.target.value,
                ) || null;
              setTemplate(selected);
              setValues({});
              if (selected) {
                setIssueTitle(selected.title);
                setIssueBody(selected.body);
              }
            }}
          >
            <option value="">Blank issue</option>
            {templates.data.map((item) => (
              <option key={item.name} value={item.name}>
                {item.name}
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
      <IssueFormFields fields={fields} values={values} onChange={setValues} />
      <label>
        {fields.length ? "Additional context" : "Description"}
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
        <button type="button" className="button" onClick={onCancel}>
          Cancel
        </button>
        <button disabled={busy} className="button primary">
          {busy ? "Creating…" : "Create issue"}
        </button>
      </div>
    </form>
  );
}

function IssueList({
  endpoint,
  repo,
  canTriage,
  canComment,
}: {
  endpoint: string;
  repo: Repo;
  canTriage: boolean;
  canComment: boolean;
}) {
  const [version, setVersion] = useState(0);
  const [showForm, setShowForm] = useState(false);
  const [showLabelForm, setShowLabelForm] = useState(false);
  const [showMilestoneForm, setShowMilestoneForm] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [filter, setFilter] = useState<IssueFilter>(emptyIssueFilter);
  const [page, setPage] = useState(1);
  const issues = usePagedIssues(endpoint, filter, page, version);
  const [expanded, setExpanded] = useState<number | null>(null);
  const labels = useData<Label[]>(`${endpoint}/labels`, version);
  const milestones = useData<Milestone[]>(`${endpoint}/milestones`, version);
  const refresh = () => setVersion((value) => value + 1);
  const applyFilter = (next: IssueFilter) => {
    setFilter(next);
    setPage(1);
    setExpanded(null);
  };
  return (
    <>
      <div className="section-heading">
        <div className="state-filters">
          <button
            className={filter.state === "open" ? "active" : ""}
            onClick={() => applyFilter({ ...filter, state: "open" })}
          >
            <CircleDot size={16} />
            Open <span>{issues.counts.open || 0}</span>
          </button>
          <button
            className={filter.state === "closed" ? "active" : ""}
            onClick={() => applyFilter({ ...filter, state: "closed" })}
          >
            <Check size={16} />
            Closed
          </button>
        </div>
        {canComment && (
          <div className="heading-actions">
            {canTriage && (
              <button
                className="button small-button"
                onClick={() => setShowMilestoneForm(!showMilestoneForm)}
              >
                <Plus size={15} />
                New milestone
              </button>
            )}
            {canTriage && (
              <button
                className="button small-button"
                onClick={() => setShowLabelForm(!showLabelForm)}
              >
                <Plus size={15} />
                New label
              </button>
            )}
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
      <IssueFilterBar
        repository={`${repo.owner}/${repo.name}`}
        filter={filter}
        labels={labels.data || []}
        milestones={milestones.data || []}
        signedIn={canComment}
        onChange={applyFilter}
      />
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
              refresh();
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
                      refresh();
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
              refresh();
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
                      refresh();
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
        <IssueCreateForm
          endpoint={endpoint}
          onCancel={() => setShowForm(false)}
          onCreated={() => {
            setShowForm(false);
            applyFilter({ ...filter, state: "open" });
            refresh();
          }}
        />
      )}
      {issues.loading && !issues.items.length ? (
        <Loading />
      ) : issues.items.length ? (
        <div className="panel issue-list">
          {issues.items.map((issue) => (
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
                  <span className="issue-row-labels">
                    {issue.labels?.map((label) => (
                      <LabelChip
                        key={label.name}
                        label={{
                          id: label.name,
                          name: label.name,
                          color: label.color,
                          description: "",
                          created_at: "",
                        }}
                      />
                    ))}
                  </span>
                  <p>
                    <Link
                      href={`${repoPath(repo)}/issues/${issue.number}`}
                      aria-label={`Open issue #${issue.number}`}
                    >
                      #{issue.number}
                    </Link>{" "}
                    opened {date(issue.created_at)} by {issue.author}
                    {issue.milestone ? ` · ${issue.milestone}` : ""}
                    {issue.sub_issues?.total
                      ? ` · ${issue.sub_issues.closed}/${issue.sub_issues.total} sub-issues`
                      : ""}
                    {issue.comments ? ` · ${issue.comments} comments` : ""}
                  </p>
                </div>
                <Badge kind={issue.state === "open" ? "green" : ""}>
                  {issue.state}
                </Badge>
              </div>
              {expanded === issue.number && (
                <IssueView
                  endpoint={endpoint}
                  repo={repo}
                  issue={issue}
                  canTriage={canTriage}
                  canComment={canComment}
                  labels={labels.data || []}
                  milestones={milestones.data || []}
                  version={version}
                  onChange={refresh}
                />
              )}
            </article>
          ))}
        </div>
      ) : (
        <div className="empty-state panel">
          <CircleDot size={30} />
          <h3>No {filter.state} issues</h3>
          <p>A place for bugs, ideas, and the things you want to build next.</p>
        </div>
      )}
      <Pagination
        page={page}
        total={issues.total}
        perPage={issuesPerPage}
        onPage={(next) => {
          setPage(next);
          setExpanded(null);
        }}
      />
    </>
  );
}

const closeReasons: Record<string, string> = {
  completed: "completed",
  not_planned: "not planned",
  duplicate: "a duplicate",
};

function IssueView({
  endpoint,
  repo,
  issue,
  canTriage,
  canComment,
  labels,
  milestones,
  version,
  onChange,
}: {
  endpoint: string;
  repo: Repo;
  issue: Issue;
  canTriage: boolean;
  canComment: boolean;
  labels: Label[];
  milestones: Milestone[];
  version: number;
  onChange: () => void;
}) {
  const me = useData<{ user: { username: string } | null }>("/auth/me");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const isAuthor = me.data?.user?.username === issue.author;
  const canEdit = canComment && (canTriage || isAuthor);
  const repository = { owner: repo.owner, name: repo.name };
  async function setState(state: "open" | "closed", reason = "") {
    setBusy(true);
    setError("");
    try {
      await api(`${endpoint}/issues/${issue.number}`, {
        method: "PATCH",
        body: JSON.stringify(
          reason ? { state, state_reason: reason } : { state },
        ),
      });
      onChange();
    } catch (stateError) {
      setError((stateError as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="issue-body">
      <ErrorMessage error={error} />
      {issue.state === "closed" && issue.state_reason && (
        <p className="muted small-text">
          Closed as {closeReasons[issue.state_reason] || issue.state_reason}
          {issue.duplicate_of ? (
            <>
              {" "}
              of{" "}
              <Link href={`${repoPath(repo)}/issues/${issue.duplicate_of}`}>
                #{issue.duplicate_of}
              </Link>
            </>
          ) : null}
          .
        </p>
      )}
      <p className="issue-description">
        {issue.body ? (
          <LinkedText
            text={issue.body}
            owner={repo.owner}
            repository={repo.name}
          />
        ) : (
          "No description provided."
        )}
      </p>
      {canEdit && (
        <IssueEditor endpoint={endpoint} issue={issue} onSaved={onChange} />
      )}
      {issue.pinned && <Badge>Pinned</Badge>}
      {issue.priority && issue.priority !== "none" && (
        <Badge>{issue.priority}</Badge>
      )}
      <IssuePlanning
        endpoint={endpoint}
        issue={issue}
        canTriage={canTriage}
        onSaved={onChange}
      />
      <IssueComments
        endpoint={endpoint}
        repo={repo}
        issueNumber={issue.number}
        canComment={canComment}
        onComment={onChange}
      />
      <IssueLabels
        endpoint={endpoint}
        issueNumber={issue.number}
        labels={labels}
        canTriage={canTriage}
      />
      <IssueAssigneePicker
        endpoint={endpoint}
        issueNumber={issue.number}
        canTriage={canTriage}
      />
      <SubIssuesPanel
        endpoint={endpoint}
        repository={repository}
        number={issue.number}
        canTriage={canTriage && !repo.archived}
        onChange={onChange}
      />
      <IssueDependenciesPicker
        endpoint={endpoint}
        issueNumber={issue.number}
        canTriage={canTriage}
      />
      <IssueReferencesPanel
        endpoint={endpoint}
        number={issue.number}
        version={version}
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
        milestones={milestones}
        canTriage={canTriage}
        onChange={onChange}
      />
      {canEdit &&
        (issue.state === "open" ? (
          <div className="form-actions issue-actions">
            <button
              disabled={busy}
              className="button small-button"
              onClick={() => setState("closed")}
            >
              Close issue
            </button>
            <button
              disabled={busy}
              className="button small-button"
              onClick={() => setState("closed", "not_planned")}
            >
              Close as not planned
            </button>
          </div>
        ) : (
          <button
            disabled={busy}
            className="button small-button"
            onClick={() => setState("open")}
          >
            Reopen issue
          </button>
        ))}
      {repo.can_write && !repo.archived && (
        <IssueTransfer
          endpoint={endpoint}
          repository={repository}
          number={issue.number}
        />
      )}
    </div>
  );
}

function IssueDetail({
  endpoint,
  repo,
  number,
}: {
  endpoint: string;
  repo: Repo;
  number: string;
}) {
  const [version, setVersion] = useState(0);
  const issue = useData<Issue>(`${endpoint}/issues/${number}`, version);
  const labels = useData<Label[]>(`${endpoint}/labels`, version);
  const milestones = useData<Milestone[]>(`${endpoint}/milestones`, version);
  if (issue.loading) return <Loading />;
  if (!issue.data)
    return (
      <>
        <ErrorMessage error={issue.error || "Issue not found."} />
        <Link className="button" href={`${repoPath(repo)}/issues`}>
          Back to issues
        </Link>
      </>
    );
  return (
    <>
      <div className="section-heading">
        <div>
          <h2>
            {issue.data.title}{" "}
            <span className="muted">#{issue.data.number}</span>
          </h2>
          <p className="muted small-text">
            <Badge kind={issue.data.state === "open" ? "green" : ""}>
              {issue.data.state}
            </Badge>{" "}
            {issue.data.author} opened this {date(issue.data.created_at)}
            {issue.data.parent ? (
              <>
                {" "}
                · part of{" "}
                <Link href={`${repoPath(repo)}/issues/${issue.data.parent}`}>
                  #{issue.data.parent}
                </Link>
              </>
            ) : null}
          </p>
        </div>
        <Link className="button small-button" href={`${repoPath(repo)}/issues`}>
          All issues
        </Link>
      </div>
      <article className="panel issue-item">
        <IssueView
          endpoint={endpoint}
          repo={repo}
          issue={issue.data}
          canTriage={repo.can_triage}
          canComment={repo.can_comment}
          labels={labels.data || []}
          milestones={milestones.data || []}
          version={version}
          onChange={() => setVersion((value) => value + 1)}
        />
      </article>
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
  canTriage,
}: {
  endpoint: string;
  issueNumber: number;
  canTriage: boolean;
}) {
  const [version, setVersion] = useState(0);
  const candidates = useData<Issue[]>(
    canTriage ? `${endpoint}/issues?state=open&per_page=100` : null,
    version,
  );
  const issues = candidates.data || [];
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
  const path = `${endpoint}/issues/${issueNumber}/subscription`;
  const subscription = useData<{ subscribed: boolean }>(path, refreshVersion);
  if (!canSubscribe) return null;
  return (
    <section className="issue-labels" aria-label="Issue updates">
      <h4>Updates</h4>
      <ErrorMessage error={subscription.error} />
      <p className="muted small-text">
        Participate follows the conversation and state changes on this issue.
        Watch also includes every other update, and ignore stays quiet even
        when you are mentioned.
      </p>
      <SubscriptionMode path={path} version={refreshVersion} />
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
  repo,
  issueNumber,
  canComment,
  onComment,
}: {
  endpoint: string;
  repo: Repo;
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
                {comment.edited && (
                  <CommentHistory path={`${path}/${comment.id}`} />
                )}
              </div>
              <p>
                <LinkedText
                  text={comment.body}
                  owner={repo.owner}
                  repository={repo.name}
                />
              </p>
              {comment.editable && (
                <CommentEdit
                  path={`${path}/${comment.id}`}
                  initial={comment.body}
                  onSaved={() => setVersion((value) => value + 1)}
                />
              )}
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
                  draft: data.get("draft") === "on",
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
            <label className="checkbox-row">
              <input name="draft" type="checkbox" /> Create as a draft
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
  const session = useData<{ user: { username: string } | null }>("/auth/me");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [mergeMethod, setMergeMethod] = useState("merge");
  const [deleteBranch, setDeleteBranch] = useState(false);
  const [lineDraft, setLineDraft] = useState<LineDraft | null>(null);
  const [morePages, setMorePages] = useState<{ diff: string; truncated: boolean }[]>([]);
  const [loadingMore, setLoadingMore] = useState(false);
  useEffect(() => setMorePages([]), [detail.data]);
  if (detail.loading) return <Loading />;
  if (!detail.data) return <ErrorMessage error={detail.error} />;
  const { pull, mergeable, diff, diff_error } = detail.data;
  const viewer = session.data?.user?.username;
  const diffPages = [diff, ...morePages.map((page) => page.diff)];
  const diffLines = diffPages.join("\n").split("\n");
  const lineInfo = annotateDiff(diffLines);
  const moreAvailable = morePages.length
    ? morePages[morePages.length - 1].truncated
    : !!detail.data.diff_truncated;
  const canLineComment = repo.can_comment && pull.state === "open";
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
      {pull.draft && <Badge>Draft</Badge>}
      <PullSubscription
        endpoint={endpoint}
        number={number}
        canSubscribe={repo.can_comment}
      />
      {notice && <div className="success-box">{notice}</div>}
      <UnitePanel
        endpoint={endpoint}
        number={number}
        canTriage={repo.can_triage}
        canComment={repo.can_comment}
        headSHA={detail.data.head_sha}
        viewer={viewer}
        district={repo.district}
        lineDraft={lineDraft}
      />
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
        canMaintain={!!repo.can_maintain}
        viewer={viewer}
      />
      <PullChecks endpoint={endpoint} headSHA={detail.data.head_sha} />
      <PullTimeline base={`${endpoint}/pulls/${number}`} />
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
            {pull.state === "open" && (
              <button
                className="button small-button"
                disabled={busy}
                onClick={async () => {
                  setBusy(true);
                  setError("");
                  try {
                    await patch(`${endpoint}/pulls/${number}`, {
                      draft: !pull.draft,
                    });
                    setVersion((value) => value + 1);
                  } catch (saveError) {
                    setError((saveError as Error).message);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                {pull.draft ? "Mark ready" : "Convert to draft"}
              </button>
            )}
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
            <div className="merge-choices">
              <label>
                Merge method
                <select
                  value={mergeMethod}
                  onChange={(event) => setMergeMethod(event.target.value)}
                  disabled={pull.state === "merging"}
                >
                  <option value="merge">Merge commit</option>
                  <option value="squash">Squash</option>
                  <option value="rebase">Rebase</option>
                </select>
              </label>
              <label className="checkbox-row">
                <input
                  type="checkbox"
                  checked={deleteBranch}
                  onChange={(event) => setDeleteBranch(event.target.checked)}
                />
                Delete the source branch after merging
              </label>
            <button
              disabled={
                busy ||
                pull.draft ||
                (!mergeable && pull.state !== "merging")
              }
              className="button primary"
              onClick={async () => {
                if (pull.draft) return;
                if (
                  !window.confirm(
                    `Merge unite request #${number} into ${pull.base_branch}?`,
                  )
                )
                  return;
                setBusy(true);
                setError("");
                setNotice("");
                try {
                  const merged = await post<Pull>(
                    `${endpoint}/pulls/${number}/merge`,
                    {
                      head_sha: detail.data!.head_sha,
                      base_sha: detail.data!.base_sha,
                      method: mergeMethod,
                      delete_branch: deleteBranch,
                    },
                  );
                  if (merged.branch_deleted) {
                    setNotice(`Deleted ${pull.head_branch}.`);
                  } else if (merged.branch_delete_error) {
                    setNotice(
                      `${pull.head_branch} was kept: ${merged.branch_delete_error}`,
                    );
                  }
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
                : pull.draft
                  ? "Drafts cannot merge"
                  : pull.state === "merging"
                    ? "Recover merge"
                    : "Merge unite request"}
            </button>
            </div>
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
              ? diffLines.map((line, i) => {
                  const info = lineInfo[i];
                  const target = info?.right ?? info?.left;
                  return (
                    <span
                      className={`diff-line ${line.startsWith("+") && !line.startsWith("+++") ? "addition" : line.startsWith("-") && !line.startsWith("---") ? "deletion" : line.startsWith("@@") ? "diff-hunk" : ""}`}
                      key={i}
                    >
                      {canLineComment && info && target ? (
                        <button
                          className="diff-comment"
                          type="button"
                          aria-label={`Comment on ${info.path} line ${target}`}
                          onClick={() => {
                            setLineDraft({
                              path: info.path,
                              side: info.right ? "right" : "left",
                              line: target,
                            });
                            document
                              .getElementById("line-comment-form")
                              ?.scrollIntoView({ block: "center" });
                          }}
                        >
                          <MessageSquarePlus size={12} />
                        </button>
                      ) : null}
                      {line || " "}
                    </span>
                  );
                })
              : "No file changes."}
          </pre>
          {moreAvailable && (
            <button
              className="button small-button"
              type="button"
              disabled={loadingMore}
              onClick={async () => {
                setLoadingMore(true);
                setError("");
                try {
                  const page = await api<PullDetail>(
                    `${endpoint}/pulls/${number}?diff_offset=${diffLines.length}&diff_limit=400`,
                  );
                  setMorePages([
                    ...morePages,
                    { diff: page.diff, truncated: !!page.diff_truncated },
                  ]);
                } catch (loadError) {
                  setError((loadError as Error).message);
                } finally {
                  setLoadingMore(false);
                }
              }}
            >
              {loadingMore ? "Loading…" : "Load more changes"}
            </button>
          )}
        </div>
      )}
    </>
  );
}

type DiffLineInfo = { path: string; left?: number; right?: number };

// annotateDiff maps each rendered diff line to the file and the old (left) or
// new (right) line number it shows, using hunk lengths to find hunk ends.
function annotateDiff(lines: string[]): (DiffLineInfo | null)[] {
  let path = "";
  let oldLine = 0;
  let newLine = 0;
  let oldLeft = 0;
  let newLeft = 0;
  return lines.map((line) => {
    if (oldLeft > 0 || newLeft > 0) {
      if (line.startsWith("+")) {
        newLeft--;
        return { path, right: newLine++ };
      }
      if (line.startsWith("-")) {
        oldLeft--;
        return { path, left: oldLine++ };
      }
      if (line.startsWith(" ")) {
        oldLeft--;
        newLeft--;
        return { path, left: oldLine++, right: newLine++ };
      }
      if (line.startsWith("\\")) return null;
      oldLeft = 0;
      newLeft = 0;
    }
    const hunk = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(line);
    if (hunk) {
      oldLine = Number(hunk[1]);
      oldLeft = hunk[2] === undefined ? 1 : Number(hunk[2]);
      newLine = Number(hunk[3]);
      newLeft = hunk[4] === undefined ? 1 : Number(hunk[4]);
      return null;
    }
    if (line.startsWith("+++ ") && line !== "+++ /dev/null") {
      path = line.slice(4).replace(/^b\//, "");
    } else if (line.startsWith("--- ") && line !== "--- /dev/null") {
      path = line.slice(4).replace(/^a\//, "");
    }
    return null;
  });
}

function PullSubscription({
  endpoint,
  number,
  canSubscribe,
}: {
  endpoint: string;
  number: string;
  canSubscribe: boolean;
}) {
  const path = `${endpoint}/pulls/${number}/subscription`;
  const subscription = useData<{ subscribed: boolean }>(path);
  if (!canSubscribe) return null;
  return (
    <section className="issue-labels" aria-label="Unite updates">
      <h4>Updates</h4>
      <ErrorMessage error={subscription.error} />
      <p className="muted small-text">
        Participate follows comments, reviews, state changes, and merges. Watch
        also includes check results, and ignore stays quiet even when you are
        mentioned.
      </p>
      <SubscriptionMode path={path} version={0} />
    </section>
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
              <CommentEdit
                path={`${path}/${comment.id}`}
                initial={comment.body}
              />
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
  canMaintain,
  viewer,
}: {
  endpoint: string;
  number: string;
  headSHA: string;
  canReview: boolean;
  canMaintain: boolean;
  viewer?: string;
}) {
  const [version, setVersion] = useState(0);
  const [state, setState] = useState<PullReview["state"]>("approved");
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const path = `${endpoint}/pulls/${number}/reviews`;
  const reviews = useData<PullReview[]>(path, version);
  // Mirror the merge guard: each reviewer's latest non-dismissed review of
  // the current head counts once.
  const latest = new Map<string, PullReview>();
  for (const review of reviews.data || []) {
    if (review.stale || review.dismissed) continue;
    const seen = latest.get(review.reviewer);
    if (!seen || seen.created_at <= review.created_at) {
      latest.set(review.reviewer, review);
    }
  }
  const current = [...latest.values()];
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
                {review.dismissed && <Badge>DISMISSED</Badge>}
                {review.body && <p>{review.body}</p>}
                {review.dismissed && review.dismissal_reason && (
                  <p className="muted small-text">
                    Dismissed: {review.dismissal_reason}
                  </p>
                )}
                {!review.dismissed &&
                  review.state !== "commented" &&
                  (canMaintain || review.reviewer === viewer) && (
                    <button
                      className="text-button"
                      type="button"
                      aria-label={`Dismiss review by ${review.reviewer}`}
                      onClick={async () => {
                        const reason = window.prompt(
                          "Why are you dismissing this review? The reason is kept in the timeline.",
                        );
                        if (!reason?.trim()) return;
                        setError("");
                        try {
                          await post(`${path}/${review.id}/dismiss`, {
                            reason: reason.trim(),
                          });
                          setVersion((value) => value + 1);
                        } catch (dismissError) {
                          setError((dismissError as Error).message);
                        }
                      }}
                    >
                      Dismiss review
                    </button>
                  )}
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
