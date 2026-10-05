"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { ArrowUpRight, FolderGit2, Plus, Trash2 } from "lucide-react";
import {
  date,
  put,
  type FollowEntry,
  type Profile,
  type ProfileLink,
  type PublicRepository,
} from "@/lib/api";
import { Backers } from "@/components/phase12";
import { Avatar, Badge, ErrorMessage, Loading, useData } from "./ui";

function EmailPreference() {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const preference = useData<{
    email_notifications: string;
    smtp_configured: boolean;
  }>("/user/email-notifications", version);
  if (preference.loading) return <Loading />;
  return (
    <section className="panel form-panel">
      <h2>Email updates</h2>
      <p>
        Immediate sends each update as it happens. Digest combines updates into
        one email per interval. Off stops email; your inbox keeps working.
      </p>
      {preference.data && !preference.data.smtp_configured && (
        <p className="muted small-text">
          This server has no mail transport configured, so GITOWN records
          messages as suppressed instead of sending them.
        </p>
      )}
      <ErrorMessage error={error || preference.error} />
      <label>
        Delivery
        <select
          value={preference.data?.email_notifications || "immediate"}
          onChange={async (event) => {
            setError("");
            try {
              await put("/user/email-notifications", {
                email_notifications: event.target.value,
              });
              setVersion((value) => value + 1);
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <option value="immediate">Immediate</option>
          <option value="digest">Digest</option>
          <option value="off">Off</option>
        </select>
      </label>
    </section>
  );
}

function ContributionStrip({ username }: { username: string }) {
  const activity = useData<{
    total: number;
    days: { date: string; count: number }[];
  }>(`/users/${encodeURIComponent(username)}/contributions`);
  if (activity.loading) return <Loading />;
  if (activity.error) return <ErrorMessage error={activity.error} />;
  const counts = new Map(
    (activity.data?.days || []).map((day) => [day.date, day.count]),
  );
  const cells: { date: string; count: number }[] = [];
  const today = new Date();
  for (let offset = 179; offset >= 0; offset -= 1) {
    const day = new Date(
      Date.UTC(
        today.getUTCFullYear(),
        today.getUTCMonth(),
        today.getUTCDate() - offset,
      ),
    );
    const key = day.toISOString().slice(0, 10);
    cells.push({ date: key, count: counts.get(key) || 0 });
  }
  return (
    <section className="panel" aria-label="Public contributions">
      <h2>Public activity</h2>
      <p className="muted small-text">
        {activity.data?.total ?? 0} public contributions in the last 180 UTC
        days: pushes, merged Unite requests, reviews, issues, and comments.
        Sparks and work in private repositories are not counted.
      </p>
      <div className="contribution-strip">
        {cells.map((cell) => (
          <span
            key={cell.date}
            className={`contribution-cell level-${cell.count === 0 ? 0 : cell.count < 2 ? 1 : cell.count < 4 ? 2 : 3}`}
            title={`${cell.date}: ${cell.count}`}
          />
        ))}
      </div>
    </section>
  );
}

function RepositoryCards({
  username,
  repositories,
}: {
  username: string;
  repositories: PublicRepository[];
}) {
  return (
    <div className="repo-list">
      {repositories.map((repo) => (
        <Link
          className="repo-card"
          href={`/repos/${username}/${repo.name}`}
          key={repo.id}
        >
          <div className="repo-card-icon">
            <FolderGit2 size={20} />
          </div>
          <div className="repo-card-main">
            <div className="repo-card-title">
              <h3>{repo.name}</h3>
              {repo.archived && <Badge>Archived</Badge>}
            </div>
            <p>{repo.description || "No description yet."}</p>
            <div className="repo-card-meta">
              {repo.stack && <span>{repo.stack}</span>}
              <span>{repo.sparks ?? 0} Sparks</span>
              {repo.latest_drop && (
                <span>
                  Latest Drop {repo.latest_drop} · {repo.downloads} downloads
                </span>
              )}
              {repo.homepage && <span>Live demo</span>}
              <span>Created {date(repo.created_at)}</span>
            </div>
          </div>
        </Link>
      ))}
    </div>
  );
}

function FollowList({
  username,
  kind,
}: {
  username: string;
  kind: "followers" | "following";
}) {
  const [offset, setOffset] = useState(0);
  const list = useData<{ items: FollowEntry[]; has_more: boolean }>(
    `/users/${encodeURIComponent(username)}/${kind}?offset=${offset}`,
  );
  if (list.loading) return <Loading />;
  if (list.error) return <ErrorMessage error={list.error} />;
  const items = list.data?.items || [];
  return (
    <section
      className="panel"
      aria-label={kind === "followers" ? "Followers" : "Following"}
    >
      {items.length ? (
        <div className="follow-list">
          {items.map((person) => (
            <Link key={person.username} href={`/u/${person.username}`}>
              <Avatar name={person.username} small />
              <span>
                <strong>{person.display_name || person.username}</strong>{" "}
                <span className="muted small-text">@{person.username}</span>
                {person.bio && (
                  <span className="muted small-text"> · {person.bio}</span>
                )}
              </span>
            </Link>
          ))}
        </div>
      ) : (
        <p className="muted">
          {kind === "followers"
            ? "No followers yet."
            : "Not following anyone yet."}
        </p>
      )}
      <div className="form-actions">
        {offset > 0 && (
          <button
            className="button small-button"
            onClick={() => setOffset(Math.max(0, offset - 30))}
          >
            Previous
          </button>
        )}
        {list.data?.has_more && (
          <button
            className="button small-button"
            onClick={() => setOffset(offset + 30)}
          >
            Next
          </button>
        )}
      </div>
    </section>
  );
}

export function PublicProfile({
  username,
  currentUsername,
  tab,
}: {
  username: string;
  currentUsername?: string;
  tab?: string;
}) {
  const [version, setVersion] = useState(0);
  const [followError, setFollowError] = useState("");
  const [busy, setBusy] = useState(false);
  const profile = useData<Profile>(
    `/users/${encodeURIComponent(username)}/profile`,
    version,
  );
  if (profile.loading) return <Loading />;
  if (profile.error || !profile.data)
    return <ErrorMessage error={profile.error || "Profile not found."} />;
  const person = profile.data;
  return (
    <>
      <div className="page-heading">
        <div className="profile-heading">
          <Avatar name={person.username} />
          <div>
            <h1>{person.display_name}</h1>
            <p>
              @{person.username} · Joined {date(person.created_at)}
            </p>
          </div>
        </div>
        {currentUsername === person.username ? (
          <Link className="button" href="/settings/profile">
            Edit profile
          </Link>
        ) : currentUsername ? (
          <button
            className="button"
            disabled={busy}
            aria-pressed={person.followed}
            onClick={async () => {
              setBusy(true);
              setFollowError("");
              try {
                await put(
                  `/users/${encodeURIComponent(person.username)}/follow`,
                  { followed: !person.followed },
                );
                setVersion((value) => value + 1);
              } catch (error) {
                setFollowError(
                  error instanceof Error
                    ? error.message
                    : "Could not update follow.",
                );
              } finally {
                setBusy(false);
              }
            }}
          >
            {person.followed ? "Following" : "Follow builder"}
          </button>
        ) : (
          <Link className="button" href="/login">
            Sign in to follow
          </Link>
        )}
      </div>
      <p className="muted small-text">
        {person.followers} followers · {person.following} following
      </p>
      <nav className="profile-tabs" aria-label="Profile sections">
        <Link
          className={`button small-button ${!tab ? "primary" : ""}`}
          href={`/u/${person.username}`}
        >
          Overview
        </Link>
        <Link
          className={`button small-button ${tab === "followers" ? "primary" : ""}`}
          href={`/u/${person.username}/followers`}
        >
          Followers
        </Link>
        <Link
          className={`button small-button ${tab === "following" ? "primary" : ""}`}
          href={`/u/${person.username}/following`}
        >
          Following
        </Link>
      </nav>
      <ErrorMessage error={followError} />
      {tab === "followers" || tab === "following" ? (
        <FollowList username={person.username} kind={tab} />
      ) : (
        <ProfileOverview person={person} currentUsername={currentUsername} />
      )}
    </>
  );
}

function ProfileOverview({
  person,
  currentUsername,
}: {
  person: Profile;
  currentUsername?: string;
}) {
  return (
    <>
      <ContributionStrip username={person.username} />
      <section className="panel profile-summary">
        <p>{person.bio || "This builder has not added a bio yet."}</p>
        {!!person.skill_tags?.length && (
          <div className="skill-tags" aria-label="Skills">
            {person.skill_tags.map((skill) => (
              <Badge key={skill}>{skill}</Badge>
            ))}
          </div>
        )}
        {person.availability && <p>Availability: {person.availability}</p>}
        {person.open_to_collaborators && (
          <Badge kind="green">Open to collaborators</Badge>
        )}
        {!!person.badges?.length && (
          <div className="badge-row" aria-label="Contribution badges">
            {person.badges.map((badge) => (
              <span key={badge.id} title={badge.reason}>
                <Badge kind={`tier-${badge.tier}`}>
                  {badge.label} · {badge.tier}
                </Badge>
              </span>
            ))}
          </div>
        )}
        {person.contributions && (
          <p className="muted small-text">
            {person.contributions.merged_unites} merged Unite requests ·{" "}
            {person.contributions.reviews} reviews ·{" "}
            {person.contributions.resolved_issues} resolved issues ·{" "}
            {person.contributions.pushes} public pushes
          </p>
        )}
        {person.location && <span>{person.location}</span>}
        {(person.website || !!person.links?.length) && (
          <div className="profile-links" aria-label="Links">
            {person.website && (
              <a
                href={person.website}
                target="_blank"
                rel="noopener noreferrer me"
              >
                Website <ArrowUpRight size={14} />
              </a>
            )}
            {person.links?.map((link) => (
              <a
                key={link.url}
                href={link.url}
                target="_blank"
                rel="noopener noreferrer me"
              >
                {link.label} <ArrowUpRight size={14} />
              </a>
            ))}
          </div>
        )}
      </section>
      <Backers username={person.username} currentUsername={currentUsername} />
      {!!person.showcase.length && (
        <section aria-label="Showcase repositories">
          <div className="section-heading">
            <h2>Showcase</h2>
          </div>
          <RepositoryCards
            username={person.username}
            repositories={person.showcase}
          />
        </section>
      )}
      <section aria-label="Public repositories">
        <div className="section-heading">
          <h2>Public repositories · {person.repositories.length}</h2>
        </div>
        {person.repositories.length ? (
          <RepositoryCards
            username={person.username}
            repositories={person.repositories}
          />
        ) : (
          <div className="empty-state panel">
            <FolderGit2 size={28} />
            <h3>No public repositories yet</h3>
          </div>
        )}
      </section>
    </>
  );
}

export function ProfileSettings({ username }: { username: string }) {
  const [version, setVersion] = useState(0);
  const profile = useData<Profile>(
    `/users/${encodeURIComponent(username)}/profile`,
    version,
  );
  const [selected, setSelected] = useState<string[]>([]);
  const [links, setLinks] = useState<ProfileLink[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (profile.data) {
      setSelected(profile.data.showcase.map((repo) => repo.id));
      setLinks(profile.data.links || []);
    }
  }, [profile.data]);
  if (profile.loading) return <Loading />;
  if (profile.error || !profile.data)
    return <ErrorMessage error={profile.error || "Profile unavailable."} />;
  return (
    <>
      <div className="page-heading">
        <div>
          <h1>Your profile</h1>
          <p>
            Introduce yourself and choose the work you want visitors to see
            first.
          </p>
        </div>
        <Link className="button" href={`/u/${username}`}>
          View profile
        </Link>
      </div>
      <ErrorMessage error={error} />
      {notice && (
        <p role="status" className="green-text">
          {notice}
        </p>
      )}
      <form
        className="panel form-panel"
        onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          setNotice("");
          const data = new FormData(event.currentTarget);
          try {
            await put<Profile>("/user/profile", {
              display_name: data.get("display_name"),
              bio: data.get("bio"),
              website: data.get("website"),
              location: data.get("location"),
              skills: data.get("skills"),
              availability: data.get("availability"),
              open_to_collaborators: data.get("open_to_collaborators") === "on",
              links: links.filter((link) => link.label || link.url),
            });
            setNotice("Profile saved.");
            setVersion((value) => value + 1);
          } catch (saveError) {
            setError((saveError as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <h2>About you</h2>
        <label>
          Display name
          <input
            name="display_name"
            defaultValue={profile.data.display_name}
            maxLength={80}
            required
          />
        </label>
        <label>
          Bio
          <textarea
            name="bio"
            defaultValue={profile.data.bio}
            maxLength={500}
            rows={4}
          />
        </label>
        <label>
          Location
          <input
            name="location"
            defaultValue={profile.data.location}
            maxLength={100}
          />
        </label>
        <label>
          HTTPS website
          <input
            name="website"
            type="url"
            defaultValue={profile.data.website}
            maxLength={300}
            placeholder="https://example.com"
          />
        </label>
        <label>
          Skills (comma-separated)
          <input
            name="skills"
            defaultValue={profile.data.skills}
            maxLength={200}
            placeholder="Go, PostgreSQL, React"
          />
        </label>
        <label>
          Availability
          <input
            name="availability"
            defaultValue={profile.data.availability}
            maxLength={200}
          />
        </label>
        <label className="checkbox-row">
          <input
            name="open_to_collaborators"
            type="checkbox"
            defaultChecked={profile.data.open_to_collaborators}
          />
          Open to collaborators
        </label>
        <fieldset className="profile-link-editor">
          <legend>Links</legend>
          {links.map((link, index) => (
            <div className="screenshot-row" key={index}>
              <label>
                Link {index + 1} address
                <input
                  type="url"
                  value={link.url}
                  maxLength={300}
                  placeholder="https://"
                  onChange={(event) =>
                    setLinks(
                      links.map((item, position) =>
                        position === index
                          ? { ...item, url: event.target.value }
                          : item,
                      ),
                    )
                  }
                />
              </label>
              <label>
                Link {index + 1} label
                <input
                  value={link.label}
                  maxLength={40}
                  placeholder="Blog"
                  onChange={(event) =>
                    setLinks(
                      links.map((item, position) =>
                        position === index
                          ? { ...item, label: event.target.value }
                          : item,
                      ),
                    )
                  }
                />
              </label>
              <button
                type="button"
                className="icon-button danger-icon"
                aria-label={`Remove link ${index + 1}`}
                onClick={() =>
                  setLinks(links.filter((_, position) => position !== index))
                }
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
          {links.length < 5 && (
            <button
              type="button"
              className="button small-button"
              onClick={() => setLinks([...links, { label: "", url: "" }])}
            >
              <Plus size={14} /> Add link
            </button>
          )}
        </fieldset>
        <div className="form-actions">
          <button className="button primary" disabled={busy}>
            Save profile
          </button>
        </div>
      </form>
      <EmailPreference />
      <section className="panel form-panel" aria-label="Repository showcase">
        <h2>Repository showcase</h2>
        <p>
          Choose up to six of your public repositories. Your selection appears
          in this order.
        </p>
        {profile.data.repositories.length ? (
          <div className="showcase-choices">
            {profile.data.repositories.map((repo) => (
              <label key={repo.id}>
                <input
                  type="checkbox"
                  checked={selected.includes(repo.id)}
                  onChange={(event) => {
                    setError("");
                    if (event.target.checked) {
                      if (selected.length >= 6) {
                        setError("Choose up to six repositories.");
                        return;
                      }
                      setSelected([...selected, repo.id]);
                    } else setSelected(selected.filter((id) => id !== repo.id));
                  }}
                />
                {repo.name}
              </label>
            ))}
          </div>
        ) : (
          <p className="muted">
            Create or make a repository public to add it here.
          </p>
        )}
        <div className="form-actions">
          <button
            className="button primary"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setError("");
              setNotice("");
              try {
                await put<Profile>("/user/showcase", {
                  repository_ids: selected,
                });
                setNotice("Showcase saved.");
                setVersion((value) => value + 1);
              } catch (saveError) {
                setError((saveError as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            Save showcase
          </button>
        </div>
      </section>
    </>
  );
}
