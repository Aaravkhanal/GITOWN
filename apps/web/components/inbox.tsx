"use client";

import Link from "next/link";
import { useState } from "react";
import { date, put, type Notification } from "@/lib/api";
import { Badge, ErrorMessage, Loading, useData } from "./ui";

const actions: Record<Notification["kind"], string> = {
  issue_comment: "commented on",
  issue_closed: "closed",
  issue_reopened: "reopened",
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
          <p>Updates from issues you follow or have joined.</p>
        </div>
        <Badge>{unread} unread</Badge>
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
                <Link href={`/repos/${item.owner}/${item.repository}/issues`}>
                  <strong>@{item.actor}</strong> {actions[item.kind]}{" "}
                  <strong>{item.title}</strong>
                </Link>
                <p>
                  {item.owner}/{item.repository}#{item.issue} ·{" "}
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
          <p>Updates from subscribed issues will appear here.</p>
        </div>
      )}
    </>
  );
}
