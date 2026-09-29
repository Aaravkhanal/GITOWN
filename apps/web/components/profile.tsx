"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { ArrowUpRight, FolderGit2 } from "lucide-react";
import { date, put, type Profile, type PublicRepository } from "@/lib/api";
import { Avatar, Badge, ErrorMessage, Loading, useData } from "./ui";

function EmailPreference() {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const preference = useData<{ email_notifications: string }>(
    "/user/email-notifications",
    version,
  );
  if (preference.loading) return <Loading />;
  return (
    <section className="panel form-panel">
      <h2>Email updates</h2>
      <p>
        Immediate mail is delivered when an SMTP server is configured.
        Otherwise GITOWN keeps a delivery record. Digest groups messages, and
        off stops email.
      </p>
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
            <span className="muted small-text">
              Created {date(repo.created_at)}
            </span>
          </div>
        </Link>
      ))}
    </div>
  );
}

export function PublicProfile({
  username,
  currentUsername,
}: {
  username: string;
  currentUsername?: string;
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
      <ErrorMessage error={followError} />
      <section className="panel profile-summary">
        <p>{person.bio || "This builder has not added a bio yet."}</p>
        {person.skills && <p>Skills: {person.skills}</p>}
        {person.availability && <p>Availability: {person.availability}</p>}
        {person.open_to_collaborators && <Badge kind="green">Open to collaborators</Badge>}
        {!!person.badges?.length && (
          <p>{person.badges.map((badge) => <Badge key={badge}>{badge}</Badge>)}</p>
        )}
        {person.contributions && (
          <p className="muted small-text">
            {person.contributions.merged_unites} merged Unite requests ·{" "}
            {person.contributions.approvals} approvals ·{" "}
            {person.contributions.closed_issues} closed issues
          </p>
        )}
        {person.location && <span>{person.location}</span>}
        {person.website && (
          <a href={person.website} target="_blank" rel="noopener noreferrer">
            Website <ArrowUpRight size={14} />
          </a>
        )}
      </section>
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
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (profile.data) setSelected(profile.data.showcase.map((repo) => repo.id));
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
          Skills
          <input name="skills" defaultValue={profile.data.skills} maxLength={200} />
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
