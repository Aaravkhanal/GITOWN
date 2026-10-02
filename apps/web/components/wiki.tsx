"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { BookOpenText, History, Plus, Save } from "lucide-react";
import { date, put, repoPath, type Commit, type Repo } from "@/lib/api";
import { ErrorMessage, Loading, useData } from "@/components/ui";
import { SafeMarkdown } from "@/components/ecosystem";

type WikiIndex = { pages: string[]; head_sha: string };
type WikiPage = { slug: string; content: string; head_sha: string };

const validSlug = /^[a-z0-9][a-z0-9-]{0,79}$/;

export function WikiPanel({
  endpoint,
  repo,
  pageSlug,
}: {
  endpoint: string;
  repo: Repo;
  pageSlug?: string;
}) {
  const router = useRouter();
  const [version, setVersion] = useState(0);
  const [newSlug, setNewSlug] = useState("");
  const [editing, setEditing] = useState(false);
  const [content, setContent] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [showHistory, setShowHistory] = useState(false);
  const [search, setSearch] = useState("");
  const [submitted, setSubmitted] = useState("");
  const slug = pageSlug || "home";
  const basePath = `${repoPath(repo)}/wiki`;
  const index = useData<WikiIndex>(`${endpoint}/wiki`, version);
  const exists = index.data?.pages.includes(slug) ?? false;
  const page = useData<WikiPage>(
    exists ? `${endpoint}/wiki/${encodeURIComponent(slug)}` : null,
    version,
  );
  const found = useData<{ items: { slug: string; line: string; text: string }[] }>(
    submitted ? `${endpoint}/wiki-search?q=${encodeURIComponent(submitted)}` : null,
    version,
  );
  const history = useData<Commit[]>(
    exists && showHistory
      ? `${endpoint}/commits?ref=${encodeURIComponent(repo.default_branch)}&path=${encodeURIComponent(`.gitown/wiki/${slug}.md`)}`
      : null,
    version,
  );
  useEffect(() => {
    setEditing(false);
    setShowHistory(false);
    setError("");
  }, [slug]);
  useEffect(() => {
    if (page.data && !editing) setContent(page.data.content);
  }, [page.data, editing]);

  if (index.loading) return <Loading />;
  const canEdit = repo.can_write && !repo.archived && !!index.data?.head_sha;
  return (
    <div className="wiki-layout">
      <aside className="panel wiki-sidebar">
        <h2>
          <BookOpenText size={18} /> Wiki
        </h2>
        <p className="muted small-text">
          Versioned Markdown pages on {repo.default_branch}.
        </p>
        <ErrorMessage error={index.error} />
        <form
          onSubmit={(event) => {
            event.preventDefault();
            setSubmitted(search.trim());
          }}
        >
          <label htmlFor="wiki-search">Search pages</label>
          <input
            id="wiki-search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search the wiki"
            maxLength={80}
          />
          <button className="button small-button" type="submit">
            Search
          </button>
        </form>
        <ErrorMessage error={found.error} />
        {found.data && (
          <ul>
            {found.data.items.map((item) => (
              <li key={`${item.slug}:${item.line}`}>
                <Link href={`${basePath}/${encodeURIComponent(item.slug)}`}>
                  {item.slug}:{item.line}
                </Link>{" "}
                {item.text}
              </li>
            ))}
            {found.data.items.length === 0 && <li>No matching pages.</li>}
          </ul>
        )}
        <nav aria-label="Wiki pages">
          {index.data?.pages.map((item) => (
            <Link
              href={`${basePath}/${encodeURIComponent(item)}`}
              className={item === slug ? "active" : ""}
              key={item}
            >
              {item.replaceAll("-", " ")}
            </Link>
          ))}
        </nav>
        {canEdit && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              const next = newSlug.trim().toLowerCase();
              if (!validSlug.test(next)) {
                setError(
                  "Use letters, numbers, and hyphens, up to 80 characters.",
                );
                return;
              }
              setNewSlug("");
              router.push(`${basePath}/${next}`);
            }}
          >
            <label htmlFor="wiki-new-slug">New page slug</label>
            <input
              id="wiki-new-slug"
              value={newSlug}
              onChange={(event) => setNewSlug(event.target.value)}
              placeholder="getting-started"
            />
            <button className="button small-button" type="submit">
              <Plus size={14} /> Add page
            </button>
          </form>
        )}
      </aside>
      <section className="panel wiki-content" aria-label="Wiki page">
        <div className="section-heading">
          <h2>{slug.replaceAll("-", " ")}</h2>
          {exists && (
            <button
              className="button small-button"
              type="button"
              onClick={() => setShowHistory((value) => !value)}
            >
              <History size={14} /> {showHistory ? "Hide history" : "History"}
            </button>
          )}
        </div>
        <ErrorMessage error={error || page.error} />
        {page.loading ? (
          <Loading />
        ) : editing ? (
          <form
            onSubmit={async (event) => {
              event.preventDefault();
              setBusy(true);
              setError("");
              try {
                await put(`${endpoint}/wiki/${encodeURIComponent(slug)}`, {
                  content,
                  expected_head: page.data?.head_sha || index.data?.head_sha,
                });
                setEditing(false);
                setVersion((value) => value + 1);
              } catch (saveError) {
                setError((saveError as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            <label htmlFor="wiki-content">Markdown</label>
            <textarea
              id="wiki-content"
              rows={18}
              maxLength={128 * 1024}
              value={content}
              onChange={(event) => setContent(event.target.value)}
            />
            <div className="form-actions">
              <button className="button primary" disabled={busy} type="submit">
                <Save size={15} /> Save wiki page
              </button>
              <button
                className="button"
                type="button"
                onClick={() => setEditing(false)}
              >
                Cancel
              </button>
            </div>
          </form>
        ) : exists && page.data ? (
          <>
            <SafeMarkdown text={page.data.content} />
            {canEdit && (
              <button
                className="button small-button"
                type="button"
                onClick={() => {
                  setContent(page.data!.content);
                  setEditing(true);
                }}
              >
                Edit page
              </button>
            )}
          </>
        ) : (
          <div className="empty-inline">
            <p>This wiki page does not exist yet.</p>
            {canEdit ? (
              <button
                className="button"
                type="button"
                onClick={() => {
                  setContent("");
                  setEditing(true);
                }}
              >
                Create this page
              </button>
            ) : (
              <p>Ask a repository writer to create it.</p>
            )}
          </div>
        )}
        {showHistory && (
          <div className="wiki-history">
            <h3>Page history</h3>
            {history.loading ? (
              <Loading />
            ) : history.data?.length ? (
              <ul>
                {history.data.map((commit) => (
                  <li key={commit.sha}>
                    <code>{commit.sha.slice(0, 8)}</code> {commit.message} ·{" "}
                    {date(commit.date)}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="muted">No history yet.</p>
            )}
          </div>
        )}
      </section>
    </div>
  );
}
