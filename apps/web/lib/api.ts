export type User = { id: string; username: string; display_name: string };
export type PublicRepository = {
  id: string;
  name: string;
  description: string;
  archived: boolean;
  created_at: string;
  homepage: string;
  stack: string;
  sparks: number;
  latest_drop: string;
  downloads: number;
};
export type ProfileLink = { label: string; url: string };
export type ContributionBadge = {
  id: string;
  label: string;
  tier: "bronze" | "silver" | "gold";
  reason: string;
};
export type Profile = {
  username: string;
  display_name: string;
  bio: string;
  website: string;
  location: string;
  skills: string;
  skill_tags: string[];
  availability: string;
  open_to_collaborators: boolean;
  links: ProfileLink[];
  created_at: string;
  followers: number;
  following: number;
  followed: boolean;
  contributions: {
    merged_unites: number;
    approvals: number;
    reviews: number;
    helped_ship: number;
    closed_issues: number;
    resolved_issues: number;
    pushes: number;
    docs_unites: number;
    external_merges: number;
    public_repositories: number;
  };
  badges: ContributionBadge[];
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
  can_maintain?: boolean;
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
  last_maintained_at?: string;
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
    skills: string;
    availability: string;
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
export type DeployKey = {
  id: string;
  title: string;
  fingerprint: string;
  write: boolean;
  created_at: string;
};
export type RefEvent = {
  ref: string;
  old_sha: string;
  new_sha: string;
  via: string;
  actor: string;
  created_at: string;
};
export type WebhookEvent =
  | "push"
  | "issue.opened"
  | "issue.closed"
  | "issue.reopened"
  | "issue.commented"
  | "pull.opened"
  | "pull.closed"
  | "pull.reopened"
  | "pull.merged"
  | "pull.reviewed"
  | "pull.commented"
  | "drop.published";
export type Webhook = {
  id: string;
  url: string;
  events: WebhookEvent[];
  active: boolean;
  kind: "generic" | "slack" | "discord";
  created_at: string;
  secret?: string;
};
export type WebhookDelivery = {
  id: string;
  event: string;
  status: "pending" | "sending" | "success" | "failed";
  attempts: number;
  response_status: number | null;
  response_body: string | null;
  last_error: string | null;
  created_at: string;
  delivered_at: string | null;
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
  state_reason?: "" | "completed" | "not_planned" | "duplicate";
  updated_at?: string;
  duplicate_of?: number | null;
  parent?: number | null;
  milestone?: string | null;
  labels?: { name: string; color: string }[];
  assignees?: string[];
  comments?: number;
  sub_issues?: { total: number; closed: number };
};
export type IssueFormField = {
  id: string;
  label: string;
  type: "text" | "textarea" | "dropdown" | "checkbox";
  required?: boolean;
  options?: string[];
};
export type IssueTemplate = {
  name: string;
  title: string;
  body: string;
  kind?: "bug" | "feature" | "custom";
  fields?: IssueFormField[];
  builtin?: boolean;
};
export type SubIssues = {
  parent: LinkedIssue | null;
  children: LinkedIssue[];
  total: number;
  closed: number;
};
export type IssueReference = {
  kind: "issue" | "pull";
  owner: string;
  repository: string;
  number: number;
  title: string;
  state: string;
};
export type IssueReferences = {
  mentions: IssueReference[];
  referenced_by: IssueReference[];
};
export type SavedSearch = {
  id: string;
  name: string;
  query: string;
  created_at: string;
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
  kind: "issue" | "pull";
  item_id: string;
  issue_id?: string;
  pull_id?: string;
  owner: string;
  repository: string;
  number: number;
  title: string;
  state: string;
  status: string;
  author: string;
  priority: string;
  iteration: string;
  pinned: boolean;
  estimate: number | null;
  due_date: string | null;
  milestone: string | null;
  milestone_due: string | null;
  fields: Record<string, string>;
};
export type BoardColumn = { key: string; name: string };
export type BoardField = {
  id: string;
  name: string;
  kind: "text" | "number" | "date" | "single_select";
  options: string[];
  position: number;
};
export type BoardSettings = {
  columns: BoardColumn[];
  automation: boolean;
  fields: BoardField[];
};
export type Notification = {
  id: number;
  kind:
    | "issue_opened"
    | "issue_comment"
    | "issue_closed"
    | "issue_reopened"
    | "pull_opened"
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
    | "follow"
    | string;
  actor: string;
  owner: string;
  repository: string;
  issue: number | null;
  pull: number | null;
  title: string;
  excerpt: string;
  created_at: string;
  read_at: string | null;
};
export type FollowEntry = {
  username: string;
  display_name: string;
  bio: string;
};
export type Screenshot = { url: string; caption: string };
export type RepositoryShowcase = {
  repository: Repo;
  readme: string;
  setup: string;
  screenshots: Screenshot[];
  stack: string[];
  sparks: number;
  contributors: {
    username: string;
    display_name: string;
    merged: number;
    role: "owner" | "contributor";
  }[];
  latest_drop: {
    tag: string;
    title: string;
    downloads: number;
    created_at: string;
  } | null;
  roadmap: {
    title: string;
    due_date: string | null;
    open_issues: number;
    closed_issues: number;
  }[];
  tasks: { number: number; title: string; labels: string[] }[];
};
export type RepositoryWatch = {
  mode: "watching" | "participating" | "ignoring" | "";
  implicit: boolean;
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
  updated_at?: string | null;
  edited?: boolean;
  editable?: boolean;
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
  branch_deleted?: boolean;
  branch_delete_error?: string;
  head_owner?: string;
  head_repository?: string;
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
  dismissed?: boolean;
  dismissal_reason?: string;
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
  required_reviewers: string[];
  allow_force_push: boolean;
  allow_deletion: boolean;
  updated_at: string;
};
export type RepositoryPermissions = {
  role: string;
  read: boolean;
  triage: boolean;
  write: boolean;
  maintain: boolean;
  manage: boolean;
};
export type CommitStatus = {
  context: string;
  state: "pending" | "success" | "failure" | "error";
  description: string;
  target_url: string;
  reporter: string;
  updated_at: string;
};
export type TimelineItem = {
  kind: string;
  actor: string;
  body: string;
  created_at: string;
};
export type ThreadReply = {
  id: string;
  author: string;
  body: string;
  created_at: string;
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
  accept_url?: string;
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
export type OAuthApp = {
  id: string;
  name: string;
  description: string;
  homepage_url: string;
  redirect_uri: string;
  client_id: string;
  client_secret?: string;
  created_at: string;
  authorized_users: number;
};
export type AuthorizedApp = {
  oauth_app_id: string;
  name: string;
  homepage_url: string;
  scope: string;
  created_at: string;
};
export type GitownApp = {
  id: string;
  name: string;
  description: string;
  homepage_url: string;
  webhook_url: string;
  requested_scope: string;
  bot_username: string;
  created_at: string;
  installations: number;
};
export type GitownAppInstallation = {
  id: string;
  gitown_app_id: string;
  name: string;
  description: string;
  granted_scope: string;
  created_at: string;
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
/** Fetches a list endpoint and reads its pagination headers. */
export async function apiPage<T>(
  path: string,
): Promise<{ items: T[]; total: number; counts: Record<string, number> }> {
  const response = await fetch(`/api/v1${path}`, {
    credentials: "same-origin",
    cache: "no-store",
  });
  const data = await response.json().catch(() => null);
  if (!response.ok)
    throw new Error(
      data?.error?.message || `Request failed (${response.status}).`,
    );
  const count = (name: string) => Number(response.headers.get(name) || 0);
  return {
    items: data || [],
    total: count("X-Total-Count"),
    counts: { open: count("X-Open-Count"), closed: count("X-Closed-Count") },
  };
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
