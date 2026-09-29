"use client";

import Link from "next/link";
import { useState } from "react";
import { date, put, type Notification } from "@/lib/api";
import { Badge, ErrorMessage, Loading, useData } from "./ui";

const actions: Record<string, string> = {
  issue_comment: "commented on",
  issue_closed: "closed",
  issue_reopened: "reopened",
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
  check_success: "reported a successful check on",
  check_failure: "reported a failed check on",
};

export function Inbox() {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const notifications = useData<Notification[]>("/user/notifications", version);
  const unread =
    notifications.data?.filter((item) => !item.read_at).length || 0;
  return (
    <>
      <div className="page-heading">
        <div>
          <h1>Your inbox</h1>
          <p>
            Updates from issues and Unite requests you follow or have joined.
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
                  setVersion((value) => value + 1);
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
      <ErrorMessage error={error || notifications.error} />
      {notifications.loading ? (
        <Loading />
      ) : notifications.data?.length ? (
        <div className="panel notification-list">
          {notifications.data.map((item) => (
            <article
              key={item.id}
              className={`notification-item ${item.read_at ? "" : "unread"}`}
            >
              <div>
                <Link
                  href={
                    item.kind === "invitation" || item.kind === "ownership_transfer"
                      ? "/invitations"
                      : `/repos/${item.owner}/${item.repository}/${item.pull ? `pulls/${item.pull}` : `issues/${item.issue}`}`
                  }
                >
                  <strong>@{item.actor}</strong>{" "}
                  {actions[item.kind] || "updated"}{" "}
                  <strong>{item.title}</strong>
                </Link>
                <p>
                  {item.owner}/{item.repository}#{item.pull ?? item.issue} ·{" "}
                  {date(item.created_at)}
                </p>
              </div>
              {!item.read_at && (
                <button
                  className="button small-button"
                  onClick={async () => {
                    setError("");
                    try {
                      await put(`/user/notifications/${item.id}/read`, {});
                      setVersion((value) => value + 1);
                    } catch (saveError) {
                      setError((saveError as Error).message);
                    }
                  }}
                >
                  Mark read
                </button>
              )}
            </article>
          ))}
        </div>
      ) : (
        <div className="empty-state panel">
          <h3>All caught up</h3>
          <p>
            Updates from followed issues and Unite requests will appear here.
          </p>
        </div>
      )}
    </>
  );
}
