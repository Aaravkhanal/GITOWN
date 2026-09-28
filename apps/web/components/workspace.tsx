"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { createContext, useContext, useEffect, useRef, useState } from "react";
import {
  ArrowDownToLine,
  ArrowRight,
  ArrowUpRight,
  BookOpen,
  Check,
  ChevronRight,
  CircleDot,
  Code2,
  Command,
  Compass,
  FolderGit2,
  GitBranch,
  GitCommitHorizontal,
  GitPullRequest,
  Globe2,
  KeyRound,
  LayoutGrid,
  LockKeyhole,
  LogOut,
  Plus,
  Search,
  ShieldCheck,
  Terminal,
  Trash2,
  RotateCcw,
  X,
} from "lucide-react";
import {
  api,
  post,
  date,
  repoPath,
  type User,
  type Repo,
  type Activity,
  type Token,
  type DeletedRepository,
  type BrowserSession,
} from "@/lib/api";
import {
  Avatar,
  Badge,
  CopyButton,
  ErrorMessage,
  Loading,
  useData,
} from "./ui";
import { RepositoryPage } from "./repository";

type Session = {
  user: User | null;
  loading: boolean;
  refresh: () => void;
  signup: boolean;
};
const SessionContext = createContext<Session>({
  user: null,
  loading: true,
  refresh: () => {},
  signup: false,
});
export function useSession() {
  return useContext(SessionContext);
}

export function Workspace({ segments }: { segments: string[] }) {
  const [version, setVersion] = useState(0);
  const session = useData<{ user: User | null; signup_enabled: boolean }>(
    "/auth/me",
    version,
  );
  const user = session.data?.user || null;
  const [search, setSearch] = useState("");
  const searchInput = useRef<HTMLInputElement>(null);
  const router = useRouter();
  const section = segments[0] || "home";
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === "k") {
        event.preventDefault();
        searchInput.current?.focus();
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, []);
  async function logout() {
    try {
      await post("/auth/logout", {});
      setVersion((v) => v + 1);
      router.push("/");
    } catch {
      setNotice("Could not sign out. Please try again.");
    }
  }
  const [notice, setNotice] = useState("");
  const authPage = section === "login" || section === "register";
  return (
    <SessionContext.Provider
      value={{
        user,
        loading: session.loading,
        signup: !!session.data?.signup_enabled,
        refresh: () => setVersion((v) => v + 1),
      }}
    >
      <header className="topbar">
        <Link className="brand" href="/" aria-label="GITOWN home">
          <span className="brand-mark">
            <GitBranch size={22} strokeWidth={2.5} />
          </span>
          GITOWN<span className="brand-dot">.</span>
        </Link>
        <nav className="top-nav" aria-label="Main navigation">
          <Link href="/" className={section === "home" ? "active" : ""}>
            Workspace
          </Link>
          <Link
            href="/explore"
            className={section === "explore" ? "active" : ""}
          >
            Explore
          </Link>
        </nav>
        <form className="global-search" action="/explore">
          <Search size={16} />
          <input
            ref={searchInput}
            name="q"
            placeholder="Find a repository…"
            aria-label="Find a repository"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <kbd>
            <Command size={10} /> K
          </kbd>
        </form>
        <div className="header-actions">
          {user ? (
            <>
              <Link
                className="icon-button"
                href="/new"
                aria-label="Create a repository"
              >
                <Plus size={20} />
              </Link>
              <span className="header-divider" />
              <Link href="/settings/tokens" title="Account and access tokens">
                <Avatar name={user.username} small />
              </Link>
              <button
                className="icon-button signout"
                onClick={logout}
                aria-label="Sign out"
              >
                <LogOut size={17} />
              </button>
            </>
          ) : (
            <Link className="button small-button" href="/login">
              Sign in <ArrowRight size={14} />
            </Link>
          )}
        </div>
      </header>
      <div className={`app-layout ${authPage ? "auth-layout" : ""}`}>
        {!authPage && (
          <aside className="sidebar">
            <div className="workspace-label">
              <span className="workspace-icon">
                {user ? user.username[0].toUpperCase() : "G"}
              </span>
              <div>
                <strong>{user?.username || "Your workspace"}</strong>
                <span>Personal account</span>
              </div>
              <Badge>ALPHA</Badge>
            </div>
            <p className="nav-label">WORKSPACE</p>
            <nav className="side-nav" aria-label="Workspace navigation">
              <Link className={section === "home" ? "selected" : ""} href="/">
                <LayoutGrid size={17} /> Overview
              </Link>
              <Link
                className={section === "explore" ? "selected" : ""}
                href="/explore"
              >
                <Compass size={17} /> Explore repositories
              </Link>
              <Link className={section === "new" ? "selected" : ""} href="/new">
                <FolderGit2 size={17} /> New repository{" "}
                <Plus className="trailing" size={15} />
              </Link>
              <Link
                className={section === "settings" ? "selected" : ""}
                href="/settings/tokens"
              >
                <KeyRound size={17} /> Access tokens
              </Link>
              {user && (
                <Link className="" href="/settings/sessions">
                  <ShieldCheck size={17} /> Signed-in devices
                </Link>
              )}
              {user && (
                <Link className="" href="/settings/security">
                  <LockKeyhole size={17} /> Account security
                </Link>
              )}
              {user && (
                <Link className="" href="/settings/repositories">
                  <Trash2 size={17} /> Deleted repositories
                </Link>
              )}
            </nav>
            <div className="sidebar-note">
              <span className="note-icon">
                <Code2 size={20} />
              </span>
              <strong>Make something yours.</strong>
              <p>Every great project starts with a first commit.</p>
              <Link href="/new">
                Start a repository <ArrowUpRight size={14} />
              </Link>
            </div>
            <div className="sidebar-bottom">
              <span className="live-dot" /> Independent by design
              <span>v0.1</span>
            </div>
          </aside>
        )}
        <main className="main-content" id="main-content">
          <ErrorMessage
            error={
              session.error
                ? "The GITOWN API is unavailable. Check that the backend and database are running."
                : notice
            }
          />
          {session.loading ? (
            <Loading />
          ) : authPage ? (
            <AuthPage mode={section} />
          ) : section === "new" ? (
            <NewRepository />
          ) : section === "settings" && segments[1] === "repositories" ? (
            <DeletedRepositoriesPage />
          ) : section === "settings" && segments[1] === "sessions" ? (
            <SessionsPage />
          ) : section === "settings" && segments[1] === "security" ? (
            <SecurityPage />
          ) : section === "settings" ? (
            <TokensPage />
          ) : section === "repos" && segments.length >= 3 ? (
            <RepositoryPage
              key={segments.slice(0, 3).join("/")}
              owner={segments[1]}
              name={segments[2]}
              tab={segments[3] || "code"}
              number={segments[4]}
            />
          ) : section === "home" || section === "explore" ? (
            <Dashboard explore={section === "explore"} />
          ) : (
            <div className="empty-state">
              <h1>Page not found</h1>
              <Link className="button" href="/">
                Back to workspace
              </Link>
            </div>
          )}
          <footer className="footer">
            <span>
              <GitBranch size={14} /> GITOWN{" "}
              <span className="muted">/ A home for your code.</span>
            </span>
            <span>Built to build things.</span>
          </footer>
        </main>
      </div>
    </SessionContext.Provider>
  );
}

function DeletedRepositoriesPage() {
  const { user } = useSession();
  const router = useRouter();
  const [version, setVersion] = useState(0);
  const repositories = useData<DeletedRepository[]>(
    user ? "/user/deleted-repositories" : null,
    version,
  );
  const [error, setError] = useState("");
  if (!user) return <SignInPrompt />;
  return (
    <div className="form-page wide-form">
      <div className="breadcrumb">
        <span>Settings</span>
        <ChevronRight size={12} /> Deleted repositories
      </div>
      <div className="page-heading">
        <div>
          <h1>Deleted repositories.</h1>
          <p>Restore repositories before their scheduled purge date.</p>
        </div>
        <Trash2 size={30} className="muted" />
      </div>
      <ErrorMessage error={error || repositories.error} />
      <div className="panel token-list">
        {repositories.loading ? (
          <Loading />
        ) : repositories.data?.length ? (
          repositories.data.map((repo) => (
            <div className="token-row" key={repo.id}>
              <Trash2 size={19} />
              <div>
                <strong>
                  {repo.owner}/{repo.name}
                </strong>
                <span>
                  Deleted {date(repo.deleted_at)} · Purges after{" "}
                  {date(repo.purge_after)}
                </span>
              </div>
              <button
                className="button small-button"
                onClick={async () => {
                  setError("");
                  try {
                    const restored = await post<{
                      owner: string;
                      name: string;
                    }>(`/user/deleted-repositories/${repo.id}/restore`, {});
                    setVersion((value) => value + 1);
                    router.push(`/repos/${restored.owner}/${restored.name}`);
                  } catch (restoreError) {
                    setError((restoreError as Error).message);
                  }
                }}
                type="button"
              >
                <RotateCcw size={15} /> Restore
              </button>
            </div>
          ))
        ) : (
          <p className="muted padded">No deleted repositories.</p>
        )}
      </div>
    </div>
  );
}

function Dashboard({ explore }: { explore: boolean }) {
  const { user } = useSession();
  const repos = useData<Repo[]>(
    `/repos${!explore && user ? "?mine=true" : ""}`,
  );
  const activity = useData<Activity[]>(
    user && !explore ? "/user/activity" : null,
  );
  const [filter, setFilter] = useState("");
  const [visibility, setVisibility] = useState("all");
  const [sort, setSort] = useState("recent");
  useEffect(() => {
    if (explore)
      setFilter(new URLSearchParams(window.location.search).get("q") || "");
  }, [explore]);
  const visible = (repos.data || [])
    .filter(
      (r) =>
        `${r.owner}/${r.name} ${r.description}`
          .toLowerCase()
          .includes(filter.toLowerCase()) &&
        (visibility === "all" || r.visibility === visibility),
    )
    .sort((a, b) =>
      sort === "name"
        ? a.name.localeCompare(b.name)
        : b.created_at.localeCompare(a.created_at),
    );
  return (
    <>
      <div className="breadcrumb">
        <span>Workspace</span>
        <ChevronRight size={12} />
        {explore ? "Explore" : "Overview"}
      </div>
      <div className="page-heading">
        <div>
          <div className="eyebrow">
            <span className="live-dot" /> YOUR NEXT CHAPTER STARTS HERE
          </div>
          <h1>
            {explore
              ? "Explore repositories"
              : user
                ? `Welcome back, ${user.display_name.split(" ")[0]}.`
                : "Great ideas deserve a home."}
          </h1>
          <p>
            {explore
              ? "Discover code and find your next source of inspiration."
              : "Your code, your ideas, your corner of the internet. Let’s build something."}
          </p>
        </div>
        <Link href={user ? "/new" : "/register"} className="button primary">
          <Plus size={17} />
          {user ? "New repository" : "Create your workspace"}
        </Link>
      </div>
      {!explore && (
        <div className="welcome-banner">
          <div className="banner-copy">
            <Badge kind="green">BUILT FOR BUILDERS</Badge>
            <h2>
              From first commit
              <br />
              to your next big thing<span>.</span>
            </h2>
            <p>
              A space to version your work, bring ideas together,
              <br className="desktop-only" /> and keep moving forward.
            </p>
            <Link href={user ? "/new" : "/register"}>
              Let’s make it happen <ArrowRight size={16} />
            </Link>
          </div>
          <div className="branch-art" aria-hidden="true">
            <div className="art-grid" />
            <svg viewBox="0 0 420 210">
              <path d="M40 175H330Q360 175 360 145V35" />
              <path d="M40 110H210Q240 110 240 80V35" />
              <path
                d="M40 45H155Q185 45 185 75V145Q185 175 215 175"
                className="art-accent"
              />
              <circle cx="70" cy="175" r="7" />
              <circle cx="285" cy="175" r="7" />
              <circle cx="110" cy="110" r="7" />
              <circle cx="240" cy="35" r="7" />
              <circle cx="360" cy="70" r="7" />
              <circle cx="40" cy="45" r="7" className="art-accent" />
              <circle cx="185" cy="120" r="7" className="art-accent" />
            </svg>
            <span className="art-label first">
              <GitCommitHorizontal size={13} /> your first commit
            </span>
            <span className="art-label second">
              <GitBranch size={13} /> endless possibilities
            </span>
          </div>
        </div>
      )}
      {!explore && (
        <div className="stat-strip">
          <div>
            <FolderGit2 size={19} />
            <span>Repositories</span>
            <strong>{repos.data?.length ?? "—"}</strong>
          </div>
          <div>
            <LockKeyhole size={18} />
            <span>Private projects</span>
            <strong>
              {repos.data?.filter((r) => r.visibility === "private").length ??
                "—"}
            </strong>
          </div>
          <div>
            <Globe2 size={19} />
            <span>Public projects</span>
            <strong>
              {repos.data?.filter((r) => r.visibility === "public").length ??
                "—"}
            </strong>
          </div>
        </div>
      )}
      <div className={`dashboard-columns ${explore ? "single-column" : ""}`}>
        <section>
          <div className="section-heading">
            <h2>
              {explore ? "Repositories" : "Your repositories"}
              <span className="count">{repos.data?.length ?? 0}</span>
            </h2>
            <span className="muted small-text">
              A little progress, every day.
            </span>
          </div>
          <div className="filter-row">
            <div className="search-field">
              <Search size={16} />
              <input
                aria-label="Filter repositories"
                placeholder="Find a repository…"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
              />
            </div>
            <select
              aria-label="Repository visibility"
              value={visibility}
              onChange={(e) => setVisibility(e.target.value)}
            >
              <option value="all">Visibility</option>
              <option value="private">Private</option>
              <option value="public">Public</option>
            </select>
            <select
              aria-label="Sort repositories"
              value={sort}
              onChange={(e) => setSort(e.target.value)}
            >
              <option value="recent">Recent</option>
              <option value="name">Name</option>
            </select>
          </div>
          <ErrorMessage error={repos.error} />
          {repos.loading ? (
            <Loading />
          ) : visible.length ? (
            <div className="repo-list">
              {visible.map((repo) => (
                <Link href={repoPath(repo)} className="repo-card" key={repo.id}>
                  <div className="repo-card-icon">
                    <FolderGit2 size={22} />
                  </div>
                  <div className="repo-card-main">
                    <div className="repo-card-title">
                      <h3>
                        {explore && (
                          <span className="muted">{repo.owner} / </span>
                        )}
                        {repo.name}
                      </h3>
                      <Badge>
                        {repo.visibility === "private" ? (
                          <LockKeyhole size={10} />
                        ) : (
                          <Globe2 size={10} />
                        )}
                        {repo.visibility}
                      </Badge>
                    </div>
                    <p>
                      {repo.description || "A new idea, ready to take shape."}
                    </p>
                    <div className="repo-meta">
                      <span>
                        <GitBranch size={13} />
                        {repo.default_branch}
                      </span>
                      <span>Created {date(repo.created_at)}</span>
                    </div>
                  </div>
                  <ArrowUpRight className="card-arrow" size={18} />
                </Link>
              ))}
            </div>
          ) : (
            <div className="empty-state repo-empty">
              <span className="empty-icon">
                <FolderGit2 size={30} />
              </span>
              <h3>
                {filter || visibility !== "all"
                  ? "No matching repositories"
                  : "Your next project starts here"}
              </h3>
              <p>
                {filter
                  ? "Try a different search or visibility filter."
                  : "Give your idea a name. We’ll give your code a home."}
              </p>
              <Link className="button" href={user ? "/new" : "/register"}>
                <Plus size={16} /> Create a repository
              </Link>
            </div>
          )}
        </section>
        {!explore && (
          <aside className="right-rail">
            <div className="section-heading">
              <h2>Recent activity</h2>
              <span className="activity-dot" />
            </div>
            <ErrorMessage error={activity.error} />
            <div className="activity-list">
              {activity.data?.length ? (
                activity.data.slice(0, 6).map((event) => (
                  <div className="activity-item" key={event.id}>
                    <span className="activity-icon">
                      {event.action.startsWith("pull") ? (
                        <GitPullRequest size={14} />
                      ) : event.action.startsWith("token") ? (
                        <KeyRound size={14} />
                      ) : (
                        <GitCommitHorizontal size={14} />
                      )}
                    </span>
                    <div>
                      <p>
                        {event.action.replaceAll(".", " ").replaceAll("_", " ")}
                      </p>
                      <strong>{event.target}</strong>
                      <span>{date(event.created_at)}</span>
                    </div>
                  </div>
                ))
              ) : (
                <div className="quiet-activity">
                  <GitCommitHorizontal size={24} />
                  <p>A fresh start.</p>
                  <span>
                    Your commits and project milestones will appear here.
                  </span>
                </div>
              )}
            </div>
            <div className="quick-start">
              <Terminal size={19} />
              <h3>Your terminal. Your workflow.</h3>
              <p>
                Use the Git commands you already know. Connect with a personal
                access token.
              </p>
              <code>git push origin main</code>
              <Link href="/settings/tokens">
                Set up Git access <ArrowRight size={14} />
              </Link>
            </div>
          </aside>
        )}
      </div>
    </>
  );
}

function AuthPage({ mode }: { mode: string }) {
  const register = mode === "register";
  const { refresh, user, signup } = useSession();
  const router = useRouter();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  if (user)
    return (
      <div className="auth-card">
        <h1>You’re signed in.</h1>
        <p>Welcome back, {user.display_name}.</p>
        <Link href="/" className="button primary">
          Open workspace <ArrowRight size={16} />
        </Link>
      </div>
    );
  return (
    <div className="auth-card">
      <span className="auth-icon">
        <GitBranch size={29} />
      </span>
      <div className="eyebrow">YOUR CODE BELONGS HERE</div>
      <h1>{register ? "Build your own corner." : "Back to building."}</h1>
      <p>
        {register
          ? "Create your GITOWN account and start something great."
          : "Sign in to your workspace. Your next commit is waiting."}
      </p>
      <ErrorMessage error={error} />
      {register && !signup ? (
        <div className="info-box">
          Registration is disabled on this instance. Contact its owner for
          access.
        </div>
      ) : (
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setError("");
            const data = new FormData(e.currentTarget);
            try {
              await post(
                `/auth/${register ? "register" : "login"}`,
                register
                  ? {
                      username: data.get("username"),
                      display_name: data.get("display_name"),
                      email: data.get("email"),
                      password: data.get("password"),
                    }
                  : {
                      username: data.get("username"),
                      password: data.get("password"),
                    },
              );
              refresh();
              router.push("/");
            } catch (error) {
              setError((error as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          {register && (
            <label>
              Display name
              <input
                name="display_name"
                placeholder="What should we call you?"
                autoComplete="name"
                maxLength={80}
                required
              />
            </label>
          )}
          <label>
            {register ? "Username" : "Username or email"}
            <input
              name="username"
              autoComplete="username"
              placeholder={register ? "your-username" : "you@example.com"}
              pattern={register ? "[a-z0-9][a-z0-9-]{0,38}" : undefined}
              maxLength={register ? 39 : 254}
              required
            />
          </label>
          {register && (
            <label>
              Email address
              <input
                name="email"
                type="email"
                autoComplete="email"
                placeholder="you@example.com"
                maxLength={254}
                required
              />
            </label>
          )}
          <label>
            Password
            <input
              name="password"
              type="password"
              autoComplete={register ? "new-password" : "current-password"}
              minLength={register ? 12 : 1}
              maxLength={128}
              placeholder={
                register ? "At least 12 characters" : "Your password"
              }
              required
            />
          </label>
          <button className="button primary full-width" disabled={busy}>
            {busy ? "One moment…" : register ? "Create account" : "Sign in"}
            <ArrowRight size={16} />
          </button>
        </form>
      )}
      <p className="auth-switch">
        {register ? "Already have a home here?" : "New around here?"}{" "}
        <Link href={register ? "/login" : "/register"}>
          {register ? "Sign in" : "Create an account"}
        </Link>
      </p>
    </div>
  );
}

function SignInPrompt() {
  return (
    <div className="empty-state">
      <LockKeyhole size={30} />
      <h1>Your workspace is waiting.</h1>
      <p>Sign in to create repositories and manage your Git access.</p>
      <Link className="button primary" href="/login">
        Sign in <ArrowRight size={16} />
      </Link>
    </div>
  );
}

function NewRepository() {
  const { user } = useSession();
  const router = useRouter();
  const [name, setName] = useState("");
  const [visibility, setVisibility] = useState("private");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!user) return <SignInPrompt />;
  return (
    <div className="form-page">
      <div className="breadcrumb">
        <Link href="/">Workspace</Link>
        <ChevronRight size={12} />
        New repository
      </div>
      <span className="page-icon">
        <FolderGit2 size={26} />
      </span>
      <h1>A new place to build.</h1>
      <p className="page-description">
        A repository keeps your code, its history, and your ideas together.
      </p>
      <form
        className="panel form-panel"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          const data = new FormData(e.currentTarget);
          try {
            const repo = await post<Repo>("/repos", {
              name,
              description: data.get("description"),
              visibility,
              readme: data.get("readme") === "on",
            });
            router.push(repoPath(repo));
          } catch (error) {
            setError((error as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <ErrorMessage error={error} />
        <div className="owner-repo">
          <label>
            Owner
            <div className="owner-field">
              <Avatar name={user.username} small />
              {user.username}
            </div>
          </label>
          <span>/</span>
          <label>
            Repository name <span className="required">*</span>
            <input
              autoFocus
              name="name"
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase())}
              placeholder="my-next-big-idea"
              pattern="[a-z0-9][a-z0-9._-]{0,99}"
              maxLength={100}
              required
            />
          </label>
        </div>
        <p className="field-hint">
          Keep it memorable. Use letters, numbers, dots, hyphens, or
          underscores.
        </p>
        <label>
          Description <span className="optional">optional</span>
          <input
            name="description"
            placeholder="What are you building?"
            maxLength={500}
          />
        </label>
        <hr />
        <h3>Choose who can see it</h3>
        <div className="visibility-options">
          {["private", "public"].map((option) => (
            <label
              key={option}
              className={`radio-card ${visibility === option ? "chosen" : ""}`}
            >
              <input
                type="radio"
                name="visibility"
                value={option}
                checked={visibility === option}
                onChange={() => setVisibility(option)}
              />
              {option === "private" ? (
                <LockKeyhole size={22} />
              ) : (
                <Globe2 size={22} />
              )}
              <span>
                <strong>{option === "private" ? "Private" : "Public"}</strong>
                <small>
                  {option === "private"
                    ? "Only you can access this repository."
                    : "Anyone can see and clone this repository."}
                </small>
              </span>
            </label>
          ))}
        </div>
        <hr />
        <label className="checkbox-label">
          <input type="checkbox" name="readme" defaultChecked />
          <span>
            <strong>Start with a README</strong>
            <small>Introduce your project with its first commit.</small>
          </span>
        </label>
        <div className="form-actions">
          <Link className="button" href="/">
            Cancel
          </Link>
          <button className="button primary" disabled={busy}>
            <Plus size={16} />
            {busy ? "Creating repository…" : "Create repository"}
          </button>
        </div>
      </form>
    </div>
  );
}

function TokensPage() {
  const { user } = useSession();
  const [version, setVersion] = useState(0);
  const tokens = useData<Token[]>(user ? "/user/tokens" : null, version);
  const [secret, setSecret] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  if (!user) return <SignInPrompt />;
  return (
    <div className="form-page wide-form">
      <div className="breadcrumb">
        <span>Settings</span>
        <ChevronRight size={12} />
        Access tokens
      </div>
      <div className="page-heading">
        <div>
          <h1>Keys to your code.</h1>
          <p>Connect your terminal to GITOWN with personal access tokens.</p>
        </div>
        <KeyRound size={30} className="muted" />
      </div>
      <ErrorMessage error={error || tokens.error} />
      {secret && (
        <div className="secret-box">
          <div>
            <ShieldCheck size={18} />
            <strong>Your token is ready. Copy it now.</strong>
            <button
              className="icon-button"
              onClick={() => setSecret("")}
              aria-label="Dismiss token"
            >
              <X size={17} />
            </button>
          </div>
          <p>
            This is the only time you’ll see the full token. Store it somewhere
            safe.
          </p>
          <div className="copy-field">
            <code>{secret}</code>
            <CopyButton text={secret} />
          </div>
        </div>
      )}
      <form
        className="panel form-panel"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          const form = e.currentTarget;
          const data = new FormData(form);
          try {
            const result = await post<{ token: string }>("/user/tokens", {
              name: data.get("name"),
              scope: data.get("scope"),
            });
            setSecret(result.token);
            setVersion((v) => v + 1);
            form.reset();
          } catch (error) {
            setError((error as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <h2>Create a token</h2>
        <p className="muted">
          Tokens expire after 30 days. You can revoke them at any time.
        </p>
        <div className="two-fields">
          <label>
            Token name
            <input
              name="name"
              placeholder="MacBook terminal"
              maxLength={80}
              required
            />
          </label>
          <label>
            Repository access
            <select name="scope">
              <option value="repo:write">Read and write</option>
              <option value="repo:read">Read only</option>
            </select>
          </label>
        </div>
        <button className="button primary" disabled={busy}>
          <KeyRound size={15} />
          {busy ? "Creating…" : "Generate token"}
        </button>
      </form>
      <h2 className="section-title">Your tokens</h2>
      <div className="panel token-list">
        {tokens.loading ? (
          <Loading />
        ) : tokens.data?.length ? (
          tokens.data.map((token) => (
            <div className="token-row" key={token.id}>
              <KeyRound size={19} />
              <div>
                <strong>{token.name}</strong>
                <span>
                  {token.scope} · Expires {date(token.expires_at)}
                </span>
              </div>
              <button
                className="button danger small-button"
                disabled={busy}
                onClick={async () => {
                  if (
                    !window.confirm(
                      `Revoke “${token.name}”? Git clients using this token will lose access.`,
                    )
                  )
                    return;
                  setBusy(true);
                  setError("");
                  try {
                    await api(`/user/tokens/${token.id}`, { method: "DELETE" });
                    setSecret("");
                    setVersion((v) => v + 1);
                  } catch (error) {
                    setError((error as Error).message);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                Revoke
              </button>
            </div>
          ))
        ) : (
          <p className="muted padded">
            No access tokens yet. Create one to push your first commit.
          </p>
        )}
      </div>
      <div className="info-box">
        <Terminal size={20} />
        <div>
          <strong>Use your token as your Git password</strong>
          <p>
            When Git asks for credentials, enter <code>{user.username}</code> as
            the username and your token as the password. Your account password
            won’t work for Git operations.
          </p>
        </div>
      </div>
    </div>
  );
}

function SessionsPage() {
  const { user } = useSession();
  const [version, setVersion] = useState(0);
  const sessions = useData<BrowserSession[]>(
    user ? "/user/sessions" : null,
    version,
  );
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  if (!user) return <SignInPrompt />;
  return (
    <div className="form-page wide-form">
      <div className="breadcrumb">
        <span>Settings</span>
        <ChevronRight size={12} /> Signed-in devices
      </div>
      <div className="page-heading">
        <div>
          <h1>Your active sessions.</h1>
          <p>
            Review where your account is signed in and revoke devices you no
            longer use.
          </p>
        </div>
        <ShieldCheck size={30} className="muted" />
      </div>
      <ErrorMessage error={error || sessions.error} />
      <div className="panel token-list">
        {sessions.loading ? (
          <Loading />
        ) : sessions.data?.length ? (
          sessions.data.map((session) => (
            <div className="token-row" key={session.id}>
              <ShieldCheck size={19} />
              <div>
                <strong>
                  {session.current
                    ? "Current device"
                    : describeDevice(session.user_agent)}
                </strong>
                <span>
                  {session.ip_address || "Unknown address"} · Last active{" "}
                  {date(session.last_seen_at)} · Expires{" "}
                  {date(session.expires_at)}
                </span>
              </div>
              {session.current ? (
                <Badge kind="green">CURRENT</Badge>
              ) : (
                <button
                  className="button danger small-button"
                  disabled={busy === session.id}
                  onClick={async () => {
                    setBusy(session.id);
                    setError("");
                    try {
                      await api(`/user/sessions/${session.id}`, {
                        method: "DELETE",
                      });
                      setVersion((value) => value + 1);
                    } catch (error) {
                      setError((error as Error).message);
                    } finally {
                      setBusy("");
                    }
                  }}
                >
                  {busy === session.id ? "Revoking…" : "Revoke"}
                </button>
              )}
            </div>
          ))
        ) : (
          <p className="muted padded">No active sessions.</p>
        )}
      </div>
      <div className="info-box">
        <ShieldCheck size={20} />
        <div>
          <strong>See something unfamiliar?</strong>
          <p>
            Revoke that session immediately. You can also change your password
            in Account security; MFA is a later account-security milestone.
          </p>
        </div>
      </div>
    </div>
  );
}

function SecurityPage() {
  const { user } = useSession();
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  if (!user) return <SignInPrompt />;
  return (
    <div className="form-page wide-form">
      <div className="breadcrumb">
        <span>Settings</span>
        <ChevronRight size={12} /> Account security
      </div>
      <div className="page-heading">
        <div>
          <h1>Protect your account.</h1>
          <p>Change your password and close access you no longer trust.</p>
        </div>
        <LockKeyhole size={30} className="muted" />
      </div>
      <ErrorMessage error={error} />
      {notice && <div className="success-message">{notice}</div>}
      <form
        className="panel form-panel"
        onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          setNotice("");
          const form = event.currentTarget;
          const data = new FormData(form);
          if (data.get("new_password") !== data.get("confirm_password")) {
            setError("The new passwords do not match.");
            setBusy(false);
            return;
          }
          try {
            await api("/user/password", {
              method: "PATCH",
              body: JSON.stringify({
                current_password: data.get("current_password"),
                new_password: data.get("new_password"),
                revoke_access_tokens: data.get("revoke_access_tokens") === "on",
              }),
            });
            form.reset();
            setNotice(
              "Password changed. Other browser sessions were signed out.",
            );
          } catch (changeError) {
            setError((changeError as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <h2>Change password</h2>
        <label>
          Current password
          <input
            name="current_password"
            type="password"
            autoComplete="current-password"
            maxLength={128}
            required
          />
        </label>
        <div className="two-fields">
          <label>
            New password
            <input
              name="new_password"
              type="password"
              autoComplete="new-password"
              minLength={12}
              maxLength={128}
              required
            />
          </label>
          <label>
            Confirm new password
            <input
              name="confirm_password"
              type="password"
              autoComplete="new-password"
              minLength={12}
              maxLength={128}
              required
            />
          </label>
        </div>
        <label className="checkbox-row">
          <input name="revoke_access_tokens" type="checkbox" defaultChecked />
          <span>
            <strong>Revoke every personal access token</strong>
            <small>Git clients will need a newly generated token.</small>
          </span>
        </label>
        <button className="button primary" disabled={busy}>
          <LockKeyhole size={15} />
          {busy ? "Changing password…" : "Change password"}
        </button>
      </form>
    </div>
  );
}

function describeDevice(userAgent: string) {
  if (!userAgent) return "Unknown device";
  if (userAgent.includes("Firefox")) return "Firefox browser";
  if (userAgent.includes("Edg/")) return "Edge browser";
  if (userAgent.includes("Chrome")) return "Chrome browser";
  if (userAgent.includes("Safari")) return "Safari browser";
  return "Browser session";
}
