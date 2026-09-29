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
  created_at: string;
  followers: number;
  following: number;
  followed: boolean;
  showcase: PublicRepository[];
  repositories: PublicRepository[];
};
export type Repo = {
  id: string;
  owner: string;
  name: string;
  description: string;
  visibility: "public" | "private";
  default_branch: string;
  created_at: string;
  can_write: boolean;
  can_triage: boolean;
  can_manage: boolean;
  can_comment: boolean;
  role?: "owner" | "maintain" | "write" | "triage" | "read";
  archived: boolean;
  clone_url: string;
};
export type SparkState = { count: number; sparked: boolean };
export type TopicState = { topics: string[] };
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
};
export type BoardItem = {
  issue_id: string;
  number: number;
  title: string;
  state: "open" | "closed";
  status: "todo" | "progress" | "done";
  author: string;
};
export type Notification = {
  id: number;
  kind: "issue_comment" | "issue_closed" | "issue_reopened";
  actor: string;
  owner: string;
  repository: string;
  issue: number;
  title: string;
  created_at: string;
  read_at: string | null;
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
};
export type PullDetail = {
  pull: Pull;
  head_sha: string;
  base_sha: string;
  diff: string;
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
  updated_at: string;
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
