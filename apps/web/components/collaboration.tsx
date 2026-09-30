"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import {
  date,
  patch,
  post,
  put,
  remove,
  type CommitStatus,
  type Invitation,
  type Issue,
  type IssueAssignees,
  type Label,
  type Repo,
  type RepositoryPermissions,
  type ThreadReply,
  type TimelineItem,
} from "@/lib/api";
import { Badge, CopyButton, ErrorMessage, Loading, useData } from "./ui";

export function IssuePlanning({
  endpoint,
  issue,
  canTriage,
}: {
  endpoint: string;
  issue: Issue;
  canTriage: boolean;
}) {
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  if (!canTriage) return null;
  return (
    <form
      className="issue-labels"
      onSubmit={async (event) => {
        event.preventDefault();
        setBusy(true);
        setError("");
        setNotice("");
        const data = new FormData(event.currentTarget);
        try {
          const estimate = String(data.get("estimate") || "");
          await put(`${endpoint}/issues/${issue.number}/planning`, {
            pinned: data.get("pinned") === "on",
            priority: data.get("priority"),
            ...(estimate ? { estimate: Number(estimate) } : {}),
            due_date: data.get("due_date") || "",
            iteration: data.get("iteration") || "",
            ...(data.get("duplicate_of")
              ? { duplicate_of: Number(data.get("duplicate_of")) }
              : {}),
          });
          setNotice("Planning saved.");
        } catch (saveError) {
          setError((saveError as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <h4>Planning</h4>
      <ErrorMessage error={error} />
      {notice && <p className="green-text">{notice}</p>}
      <label className="checkbox-row">
        <input name="pinned" type="checkbox" defaultChecked={issue.pinned} /> Pin
        this issue
      </label>
      <label>
        Priority
        <select name="priority" defaultValue={issue.priority || "none"}>
          {["none", "low", "medium", "high", "urgent"].map((priority) => (
            <option key={priority} value={priority}>
              {priority}
            </option>
          ))}
        </select>
      </label>
      <label>
        Estimate
        <input
          name="estimate"
          type="number"
          min="0"
          max="100"
          defaultValue={issue.estimate ?? ""}
        />
      </label>
      <label>
        Due date
        <input
          name="due_date"
          type="date"
          defaultValue={issue.due_date?.slice(0, 10) || ""}
        />
      </label>
      <label>
        Iteration
        <input
          name="iteration"
          maxLength={40}
          defaultValue={issue.iteration || ""}
        />
      </label>
      <label>
        Duplicate of issue number
        <input name="duplicate_of" type="number" min="1" />
      </label>
      <button className="button small-button" disabled={busy}>
        {busy ? "Saving…" : "Save planning"}
      </button>
    </form>
  );
}

export function CommentEdit({
  path,
  initial,
}: {
  path: string;
  initial: string;
}) {
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState(initial);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  if (!open) {
    return (
      <button className="text-button" type="button" onClick={() => setOpen(true)}>
        Edit
      </button>
    );
  }
  return (
    <form
      onSubmit={async (event) => {
        event.preventDefault();
        setBusy(true);
        setError("");
        try {
          await patch(path, { body });
          setOpen(false);
        } catch (saveError) {
          setError((saveError as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <ErrorMessage error={error} />
      <textarea
        value={body}
        onChange={(event) => setBody(event.target.value)}
        maxLength={10000}
        rows={3}
        required
      />
      <button className="button small-button" disabled={busy}>
        {busy ? "Saving…" : "Save edit"}
      </button>
    </form>
  );
}

type Thread = {
  id: string;
  path: string;
  side: string;
  line: number;
  commit_sha: string;
  body: string;
  author: string;
  resolved: boolean;
  outdated: boolean;
  reply_count: number;
};

export type LineDraft = { path: string; side: "left" | "right"; line: number };

export function UnitePanel({
  endpoint,
  number,
  canTriage,
  canComment,
  headSHA,
  viewer,
  district,
  lineDraft,
}: {
  endpoint: string;
  number: string;
  canTriage: boolean;
  canComment: boolean;
  headSHA: string;
  viewer?: string;
  district?: string;
  lineDraft?: LineDraft | null;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const base = `${endpoint}/pulls/${number}`;
  const threads = useData<Thread[]>(`${base}/threads`, version);
  const reviewers = useData<{ username: string; display_name: string }[]>(
    `${base}/reviewers`,
    version,
  );
  const assignees = useData<IssueAssignees>(`${base}/assignees`, version);
  const labels = useData<Label[]>(`${base}/labels`, version);
  const catalog = useData<Label[]>(`${endpoint}/labels`, version);
  const refresh = () => setVersion((value) => value + 1);
  return (
    <section className="panel" aria-label="Unite collaboration">
      <h3>Review conversations</h3>
      <ErrorMessage error={error || threads.error} />
      {threads.loading ? (
        <Loading />
      ) : (
        threads.data?.map((thread) => (
          <ThreadCard
            key={thread.id}
            base={base}
            thread={thread}
            canResolve={canTriage || thread.author === viewer}
            canComment={canComment}
            version={version}
            onChange={refresh}
            onError={setError}
          />
        ))
      )}
      {canComment && headSHA && (
        <form
          className="comment-form"
          id="line-comment-form"
          key={lineDraft ? `${lineDraft.path}:${lineDraft.side}:${lineDraft.line}` : "blank"}
          onSubmit={async (event) => {
            event.preventDefault();
            const form = event.currentTarget;
            const data = new FormData(form);
            setError("");
            try {
              await post(`${base}/threads`, {
                commit_sha: headSHA,
                path: data.get("path"),
                side: data.get("side"),
                line: Number(data.get("line")),
                body: data.get("body"),
              });
              form.reset();
              refresh();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <h4>Comment on a line</h4>
          <p className="muted small-text">
            Choose a line in the changes below, or enter a file path and line
            number that appears in the diff.
          </p>
          <label>
            File path
            <input name="path" defaultValue={lineDraft?.path || ""} required />
          </label>
          <label>
            Side
            <select name="side" defaultValue={lineDraft?.side || "right"}>
              <option value="right">New file (right)</option>
              <option value="left">Old file (left)</option>
            </select>
          </label>
          <label>
            Line
            <input
              name="line"
              type="number"
              min="1"
              defaultValue={lineDraft?.line || ""}
              required
            />
          </label>
          <label>
            Line comment
            <textarea
              name="body"
              maxLength={10000}
              rows={3}
              required
              autoFocus={!!lineDraft}
            />
          </label>
          <button className="button small-button">Add line comment</button>
        </form>
      )}
      <PullCrews
        base={base}
        district={district}
        canTriage={canTriage}
        version={version}
        onChange={refresh}
        onError={setError}
      />
      <h4>Requested reviewers</h4>
      <p className="muted small-text">
        {(reviewers.data || []).map((person) => person.username).join(", ") ||
          "No reviewers requested."}
      </p>
      {canTriage && (
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            const names = String(data.get("usernames") || "")
              .split(",")
              .map((name) => name.trim())
              .filter(Boolean);
            setError("");
            try {
              await put(`${base}/reviewers`, { usernames: names });
              refresh();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <input name="usernames" placeholder="username, username" />
          <button className="button small-button">Request review</button>
        </form>
      )}
      <h4>Assignees</h4>
      <p className="muted small-text">
        {(assignees.data?.assigned || []).map((person) => person.username).join(", ") ||
          "Unassigned."}
      </p>
      {canTriage && assignees.data && (
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            setError("");
            try {
              await put(`${base}/assignees`, {
                usernames: data.getAll("username"),
              });
              refresh();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          {[...assignees.data.assigned, ...assignees.data.available].map(
            (person) => (
              <label key={person.username} className="checkbox-row">
                <input
                  type="checkbox"
                  name="username"
                  value={person.username}
                  defaultChecked={assignees.data?.assigned.some(
                    (assigned) => assigned.username === person.username,
                  )}
                />
                {person.username}
              </label>
            ),
          )}
          <button className="button small-button">Save assignees</button>
        </form>
      )}
      <h4>Labels</h4>
      <p>
        {(labels.data || []).map((label) => (
          <Badge key={label.id}>{label.name}</Badge>
        ))}
      </p>
      {canTriage && catalog.data && (
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            setError("");
            try {
              await put(`${base}/labels`, { label_ids: data.getAll("label") });
              refresh();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          {catalog.data.map((label) => (
            <label key={label.id} className="checkbox-row">
              <input
                type="checkbox"
                name="label"
                value={label.id}
                defaultChecked={labels.data?.some((item) => item.id === label.id)}
              />
              {label.name}
            </label>
          ))}
          <button className="button small-button">Save labels</button>
        </form>
      )}
      {canTriage && (
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            const raw = String(data.get("links") || "");
            const links = raw
              .split(",")
              .map((part) => part.trim())
              .filter(Boolean)
              .map((part) => ({
                number: Number(part.replace("#", "")),
                closes: part.endsWith("!"),
              }));
            setError("");
            try {
              await put(`${base}/links`, { links });
              refresh();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <label>
            Linked issues
            <input name="links" placeholder="12, 15!" />
          </label>
          <p className="muted small-text">
            Add ! after a number to close that issue when this Unite request
            merges.
          </p>
          <button className="button small-button">Save links</button>
        </form>
      )}
    </section>
  );
}

function ThreadCard({
  base,
  thread,
  canResolve,
  canComment,
  version,
  onChange,
  onError,
}: {
  base: string;
  thread: Thread;
  canResolve: boolean;
  canComment: boolean;
  version: number;
  onChange: () => void;
  onError: (message: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const replies = useData<ThreadReply[]>(
    open ? `${base}/threads/${thread.id}/replies` : null,
    version,
  );
  return (
    <article className="board-card review-thread">
      <strong>
        {thread.path}:{thread.line} ({thread.side === "left" ? "old" : "new"})
      </strong>
      <p className="muted small-text">
        @{thread.author}
        {thread.outdated ? " · outdated" : ""}
        {thread.resolved ? " · resolved" : ""}
      </p>
      <p>{thread.body}</p>
      {thread.reply_count > 0 && (
        <button
          className="text-button"
          type="button"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          {open ? "Hide" : "Show"} {thread.reply_count}{" "}
          {thread.reply_count === 1 ? "reply" : "replies"}
        </button>
      )}
      {open &&
        (replies.loading ? (
          <Loading />
        ) : (
          <div className="thread-replies">
            <ErrorMessage error={replies.error} />
            {replies.data?.map((reply) => (
              <p key={reply.id}>
                <strong>@{reply.author}</strong> {reply.body}
              </p>
            ))}
          </div>
        ))}
      {canResolve && (
        <button
          className="button small-button"
          type="button"
          onClick={async () => {
            onError("");
            try {
              await post(`${base}/threads/${thread.id}/resolve`, {
                resolved: !thread.resolved,
              });
              onChange();
            } catch (saveError) {
              onError((saveError as Error).message);
            }
          }}
        >
          {thread.resolved ? "Reopen conversation" : "Resolve conversation"}
        </button>
      )}
      {canComment && (
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            const form = event.currentTarget;
            const data = new FormData(form);
            onError("");
            try {
              await post(`${base}/threads/${thread.id}/replies`, {
                body: data.get("body"),
              });
              form.reset();
              setOpen(true);
              onChange();
            } catch (saveError) {
              onError((saveError as Error).message);
            }
          }}
        >
          <input
            name="body"
            maxLength={10000}
            placeholder="Reply"
            aria-label={`Reply to ${thread.path} line ${thread.line}`}
            required
          />
          <button className="button small-button">Reply</button>
        </form>
      )}
    </article>
  );
}

function PullCrews({
  base,
  district,
  canTriage,
  version,
  onChange,
  onError,
}: {
  base: string;
  district?: string;
  canTriage: boolean;
  version: number;
  onChange: () => void;
  onError: (message: string) => void;
}) {
  const crews = useData<{
    available: { slug: string; name: string }[];
    requested: string[];
  }>(district ? `${base}/crews` : null, version);
  if (!district || !crews.data) return null;
  return (
    <>
      <h4>Crew reviews</h4>
      <p className="muted small-text">
        {crews.data.requested.join(", ") || "No crews requested."}
      </p>
      {canTriage && crews.data.available.length > 0 && (
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            onError("");
            try {
              await put(`${base}/crews`, { slugs: data.getAll("crew") });
              onChange();
            } catch (saveError) {
              onError((saveError as Error).message);
            }
          }}
        >
          {crews.data.available.map((crew) => (
            <label key={crew.slug} className="checkbox-row">
              <input
                type="checkbox"
                name="crew"
                value={crew.slug}
                defaultChecked={crews.data?.requested.includes(crew.slug)}
              />
              {crew.name} ({district}/{crew.slug})
            </label>
          ))}
          <button className="button small-button">Request crew review</button>
        </form>
      )}
    </>
  );
}

const statusKinds: Record<string, string> = {
  success: "green",
  failure: "red",
  error: "red",
  pending: "",
};

export function PullChecks({
  endpoint,
  headSHA,
}: {
  endpoint: string;
  headSHA: string;
}) {
  const statuses = useData<CommitStatus[]>(
    headSHA ? `${endpoint}/commits/${headSHA}/statuses` : null,
  );
  if (!headSHA || !statuses.data?.length) return null;
  return (
    <section className="panel" aria-label="Checks">
      <h3>Checks on {headSHA.slice(0, 7)}</h3>
      {statuses.data.map((status) => (
        <p key={status.context}>
          <Badge kind={statusKinds[status.state]}>{status.state}</Badge>{" "}
          <strong>{status.context}</strong>
          {status.description && ` · ${status.description}`}
          {status.target_url && (
            <>
              {" · "}
              <a href={status.target_url} rel="noreferrer nofollow" target="_blank">
                Details
              </a>
            </>
          )}
        </p>
      ))}
    </section>
  );
}

function timelineText(item: TimelineItem) {
  const kind = item.kind;
  if (kind === "comment") return "commented";
  if (kind === "inline") return "commented on a line";
  if (kind === "reply") return "replied on a line";
  if (kind.startsWith("review.")) return kind.slice(7).replace("_", " ");
  if (kind.startsWith("check.")) return `check ${kind.slice(6)}`;
  if (kind === "push") return "pushed";
  if (kind === "review_dismissed") return "dismissed a review";
  if (kind === "draft") return item.body === "true" ? "marked as draft" : "marked ready";
  if (kind === "resolved" || kind === "unresolved") return `${kind} a conversation`;
  return kind;
}

export function PullTimeline({ base }: { base: string }) {
  const [open, setOpen] = useState(false);
  const [offset, setOffset] = useState(0);
  const timeline = useData<{ items: TimelineItem[]; has_more: boolean }>(
    open ? `${base}/timeline?offset=${offset}` : null,
  );
  return (
    <section className="panel" aria-label="Unite request timeline">
      <div className="review-heading">
        <h3>Timeline</h3>
        <button
          className="text-button"
          type="button"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          {open ? "Hide timeline" : "Show timeline"}
        </button>
      </div>
      {open &&
        (timeline.loading ? (
          <Loading />
        ) : (
          <>
            <ErrorMessage error={timeline.error} />
            <ol className="timeline-list">
              {timeline.data?.items.map((item, index) => (
                <li key={`${offset}-${index}`}>
                  <span className="muted small-text">{date(item.created_at)}</span>{" "}
                  <strong>{item.actor ? `@${item.actor}` : "GITOWN"}</strong>{" "}
                  {timelineText(item)}
                  {item.kind === "push" ? (
                    <code> {item.body.split("..").map((sha) => sha.slice(0, 7)).join("..")}</code>
                  ) : item.kind !== "draft" && item.kind !== "resolved" && item.kind !== "unresolved" && item.body ? (
                    <span className="muted"> · {item.body.slice(0, 140)}</span>
                  ) : null}
                </li>
              ))}
            </ol>
            <div className="form-actions">
              {offset > 0 && (
                <button className="button small-button" type="button" onClick={() => setOffset(Math.max(0, offset - 50))}>
                  Earlier
                </button>
              )}
              {timeline.data?.has_more && (
                <button className="button small-button" type="button" onClick={() => setOffset(offset + 50)}>
                  Later
                </button>
              )}
            </div>
          </>
        ))}
    </section>
  );
}

export function RepositoryPermissionSummary({ endpoint }: { endpoint: string }) {
  const permissions = useData<RepositoryPermissions>(`${endpoint}/permissions`);
  if (!permissions.data) return <ErrorMessage error={permissions.error} />;
  const rows: [keyof RepositoryPermissions, string][] = [
    ["read", "View, clone, and comment"],
    ["triage", "Manage issues, labels, assignees, and review requests"],
    ["write", "Push branches and merge unite requests"],
    ["maintain", "Edit branch rules, dismiss reviews, and push to restricted branches"],
    ["manage", "Control access, visibility, transfer, and deletion"],
  ];
  return (
    <section className="panel" aria-label="Your access">
      <h2>Your access: {permissions.data.role || "read"}</h2>
      <ul className="permission-list">
        {rows.map(([key, label]) => (
          <li key={key}>
            {permissions.data?.[key] ? "✓" : "—"} {label}
          </li>
        ))}
      </ul>
    </section>
  );
}

export function OwnerDelivery({
  endpoint,
  homepage,
  stack,
}: {
  endpoint: string;
  homepage?: string;
  stack?: string;
}) {
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  return (
    <section className="panel settings-form">
      <h2>Project presentation</h2>
      <ErrorMessage error={error} />
      {notice && <p className="green-text">{notice}</p>}
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setError("");
          setNotice("");
          try {
            await put(`${endpoint}/presentation`, {
              homepage: data.get("homepage"),
              stack: data.get("stack"),
            });
            setNotice("Presentation saved.");
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        <label>
          Homepage
          <input
            name="homepage"
            defaultValue={homepage || ""}
            placeholder="https://example.com"
          />
        </label>
        <label>
          Tech stack
          <input name="stack" defaultValue={stack || ""} maxLength={200} />
        </label>
        <button className="button small-button">Save presentation</button>
      </form>
    </section>
  );
}

export function RepositoryAccess({ endpoint, repo }: { endpoint: string; repo: Repo }) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [acceptURL, setAcceptURL] = useState("");
  const invitations = useData<Invitation[]>(`${endpoint}/invitations`, version);
  const transfer = useData<{
    pending: { id: string; username: string; created_at: string } | null;
  }>(`${endpoint}/transfer`, version);
  const refresh = () => setVersion((value) => value + 1);
  const fullName = `${repo.owner}/${repo.name}`;
  const pending = invitations.data?.filter((item) => item.status === "pending") || [];
  const answered = invitations.data?.filter((item) => item.status !== "pending") || [];
  return (
    <section className="panel settings-form" aria-label="Access and ownership">
      <h2>Invite collaborators</h2>
      <ErrorMessage error={error || invitations.error || transfer.error} />
      {notice && (
        <p role="status" className="green-text">
          {notice}
        </p>
      )}
      <p className="muted small-text">
        Invite a GITOWN username or an email address. People join only after
        they accept. Invitations expire after 14 days. Someone invited by email
        accepts with the link in that email, even if they create their account
        later.
      </p>
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          const form = event.currentTarget;
          const data = new FormData(form);
          setError("");
          setNotice("");
          setAcceptURL("");
          try {
            const created = await post<Invitation>(`${endpoint}/invitations`, {
              username: data.get("username"),
              email: data.get("email"),
              role: data.get("role"),
            });
            form.reset();
            setNotice(
              `Invitation sent to ${created.username ? `@${created.username}` : created.email}.`,
            );
            setAcceptURL(created.accept_url || "");
            refresh();
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        <label>
          Invite username
          <input name="username" autoComplete="off" placeholder="gitown-user" />
        </label>
        <label>
          Or invite email
          <input name="email" type="email" autoComplete="off" />
        </label>
        <label>
          Invitation role
          <select name="role" defaultValue="read">
            <option value="read">Read</option>
            <option value="triage">Triage</option>
            <option value="write">Write</option>
            <option value="maintain">Maintain</option>
          </select>
        </label>
        <button className="button small-button">Send invitation</button>
      </form>
      {acceptURL && (
        <div className="copy-field">
          <code>{acceptURL}</code>
          <CopyButton text={acceptURL} label="Copy invitation link" />
        </div>
      )}
      <h3>Pending invitations</h3>
      {invitations.loading ? (
        <Loading />
      ) : pending.length ? (
        pending.map((item) => (
          <p key={item.id} className="member-row">
            <span>
              {item.username ? `@${item.username}` : item.email} · {item.role} · expires{" "}
              {date(item.expires_at)}
            </span>
            <button
              className="button small-button"
              type="button"
              aria-label={`Revoke invitation for ${item.username || item.email}`}
              onClick={async () => {
                setError("");
                setNotice("");
                try {
                  await remove(`${endpoint}/invitations/${item.id}`);
                  setNotice("Invitation revoked.");
                  refresh();
                } catch (saveError) {
                  setError((saveError as Error).message);
                }
              }}
            >
              Revoke
            </button>
          </p>
        ))
      ) : (
        <p className="muted small-text">No pending invitations.</p>
      )}
      {answered.length > 0 && (
        <details>
          <summary>Past invitations</summary>
          {answered.map((item) => (
            <p key={item.id} className="muted small-text">
              {item.username ? `@${item.username}` : item.email} · {item.role} · {item.status}
            </p>
          ))}
        </details>
      )}
      <h3>Transfer ownership</h3>
      {transfer.data?.pending ? (
        <p className="member-row">
          <span>
            Waiting for @{transfer.data.pending.username} to accept since{" "}
            {date(transfer.data.pending.created_at)}.
          </span>
          <button
            className="button small-button"
            type="button"
            onClick={async () => {
              setError("");
              setNotice("");
              try {
                await remove(`${endpoint}/transfer`);
                setNotice("Ownership transfer cancelled.");
                refresh();
              } catch (saveError) {
                setError((saveError as Error).message);
              }
            }}
          >
            Cancel transfer
          </button>
        </p>
      ) : (
        <form
          onSubmit={async (event) => {
            event.preventDefault();
            const form = event.currentTarget;
            const data = new FormData(form);
            setError("");
            setNotice("");
            try {
              await post(`${endpoint}/transfer`, {
                username: data.get("username"),
                confirm: data.get("confirm"),
              });
              form.reset();
              setNotice("Transfer requested. The new owner must accept it.");
              refresh();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <p className="muted small-text">
            The new owner receives every issue, unite request, and setting. You
            stay on as a maintainer.
          </p>
          <label>
            New owner username
            <input name="username" required />
          </label>
          <label>
            Type {fullName} to confirm
            <input name="confirm" required autoComplete="off" />
          </label>
          <button className="button small-button">Request transfer</button>
        </form>
      )}
    </section>
  );
}

export function InvitationAccept() {
  const token = useSearchParams().get("token") || "";
  const router = useRouter();
  const [state, setState] = useState<"idle" | "busy" | "done">("idle");
  const [error, setError] = useState(
    token ? "" : "This invitation link is incomplete.",
  );
  return (
    <>
      <div className="page-heading">
        <div>
          <h1>Repository invitation</h1>
          <p>
            Accepting adds you as a collaborator with the role chosen by the
            repository owner.
          </p>
        </div>
      </div>
      <ErrorMessage error={error} />
      {state === "done" ? (
        <section className="panel">
          <p role="status" className="green-text">
            Invitation accepted.
          </p>
          <Link className="button" href="/">
            Go to your workspace
          </Link>
        </section>
      ) : (
        <section className="panel">
          <button
            className="button primary"
            disabled={!token || state === "busy"}
            onClick={async () => {
              setState("busy");
              setError("");
              try {
                await post("/invitations/accept", { token });
                setState("done");
                router.refresh();
              } catch (acceptError) {
                setError((acceptError as Error).message);
                setState("idle");
              }
            }}
          >
            {state === "busy" ? "Accepting…" : "Accept invitation"}
          </button>
        </section>
      )}
    </>
  );
}

export function InvitationsPage() {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const invitations = useData<Invitation[]>("/user/invitations", version);
  const transfers = useData<
    {
      id: string;
      owner: string;
      repository: string;
      actor: string;
      status: string;
    }[]
  >("/user/transfers", version);
  async function respond(kind: "invitations" | "transfers", id: string, accept: boolean) {
    setError("");
    try {
      await post(`/user/${kind}/${id}/${accept ? "accept" : "decline"}`, {});
      setVersion((value) => value + 1);
    } catch (saveError) {
      setError((saveError as Error).message);
    }
  }
  return (
    <>
      <div className="page-heading">
        <div>
          <h1>Invitations</h1>
          <p>Accept repository access and ownership transfers sent to you.</p>
        </div>
      </div>
      <ErrorMessage error={error || invitations.error || transfers.error} />
      <section className="panel">
        <h2>Repository invitations</h2>
        {invitations.loading ? (
          <Loading />
        ) : invitations.data?.length ? (
          invitations.data.map((item) => (
            <article className="notification-item" key={item.id}>
              <div>
                <strong>
                  {item.owner}/{item.repository}
                </strong>
                <p>
                  {item.role} · {item.status}
                </p>
              </div>
              {item.status === "pending" && (
                <div>
                  <button
                    className="button small-button"
                    aria-label={`Accept invitation to ${item.owner}/${item.repository}`}
                    onClick={() => respond("invitations", item.id, true)}
                  >
                    Accept
                  </button>
                  <button
                    className="button small-button"
                    aria-label={`Decline invitation to ${item.owner}/${item.repository}`}
                    onClick={() => respond("invitations", item.id, false)}
                  >
                    Decline
                  </button>
                </div>
              )}
            </article>
          ))
        ) : (
          <p className="muted">No invitations.</p>
        )}
      </section>
      <section className="panel">
        <h2>Ownership transfers</h2>
        {transfers.data?.length ? (
          transfers.data.map((item) => (
            <article className="notification-item" key={item.id}>
              <div>
                <strong>
                  {item.owner}/{item.repository}
                </strong>
                <p>
                  From @{item.actor} · {item.status}
                </p>
              </div>
              {item.status === "pending" && (
                <div>
                  <button
                    className="button small-button"
                    onClick={() => respond("transfers", item.id, true)}
                  >
                    Accept
                  </button>
                  <button
                    className="button small-button"
                    onClick={() => respond("transfers", item.id, false)}
                  >
                    Decline
                  </button>
                </div>
              )}
            </article>
          ))
        ) : (
          <p className="muted">No ownership transfers.</p>
        )}
      </section>
    </>
  );
}

export function SubscriptionMode({
  path,
  version,
}: {
  path: string;
  version: number;
}) {
  const [local, setLocal] = useState(0);
  const [error, setError] = useState("");
  const subscription = useData<{ subscribed: boolean; mode: string }>(
    path,
    version + local,
  );
  const value =
    subscription.data?.mode || (subscription.data?.subscribed ? "participate" : "off");
  return (
    <label>
      Notification mode
      <select
        aria-label="Notification mode"
        value={value}
        onChange={async (event) => {
          const mode = event.target.value;
          setError("");
          try {
            await put(
              path,
              mode === "off"
                ? { subscribed: false }
                : { subscribed: true, mode },
            );
            setLocal((item) => item + 1);
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        <option value="participate">Participate</option>
        <option value="watch">Watch</option>
        <option value="ignore">Ignore</option>
        <option value="off">Not following</option>
      </select>
      <ErrorMessage error={error} />
    </label>
  );
}
