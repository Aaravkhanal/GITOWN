export type User = { id: string; username: string; display_name: string };
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
export type IssueComment = {
  id: string;
  body: string;
  author: string;
  created_at: string;
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
};
export type PullComment = {
  id: string;
  body: string;
  author: string;
  created_at: string;
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
