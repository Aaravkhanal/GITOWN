"use client";

import { useState } from "react";
import {
  patch,
  post,
  put,
  type Invitation,
  type Issue,
  type IssueAssignees,
  type Label,
} from "@/lib/api";
import { Badge, ErrorMessage, Loading, useData } from "./ui";

export function IssuePlanning({
  endpoint,
  issue,
  canTriage,
  onSaved,
}: {
  endpoint: string;
  issue: Issue;
  canTriage: boolean;
  onSaved?: () => void;
}) {
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  if (!canTriage) return null;
  async function save(body: Record<string, unknown>, message: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await put(`${endpoint}/issues/${issue.number}/planning`, body);
      setNotice(message);
      onSaved?.();
    } catch (saveError) {
      setError((saveError as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      className="issue-labels"
      onSubmit={(event) => {
        event.preventDefault();
        const data = new FormData(event.currentTarget);
        const estimate = String(data.get("estimate") || "");
        const duplicate = String(data.get("duplicate_of") || "");
        void save(
          {
            pinned: data.get("pinned") === "on",
            priority: data.get("priority"),
            ...(estimate
              ? { estimate: Number(estimate) }
              : { clear_estimate: true }),
            due_date: data.get("due_date") || "",
            iteration: data.get("iteration") || "",
            ...(duplicate ? { duplicate_of: Number(duplicate) } : {}),
          },
          "Planning saved.",
        );
      }}
    >
      <h4>Planning</h4>
      <ErrorMessage error={error} />
      {notice && <p className="green-text">{notice}</p>}
      <label className="checkbox-row">
        <input name="pinned" type="checkbox" defaultChecked={issue.pinned} />{" "}
        Pin this issue
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
      {issue.duplicate_of ? (
        <p className="small-text">
          Marked as a duplicate of #{issue.duplicate_of}.{" "}
          <button
            type="button"
            className="text-button"
            disabled={busy}
            onClick={() =>
              save({ clear_duplicate: true }, "Duplicate mark removed.")
            }
          >
            Not a duplicate
          </button>
        </p>
      ) : (
        <label>
          Duplicate of issue number (closes this issue)
          <input name="duplicate_of" type="number" min="1" />
        </label>
      )}
      <button className="button small-button" disabled={busy}>
        {busy ? "Saving…" : "Save planning"}
      </button>
    </form>
  );
}

export function CommentEdit({
  path,
  initial,
  onSaved,
}: {
  path: string;
  initial: string;
  onSaved?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState(initial);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  if (!open) {
    return (
      <button
        className="text-button"
        type="button"
        onClick={() => {
          setBody(initial);
          setOpen(true);
        }}
      >
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
          onSaved?.();
        } catch (saveError) {
          setError((saveError as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <ErrorMessage error={error} />
      <textarea
        aria-label="Edit comment"
        value={body}
        onChange={(event) => setBody(event.target.value)}
        maxLength={10000}
        rows={3}
        required
      />
      <button className="button small-button" disabled={busy}>
        {busy ? "Saving…" : "Save edit"}
      </button>
      <button
        type="button"
        className="text-button"
        onClick={() => setOpen(false)}
      >
        Cancel
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

export function UnitePanel({
  endpoint,
  number,
  canTriage,
  canComment,
  headSHA,
}: {
  endpoint: string;
  number: string;
  canTriage: boolean;
  canComment: boolean;
  headSHA: string;
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
          <article className="board-card" key={thread.id}>
            <strong>
              {thread.path}:{thread.line} ({thread.side})
            </strong>
            <p>
              @{thread.author}
              {thread.outdated ? " · outdated" : ""}
              {thread.resolved ? " · resolved" : ""}
              {thread.reply_count ? ` · ${thread.reply_count} replies` : ""}
            </p>
            <p>{thread.body}</p>
            {canTriage && (
              <button
                className="button small-button"
                type="button"
                onClick={async () => {
                  setError("");
                  try {
                    await post(`${base}/threads/${thread.id}/resolve`, {
                      resolved: !thread.resolved,
                    });
                    refresh();
                  } catch (saveError) {
                    setError((saveError as Error).message);
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
                  const data = new FormData(event.currentTarget);
                  setError("");
                  try {
                    await post(`${base}/threads/${thread.id}/replies`, {
                      body: data.get("body"),
                    });
                    event.currentTarget.reset();
                    refresh();
                  } catch (saveError) {
                    setError((saveError as Error).message);
                  }
                }}
              >
                <input name="body" maxLength={10000} placeholder="Reply" required />
                <button className="button small-button">Reply</button>
              </form>
            )}
          </article>
        ))
      )}
      {canComment && headSHA && (
        <form
          className="comment-form"
          onSubmit={async (event) => {
            event.preventDefault();
            const data = new FormData(event.currentTarget);
            setError("");
            try {
              await post(`${base}/threads`, {
                commit_sha: headSHA,
                path: data.get("path"),
                side: data.get("side"),
                line: Number(data.get("line")),
                body: data.get("body"),
              });
              event.currentTarget.reset();
              refresh();
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <h4>Comment on a line</h4>
          <label>
            File path
            <input name="path" required />
          </label>
          <label>
            Side
            <select name="side" defaultValue="right">
              <option value="right">Right</option>
              <option value="left">Left</option>
            </select>
          </label>
          <label>
            Line
            <input name="line" type="number" min="1" required />
          </label>
          <label>
            Comment
            <textarea name="body" maxLength={10000} rows={3} required />
          </label>
          <button className="button small-button">Add line comment</button>
        </form>
      )}
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

export function OwnerDelivery({
  endpoint,
  homepage,
  stack,
}: {
  endpoint: string;
  homepage?: string;
  stack?: string;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const invitations = useData<Invitation[]>(`${endpoint}/invitations`, version);
  return (
    <section className="panel settings-form">
      <h2>Project presentation</h2>
      <ErrorMessage error={error || invitations.error} />
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
      <h3>Invitations</h3>
      <p className="muted small-text">
        Invite an existing username or an email address. Pending invitations
        expire after 14 days. Email is stored for delivery and sent when mail
        is configured.
      </p>
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setError("");
          setNotice("");
          try {
            await post(`${endpoint}/invitations`, {
              username: data.get("username"),
              email: data.get("email"),
              role: data.get("role"),
            });
            event.currentTarget.reset();
            setNotice("Invitation created.");
            setVersion((value) => value + 1);
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        <label>
          Username
          <input name="username" />
        </label>
        <label>
          Email
          <input name="email" type="email" />
        </label>
        <label>
          Role
          <select name="role" defaultValue="read">
            <option value="read">read</option>
            <option value="triage">triage</option>
            <option value="write">write</option>
            <option value="maintain">maintain</option>
          </select>
        </label>
        <button className="button small-button">Send invitation</button>
      </form>
      {invitations.loading ? (
        <Loading />
      ) : (
        invitations.data?.map((item) => (
          <p key={item.id}>
            {item.username || item.email} · {item.role} · {item.status}
          </p>
        ))
      )}
      <h3>Ownership transfer</h3>
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setError("");
          setNotice("");
          try {
            await post(`${endpoint}/transfer`, { username: data.get("username") });
            setNotice("Transfer requested. The recipient must accept it.");
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        <label>
          New owner username
          <input name="username" required />
        </label>
        <button className="button small-button">Request transfer</button>
      </form>
    </section>
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
                    onClick={() => respond("invitations", item.id, true)}
                  >
                    Accept
                  </button>
                  <button
                    className="button small-button"
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
