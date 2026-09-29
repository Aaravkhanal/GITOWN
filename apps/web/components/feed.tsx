"use client";

import Link from "next/link";
import { Rss } from "lucide-react";
import { date, type FeedEvent } from "@/lib/api";
import { ErrorMessage, Loading, useData } from "./ui";

function eventDetail(event: FeedEvent) {
  if (event.kind === "repository_created") return "created a repository";
  if (event.kind === "repository_sparked") return "Sparked a repository";
  if (event.kind === "issue_opened") return "opened an issue";
  return "opened a Unite request";
}

function eventPath(event: FeedEvent) {
  const repository = `/repos/${encodeURIComponent(event.owner)}/${encodeURIComponent(event.repository)}`;
  if (event.kind === "issue_opened")
    return `${repository}/issues/${event.number}`;
  if (event.kind === "unite_opened")
    return `${repository}/pulls/${event.number}`;
  return repository;
}

export function FollowingFeed() {
  const feed = useData<FeedEvent[]>("/user/feed");
  return (
    <div className="form-page wide-form">
      <div className="page-heading">
        <div>
          <h1>Following feed</h1>
          <p>Public work from builders you follow, newest first.</p>
        </div>
        <Rss size={28} className="muted" />
      </div>
      <ErrorMessage error={feed.error} />
      {feed.loading ? (
        <Loading />
      ) : feed.data?.length ? (
        <div className="repo-list" aria-label="Following activity">
          {feed.data.map((event, index) => (
            <Link
              href={eventPath(event)}
              className="repo-card"
              key={`${event.kind}-${event.actor}-${event.created_at}-${index}`}
            >
              <div className="repo-card-icon">
                <Rss size={18} />
              </div>
              <div className="repo-card-main">
                <div className="repo-card-title">
                  <h3>
                    {event.actor} {eventDetail(event)}
                  </h3>
                </div>
                <p>
                  {event.owner}/{event.repository}
                  {event.number ? `#${event.number}` : ""} · {event.title}
                </p>
                <span className="muted small-text">
                  {date(event.created_at)}
                </span>
              </div>
            </Link>
          ))}
        </div>
      ) : (
        <div className="empty-state panel">
          <Rss size={28} />
          <h3>No activity yet</h3>
          <p>Follow a builder to see their public work here.</p>
          <Link className="button" href="/explore">
            Explore repositories
          </Link>
        </div>
      )}
    </div>
  );
}
