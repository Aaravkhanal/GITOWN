"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, date, put, type Notification } from "@/lib/api";
import { Badge, ErrorMessage, Loading, useData } from "./ui";

const actions: Record<string, string> = {
  issue_opened: "opened",
  issue_comment: "commented on",
  issue_closed: "closed",
  issue_reopened: "reopened",
  pull_opened: "opened",
  pull_comment: "commented on",
  pull_review: "reviewed",
  pull_closed: "closed",
  pull_reopened: "reopened",
  pull_merged: "merged",
  mention: "mentioned you on",
  assignment: "assigned you to",
  review_request: "requested your review on",
  invitation: "invited you to",
  ownership_transfer: "offered ownership of",
  check_success: "reported a passing check on",
  check_failure: "reported a failing check on",
  follow: "started following you",
};

const kindFilters: [string, string][] = [
  ["", "Every kind"],
  ["mention", "Mentions"],
  ["review_request", "Review requests"],
  ["assignment", "Assignments"],
  ["issue_opened", "New issues"],
  ["pull_opened", "New Unite requests"],
  ["check_failure", "Failing checks"],
  ["invitation", "Invitations"],
  ["follow", "Followers"],
];

const pageSize = 50;

function notificationHref(item: Notification) {
  if (item.kind === "invitation" || item.kind === "ownership_transfer")
    return "/invitations";
  if (item.kind === "follow") return `/u/${item.actor}`;
  return `/repos/${item.owner}/${item.repository}/${item.pull ? `pulls/${item.pull}` : `issues/${item.issue}`}`;
}

// useUnreadCount keeps the sidebar badge and inbox heading in sync.
export function useUnreadCount(version = 0, enabled = true) {
  const count = useData<{ unread: number }>(
    enabled ? "/user/notifications/unread-count" : null,
    version,
  );
  return count.data?.unread ?? 0;
}

export function Inbox({ onChange }: { onChange?: () => void }) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("all");
  const [kind, setKind] = useState("");
  const [older, setOlder] = useState<Notification[]>([]);
  const [exhausted, setExhausted] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const query = `?limit=${pageSize}${filter === "unread" ? "&filter=unread" : ""}${kind ? `&kind=${kind}` : ""}`;
  const notifications = useData<Notification[]>(
    `/user/notifications${query}`,
    version,
  );
  const unread = useUnreadCount(version);
  useEffect(() => {
    setOlder([]);
    setExhausted(false);
  }, [query, version]);
  const items = [...(notifications.data || []), ...older];
  const refresh = () => {
    setVersion((value) => value + 1);
    onChange?.();
  };
  async function markRead(item: Notification) {
    if (item.read_at) return;
    setError("");
    try {
      await put(`/user/notifications/${item.id}/read`, {});
      refresh();
    } catch (saveError) {
      setError((saveError as Error).message);
    }
  }
  return (
    <>
      <div className="page-heading">
        <div>
          <h1>Your inbox</h1>
          <p>
            Updates from repositories you watch, threads you joined, and people
            who follow you.
          </p>
        </div>
        <div className="heading-actions">
          <Badge>{unread} unread</Badge>
          {unread > 0 && (
            <button
              className="button small-button"
              onClick={async () => {
                setError("");
                try {
                  await put("/user/notifications/read", {});
                  refresh();
                } catch (saveError) {
                  setError((saveError as Error).message);
                }
              }}
            >
              Mark all read
            </button>
          )}
        </div>
      </div>
      <div className="inbox-filters">
        <label>
          Show
          <select
            aria-label="Show notifications"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          >
            <option value="all">All</option>
            <option value="unread">Unread</option>
          </select>
        </label>
        <label>
          Kind
          <select
            aria-label="Notification kind"
            value={kind}
            onChange={(event) => setKind(event.target.value)}
          >
            {kindFilters.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
      </div>
      <ErrorMessage error={error || notifications.error} />
      {notifications.loading ? (
        <Loading />
      ) : items.length ? (
        <>
          <div className="panel notification-list">
            {items.map((item) => (
              <article
                key={item.id}
                className={`notification-item ${item.read_at ? "" : "unread"}`}
              >
                <div>
                  <Link
                    href={notificationHref(item)}
                    onClick={() => void markRead(item)}
                  >
                    <strong>@{item.actor}</strong>{" "}
                    {actions[item.kind] || "updated"}
                    {item.kind !== "follow" && (
                      <>
                        {" "}
                        <strong>{item.title}</strong>
                      </>
                    )}
                  </Link>
                  {item.excerpt && (
                    <p className="notification-excerpt">{item.excerpt}</p>
                  )}
                  <p>
                    {item.repository
                      ? `${item.owner}/${item.repository}${item.pull || item.issue ? `#${item.pull ?? item.issue}` : ""} · `
                      : ""}
                    {date(item.created_at)}
                  </p>
                </div>
                {!item.read_at && (
                  <button
                    className="button small-button"
                    onClick={() => void markRead(item)}
                  >
                    Mark read
                  </button>
                )}
              </article>
            ))}
          </div>
          {!exhausted && items.length >= pageSize && (
            <button
              className="button"
              disabled={loadingOlder}
              onClick={async () => {
                setLoadingOlder(true);
                setError("");
                try {
                  const last = items[items.length - 1];
                  const page = await api<Notification[]>(
                    `/user/notifications${query}&before=${last.id}`,
                  );
                  setOlder((current) => [...current, ...page]);
                  if (page.length < pageSize) setExhausted(true);
                } catch (loadError) {
                  setError((loadError as Error).message);
                } finally {
                  setLoadingOlder(false);
                }
              }}
            >
              {loadingOlder ? "Loading…" : "Load older notifications"}
            </button>
          )}
        </>
      ) : (
        <div className="empty-state panel">
          <h3>All caught up</h3>
          <p>
            {filter === "unread" || kind
              ? "Nothing matches these filters."
              : "Updates from watched repositories and followed threads will appear here."}
          </p>
        </div>
      )}
    </>
  );
}
