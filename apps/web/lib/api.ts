export type User = { id: string; username: string; display_name: string };
export type PublicRepository = {
  id: string;
  name: string;
  description: string;
  archived: boolean;
  created_at: string;
};
export type Profile = {
  username: string;
  display_name: string;
  bio: string;
  website: string;
  location: string;
  skills: string;
  availability: string;
  open_to_collaborators: boolean;
  created_at: string;
  followers: number;
  following: number;
  followed: boolean;
  contributions: {
    merged_unites: number;
    approvals: number;
    closed_issues: number;
    public_repositories: number;
  };
  badges: string[];
  showcase: PublicRepository[];
  repositories: PublicRepository[];
};
export type Repo = {
  id: string;
  owner: string;
  name: string;
  description: string;
  visibility: "public" | "private" | "internal";
  default_branch: string;
  created_at: string;
  can_write: boolean;
  can_triage: boolean;
  can_manage: boolean;
  can_comment: boolean;
  role?: "owner" | "maintain" | "write" | "triage" | "read";
  archived: boolean;
  clone_url: string;
  homepage?: string;
  stack?: string;
  language?: string;
  pushed_at?: string;
  size_bytes?: number;
  district?: string;
  ssh_clone_url?: string;
};
export type SparkState = { count: number; sparked: boolean };
export type TopicState = { topics: string[] };
export type RepositorySearch = { items: Repo[]; has_more: boolean };
export type BuilderSearch = {
  items: {
    username: string;
    display_name: string;
    bio: string;
    location: string;
    followers: number;
    repositories: number;
    open_to_collaborators: boolean;
  }[];
  has_more: boolean;
};
export type DeletedRepository = {
  id: string;
  owner: string;
  name: string;
  deleted_at: string;
  purge_after: string;
};
export type RepositoryMember = {
  username: string;
  display_name: string;
  role: "maintain" | "write" | "triage" | "read";
  created_at: string;
};
export type Commit = {
  sha: string;
  message: string;
  author: string;
  date: string;
};
export type Tree = {
  branch: string;
  path: string;
  sha: string;
  entries: { name: string; type: string; sha: string }[];
  content?: string;
  binary: boolean;
};
export type Issue = {
  id: string;
  number: number;
  title: string;
  body: string;
  state: string;
  author: string;
  created_at: string;
  pinned?: boolean;
  priority?: string;
  iteration?: string;
  estimate?: number | null;
  due_date?: string | null;
};
export type IssueTemplate = {
  name: string;
  title: string;
  body: string;
  kind?: "bug" | "feature" | "custom";
};
export type LinkedIssue = {
  number: number;
  title: string;
  state: "open" | "closed";
};
export type IssueDependencies = {
  blocked_by: LinkedIssue[];
  blocks: LinkedIssue[];
};
export type BoardItem = {
  issue_id: string;
  number: number;
  title: string;
  state: "open" | "closed";
  status: "todo" | "progress" | "done";
  author: string;
  priority: string;
  iteration: string;
  pinned: boolean;
  estimate: number | null;
  due_date: string | null;
};
export type Notification = {
  id: number;
  kind:
    | "issue_comment"
    | "issue_closed"
    | "issue_reopened"
    | "pull_comment"
    | "pull_review"
    | "pull_closed"
    | "pull_reopened"
    | "pull_merged"
    | "mention"
    | "assignment"
    | "review_request"
    | "invitation"
    | "ownership_transfer"
    | "check_success"
    | "check_failure"
    | string;
  actor: string;
  owner: string;
  repository: string;
  issue: number | null;
  pull: number | null;
  title: string;
  created_at: string;
  read_at: string | null;
};
export type FeedEvent = {
  kind:
    | "repository_created"
    | "repository_sparked"
    | "issue_opened"
    | "unite_opened";
  actor: string;
  owner: string;
  repository: string;
  number: number;
  title: string;
  created_at: string;
};
export type IssueComment = {
  id: string;
  body: string;
  author: string;
  created_at: string;
};
export type IssueAssignees = {
  assigned: Pick<User, "username" | "display_name">[];
  available: Pick<User, "username" | "display_name">[];
};
export type Milestone = {
  id: string;
  title: string;
  description: string;
  state: "open" | "closed";
  due_date: string | null;
  open_issues: number;
  closed_issues: number;
  created_at: string;
  updated_at: string;
};
export type Label = {
  id: string;
  name: string;
  color: string;
  description: string;
  created_at: string;
};
export type Pull = Issue & {
  base_branch: string;
  head_branch: string;
  merge_sha?: string;
  draft?: boolean;
  merge_method?: string;
};
export type PullDetail = {
  pull: Pull;
  head_sha: string;
  base_sha: string;
  diff: string;
  diff_truncated?: boolean;
  diff_error: string;
  mergeable: boolean;
  can_review: boolean;
};
export type PullComment = {
  id: string;
  body: string;
  author: string;
  created_at: string;
};
export type PullReview = {
  id: string;
  state: "approved" | "changes_requested" | "commented";
  body: string;
  reviewer: string;
  head_sha: string;
  stale: boolean;
  created_at: string;
};
export type BranchRule = {
  branch: string;
  required_approvals: number;
  block_changes_requested: boolean;
  require_unite: boolean;
  require_resolved: boolean;
  require_up_to_date: boolean;
  restrict_push: boolean;
  require_signed: boolean;
  require_maintainer_approval: boolean;
  required_checks: string[];
  updated_at: string;
};
export type Invitation = {
  id: string;
  owner: string;
  repository: string;
  email: string;
  username: string;
  role: string;
  status: string;
  expires_at: string;
  created_at: string;
};
export type WorkSearch = {
  items: {
    kind: string;
    owner: string;
    repository: string;
    number: number;
    title: string;
    preview: string;
    state: string;
  }[];
  has_more: boolean;
};
export type Activity = {
  id: number;
  action: string;
  target: string;
  created_at: string;
};
export type Token = {
  id: string;
  name: string;
  scope: string;
  created_at: string;
  expires_at: string;
};
export type BrowserSession = {
  id: string;
  ip_address: string;
  user_agent: string;
  current: boolean;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
};

export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    ...options,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...options.headers },
    cache: "no-store",
  });
  const data = await response.json().catch(() => null);
  if (!response.ok)
    throw new Error(
      data?.error?.message || `Request failed (${response.status}).`,
    );
  return data;
}
export function post<T>(path: string, body: unknown) {
  return api<T>(path, { method: "POST", body: JSON.stringify(body) });
}
export function patch<T>(path: string, body: unknown) {
  return api<T>(path, { method: "PATCH", body: JSON.stringify(body) });
}
export function put<T>(path: string, body: unknown) {
  return api<T>(path, { method: "PUT", body: JSON.stringify(body) });
}
export function remove<T>(path: string) {
  return api<T>(path, { method: "DELETE" });
}
export function destroy<T>(path: string, body: unknown) {
  return api<T>(path, { method: "DELETE", body: JSON.stringify(body) });
}
export function date(value: string) {
  return new Date(value).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
}
export function repoPath(repo: Repo) {
  return `/repos/${repo.owner}/${repo.name}`;
}
