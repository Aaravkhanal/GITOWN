"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  ArrowLeft,
  ArrowRight,
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
  LockKeyhole,
  Plus,
  Save,
  Settings,
  Terminal,
} from "lucide-react";
import {
  api,
  patch,
  post,
  date,
  repoPath,
  type Repo,
  type Commit,
  type Tree,
  type Issue,
  type Pull,
  type PullDetail,
} from "@/lib/api";
import {
  Avatar,
  Badge,
  CopyButton,
  ErrorMessage,
  Loading,
  useData,
} from "./ui";

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
        <span>{owner}</span>
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
          </div>
          <p>{r.description || "A home for your next great idea."}</p>
        </div>
        <button className="button" onClick={() => setClone(!clone)}>
          <Terminal size={16} />
          Clone repository
        </button>
      </div>
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
            key: "pulls",
            icon: GitPullRequest,
            label: "Pull requests",
            href: `${basePath}/pulls`,
          },
          {
            key: "commits",
            icon: History,
            label: "Commits",
            href: `${basePath}/commits`,
          },
          ...(r.can_write
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
        <IssueList endpoint={endpoint} canWrite={r.can_write} />
      ) : tab === "pulls" && number ? (
        <PullRequestDetail endpoint={endpoint} number={number} repo={r} />
      ) : tab === "pulls" ? (
        <PullRequestList
          endpoint={endpoint}
          repo={r}
          branches={repo.data.branches}
        />
      ) : tab === "settings" && r.can_write ? (
        <RepositorySettings
          endpoint={endpoint}
          repo={r}
          onSaved={() => setVersion((v) => v + 1)}
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
  const query = `?ref=${encodeURIComponent(branch)}&path=${encodeURIComponent(path)}`;
  const tree = useData<Tree>(branch ? `${endpoint}/tree${query}` : null);
  const commits = useData<Commit[]>(
    branch ? `${endpoint}/commits?ref=${encodeURIComponent(branch)}` : null,
  );
  const readme = useData<Tree>(
    tree.data?.entries.find((e) => e.name.toLowerCase() === "readme.md")
      ? `${endpoint}/tree?ref=${encodeURIComponent(branch)}&path=${encodeURIComponent((path ? path + "/" : "") + tree.data.entries.find((e) => e.name.toLowerCase() === "readme.md")!.name)}`
      : null,
  );
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
              </>
            ) : (
              <span className="muted">Repository files</span>
            )}
          </div>
          <ErrorMessage error={tree.error} />
          {tree.loading ? (
            <Loading />
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
              </div>
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
            </div>
          ) : tree.data?.binary ? (
            <div className="empty-state">
              <File size={26} />
              <h3>Binary file</h3>
              <p>Clone this repository to open the file locally.</p>
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
          Open a pull request <ArrowRight size={14} />
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

function RepositorySettings({
  endpoint,
  repo,
  onSaved,
}: {
  endpoint: string;
  repo: Repo;
  onSaved: () => void;
}) {
  const [visibility, setVisibility] = useState(repo.visibility);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
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
            window.setTimeout(onSaved, 700);
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
    </section>
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
  canWrite,
}: {
  endpoint: string;
  canWrite: boolean;
}) {
  const [version, setVersion] = useState(0);
  const issues = useData<Issue[]>(`${endpoint}/issues`, version);
  const [showForm, setShowForm] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [filter, setFilter] = useState("open");
  const [expanded, setExpanded] = useState<number | null>(null);
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
        {canWrite && (
          <button
            className="button primary small-button"
            onClick={() => setShowForm(!showForm)}
          >
            <Plus size={15} />
            New issue
          </button>
        )}
      </div>
      <ErrorMessage error={error || issues.error} />
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
          <label>
            Title
            <input
              name="title"
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
                  {canWrite && (
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
          Pull requests <span className="count">{pulls.data?.length || 0}</span>
        </h2>
        {repo.can_write && (
          <button
            className="button primary small-button"
            onClick={() => setShowForm(!showForm)}
          >
            <Plus size={15} />
            New pull request
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
                {busy ? "Creating…" : "Create pull request"}
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
            Push a branch and open a pull request to compare and merge your
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
        All pull requests
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
                    `Merge pull request #${number} into ${pull.base_branch}?`,
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
                  : "Merge pull request"}
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
