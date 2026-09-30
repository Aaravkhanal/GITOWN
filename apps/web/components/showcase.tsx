"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import {
  ArrowUpRight,
  Package,
  Plus,
  Sparkles,
  Trash2,
  Users,
} from "lucide-react";
import {
  date,
  post,
  put,
  type RepositoryShowcase,
  type Screenshot,
} from "@/lib/api";
import { Avatar, Badge, ErrorMessage, Loading, useData } from "./ui";

// ProjectShowcase is the public page a project can share: it is generated
// from the README, presentation settings, people, Drops, and milestones.
export function ProjectShowcase({
  owner,
  name,
}: {
  owner: string;
  name: string;
}) {
  const showcase = useData<RepositoryShowcase>(
    `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/showcase`,
  );
  if (showcase.loading) return <Loading />;
  if (showcase.error || !showcase.data)
    return <ErrorMessage error={showcase.error || "Showcase unavailable."} />;
  const show = showcase.data;
  const repo = show.repository;
  const base = `/repos/${repo.owner}/${repo.name}`;
  return (
    <article className="project-showcase" aria-label="Project showcase">
      <div className="page-heading showcase-hero">
        <div>
          <p className="muted small-text">
            <Link href={`/u/${repo.owner}`}>{repo.owner}</Link> / {repo.name}
          </p>
          <h1>{repo.name}</h1>
          <p>{repo.description || "An independent project on GITOWN."}</p>
          {!!show.stack.length && (
            <div className="skill-tags" aria-label="Tech stack">
              {show.stack.map((item) => (
                <Badge key={item}>{item}</Badge>
              ))}
            </div>
          )}
        </div>
        <div className="heading-actions">
          {repo.homepage && (
            <a
              className="button primary"
              href={repo.homepage}
              target="_blank"
              rel="noopener noreferrer"
            >
              Open demo <ArrowUpRight size={14} />
            </a>
          )}
          <Link className="button" href={base}>
            View code
          </Link>
          <span className="muted small-text">
            <Sparkles size={14} /> {show.sparks} Sparks
          </span>
        </div>
      </div>
      {!!show.screenshots.length && (
        <section className="panel" aria-label="Screenshots">
          <h2>Screenshots</h2>
          <div className="showcase-shots">
            {show.screenshots.map((shot) => (
              <figure key={shot.url}>
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={shot.url}
                  alt={shot.caption || `${repo.name} screenshot`}
                  loading="lazy"
                  referrerPolicy="no-referrer"
                />
                {shot.caption && <figcaption>{shot.caption}</figcaption>}
              </figure>
            ))}
          </div>
        </section>
      )}
      <div className="showcase-grid">
        {show.readme && (
          <section className="panel" aria-label="About this project">
            <h2>About</h2>
            <p className="showcase-text">{show.readme}</p>
          </section>
        )}
        {show.setup && (
          <section className="panel" aria-label="Setup instructions">
            <h2>Setup</h2>
            <pre className="showcase-text">{show.setup}</pre>
          </section>
        )}
        <section className="panel" aria-label="Contributors">
          <h2>
            <Users size={16} /> People
          </h2>
          <div className="follow-list">
            {show.contributors.map((person) => (
              <Link key={person.username} href={`/u/${person.username}`}>
                <Avatar name={person.username} small />
                <span>
                  {person.display_name || person.username}
                  <span className="muted small-text">
                    {" "}
                    {person.role === "owner"
                      ? "Maintainer"
                      : `${person.merged} merged`}
                  </span>
                </span>
              </Link>
            ))}
          </div>
        </section>
        {show.latest_drop && (
          <section className="panel" aria-label="Latest Drop">
            <h2>
              <Package size={16} /> Latest Drop
            </h2>
            <p>
              <Link href={`${base}/drops`}>
                <strong>{show.latest_drop.tag}</strong> ·{" "}
                {show.latest_drop.title}
              </Link>
            </p>
            <p className="muted small-text">
              {date(show.latest_drop.created_at)} · {show.latest_drop.downloads}{" "}
              downloads
            </p>
          </section>
        )}
        {!!show.roadmap.length && (
          <section className="panel" aria-label="Roadmap">
            <h2>Roadmap</h2>
            <ul>
              {show.roadmap.map((milestone) => (
                <li key={milestone.title}>
                  <strong>{milestone.title}</strong>
                  <span className="muted small-text">
                    {" "}
                    {milestone.closed_issues}/
                    {milestone.open_issues + milestone.closed_issues} done
                    {milestone.due_date ? ` · due ${milestone.due_date}` : ""}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        )}
        <section className="panel" aria-label="Open collaboration tasks">
          <h2>Help wanted</h2>
          {show.tasks.length ? (
            <ul>
              {show.tasks.map((task) => (
                <li key={task.number}>
                  <Link href={`${base}/issues/${task.number}`}>
                    #{task.number} {task.title}
                  </Link>{" "}
                  {task.labels.map((label) => (
                    <Badge key={label}>{label}</Badge>
                  ))}
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted">
              No issues are labelled “good first task” or “help wanted” yet.
            </p>
          )}
        </section>
      </div>
    </article>
  );
}

// UnsubscribePage confirms the email opt-out behind an explicit button so
// link scanners that open the page do not unsubscribe anyone.
export function UnsubscribePage() {
  const [token, setToken] = useState<string | null>(null);
  useEffect(() => {
    setToken(new URLSearchParams(window.location.search).get("token") || "");
  }, []);
  const preview = useData<{ email: string; email_notifications: string }>(
    token ? `/email/unsubscribe?token=${encodeURIComponent(token)}` : null,
  );
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  if (token === null) return <Loading />;
  if (!token)
    return <ErrorMessage error="This unsubscribe link is missing its token." />;
  if (preview.loading) return <Loading />;
  if (preview.error || !preview.data)
    return <ErrorMessage error={preview.error || "Link not found."} />;
  return (
    <section className="panel form-panel" aria-label="Email preferences">
      <h1>Stop GITOWN email</h1>
      {done || preview.data.email_notifications === "off" ? (
        <p role="status" className="green-text">
          {preview.data.email} will no longer receive GITOWN email. You can turn
          email back on from your profile settings.
        </p>
      ) : (
        <>
          <p>
            Stop all notification email to <strong>{preview.data.email}</strong>
            ? In-app notifications keep working.
          </p>
          <ErrorMessage error={error} />
          <div className="form-actions">
            <button
              className="button primary"
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                setError("");
                try {
                  await post("/email/unsubscribe", { token });
                  setDone(true);
                } catch (saveError) {
                  setError((saveError as Error).message);
                } finally {
                  setBusy(false);
                }
              }}
            >
              Unsubscribe
            </button>
          </div>
        </>
      )}
    </section>
  );
}

// PresentationSettings edits what the showcase page shows: demo link,
// tech stack, and up to six HTTPS screenshots.
export function PresentationSettings({
  endpoint,
  homepage,
  stack,
}: {
  endpoint: string;
  homepage?: string;
  stack?: string;
}) {
  const current = useData<RepositoryShowcase>(`${endpoint}/showcase`);
  const [shots, setShots] = useState<Screenshot[] | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const screenshots = shots ?? current.data?.screenshots ?? [];
  const update = (index: number, field: keyof Screenshot, value: string) =>
    setShots(
      screenshots.map((shot, position) =>
        position === index ? { ...shot, [field]: value } : shot,
      ),
    );
  return (
    <>
      <h2>Project presentation</h2>
      <p className="muted small-text">
        These details appear on the repository page and its public showcase.
      </p>
      <ErrorMessage error={error} />
      {notice && <p className="green-text">{notice}</p>}
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          setError("");
          setNotice("");
          try {
            const saved = await put<{ screenshots: Screenshot[] }>(
              `${endpoint}/presentation`,
              {
                homepage: data.get("homepage"),
                stack: data.get("stack"),
                screenshots: screenshots.filter((shot) => shot.url),
              },
            );
            setShots(saved.screenshots);
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
          Tech stack (comma-separated)
          <input name="stack" defaultValue={stack || ""} maxLength={200} />
        </label>
        <fieldset className="profile-link-editor">
          <legend>Screenshots</legend>
          {screenshots.map((shot, index) => (
            <div className="screenshot-row" key={index}>
              <label>
                Screenshot {index + 1} image address
                <input
                  type="url"
                  value={shot.url}
                  maxLength={300}
                  placeholder="https://"
                  onChange={(event) => update(index, "url", event.target.value)}
                />
              </label>
              <label>
                Screenshot {index + 1} caption
                <input
                  value={shot.caption}
                  maxLength={140}
                  onChange={(event) =>
                    update(index, "caption", event.target.value)
                  }
                />
              </label>
              <button
                type="button"
                className="icon-button danger-icon"
                aria-label={`Remove screenshot ${index + 1}`}
                onClick={() =>
                  setShots(
                    screenshots.filter((_, position) => position !== index),
                  )
                }
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
          {screenshots.length < 6 && (
            <button
              type="button"
              className="button small-button"
              onClick={() =>
                setShots([...screenshots, { url: "", caption: "" }])
              }
            >
              <Plus size={14} /> Add screenshot
            </button>
          )}
        </fieldset>
        <button className="button small-button">Save presentation</button>
      </form>
    </>
  );
}
