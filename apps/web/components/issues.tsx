"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { Fragment, useEffect, useState } from "react";
import { Bookmark, Check, CircleDot, History, X } from "lucide-react";
import {
  apiPage,
  date,
  patch,
  post,
  put,
  remove,
  type Issue,
  type IssueFormField,
  type IssueReferences,
  type Label,
  type Milestone,
  type Repo,
  type SavedSearch,
  type SubIssues,
} from "@/lib/api";
import { Badge, ErrorMessage, Loading, useData } from "./ui";

export type IssueFilter = {
  state: "open" | "closed";
  labels: string[];
  assignee: string;
  milestone: string;
  author: string;
  q: string;
  sort: "" | "oldest" | "updated";
};

export const emptyIssueFilter: IssueFilter = {
  state: "open",
  labels: [],
  assignee: "",
  milestone: "",
  author: "",
  q: "",
  sort: "",
};

export const issuesPerPage = 25;

export function issueQuery(filter: IssueFilter) {
  const params = new URLSearchParams({ state: filter.state });
  for (const label of filter.labels) params.append("label", label);
  if (filter.assignee) params.set("assignee", filter.assignee);
  if (filter.milestone) params.set("milestone", filter.milestone);
  if (filter.author) params.set("author", filter.author);
  if (filter.q) params.set("q", filter.q);
  if (filter.sort) params.set("sort", filter.sort);
  return params;
}

function filterFromQuery(query: string): IssueFilter {
  const params = new URLSearchParams(query);
  const state = params.get("state") === "closed" ? "closed" : "open";
  const sort = params.get("sort");
  return {
    state,
    labels: params.getAll("label"),
    assignee: params.get("assignee") || "",
    milestone: params.get("milestone") || "",
    author: params.get("author") || "",
    q: params.get("q") || "",
    sort: sort === "oldest" || sort === "updated" ? sort : "",
  };
}

/** Loads one page of issues and the open/closed totals for the filter. */
export function usePagedIssues(
  endpoint: string,
  filter: IssueFilter,
  page: number,
  version: number,
) {
  const params = issueQuery(filter);
  params.set("page", String(page));
  params.set("per_page", String(issuesPerPage));
  const path = `${endpoint}/issues?${params}`;
  const [state, setState] = useState<{
    path?: string;
    items: Issue[];
    total: number;
    counts: Record<string, number>;
    loading: boolean;
    error?: string;
  }>({ items: [], total: 0, counts: {}, loading: true });
  useEffect(() => {
    let current = true;
    setState((previous) =>
      previous.path === path ? previous : { ...previous, loading: true },
    );
    apiPage<Issue>(path)
      .then((result) => {
        if (current) setState({ path, ...result, loading: false });
      })
      .catch((error) => {
        if (current)
          setState((previous) => ({
            ...previous,
            path,
            error: (error as Error).message,
            loading: false,
          }));
      });
    return () => {
      current = false;
    };
  }, [path, version]);
  return state;
}

const referencePattern =
  /(^|[^A-Za-z0-9_\/#.-])(?:([a-z0-9][a-z0-9-]*)\/([a-z0-9][a-z0-9._-]*))?#([1-9][0-9]{0,8})\b|(^|[^A-Za-z0-9_.-])@([a-z0-9][a-z0-9-]{0,38})\b/gi;

/**
 * Renders plain text with #42, owner/repo#42, and @user turned into links.
 * The text is never interpreted as HTML.
 */
export function LinkedText({
  text,
  owner,
  repository,
}: {
  text: string;
  owner: string;
  repository: string;
}) {
  const parts: React.ReactNode[] = [];
  let last = 0;
  for (const match of text.matchAll(referencePattern)) {
    const index = match.index ?? 0;
    const prefix = match[1] ?? match[5] ?? "";
    const start = index + prefix.length;
    parts.push(text.slice(last, start));
    if (match[4]) {
      const targetOwner = (match[2] || owner).toLowerCase();
      const targetRepository = (match[3] || repository).toLowerCase();
      parts.push(
        <Link
          key={start}
          href={`/repos/${targetOwner}/${targetRepository}/issues/${match[4]}`}
        >
          {match[0].slice(prefix.length)}
        </Link>,
      );
    } else {
      parts.push(
        <Link key={start} href={`/u/${match[6].toLowerCase()}`}>
          @{match[6]}
        </Link>,
      );
    }
    last = index + match[0].length;
  }
  parts.push(text.slice(last));
  return <span className="linked-text">{parts}</span>;
}

function IssueSavedSearches({
  repository,
  filter,
  onApply,
}: {
  repository: string;
  filter: IssueFilter;
  onApply: (filter: IssueFilter) => void;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState("");
  const saved = useData<SavedSearch[]>("/user/saved-searches", version);
  const scope = `repo=${repository}&`;
  const mine = (saved.data || []).filter((item) =>
    item.query.startsWith(scope),
  );
  if (saved.error) return null;
  return (
    <div className="saved-searches" aria-label="Saved searches">
      <ErrorMessage error={error} />
      {mine.map((item) => (
        <span className="label-catalog-item" key={item.id}>
          <button
            type="button"
            className="text-button"
            onClick={() =>
              onApply(filterFromQuery(item.query.slice(scope.length)))
            }
          >
            <Bookmark size={13} /> {item.name}
          </button>
          <button
            type="button"
            aria-label={`Delete saved search ${item.name}`}
            onClick={async () => {
              setError("");
              try {
                await remove(`/user/saved-searches/${item.id}`);
                setVersion((value) => value + 1);
              } catch (deleteError) {
                setError((deleteError as Error).message);
              }
            }}
          >
            ×
          </button>
        </span>
      ))}
      {naming ? (
        <form
          className="inline-save"
          onSubmit={async (event) => {
            event.preventDefault();
            setError("");
            try {
              await post("/user/saved-searches", {
                name,
                query: scope + issueQuery(filter).toString(),
              });
              setNaming(false);
              setName("");
              setVersion((value) => value + 1);
            } catch (saveError) {
              setError((saveError as Error).message);
            }
          }}
        >
          <input
            aria-label="Saved search name"
            value={name}
            maxLength={80}
            onChange={(event) => setName(event.target.value)}
            required
            autoFocus
          />
          <button className="button small-button">Save</button>
          <button
            type="button"
            className="text-button"
            onClick={() => setNaming(false)}
          >
            Cancel
          </button>
        </form>
      ) : (
        <button
          type="button"
          className="text-button"
          onClick={() => setNaming(true)}
        >
          Save this search
        </button>
      )}
    </div>
  );
}

export function IssueFilterBar({
  repository,
  filter,
  labels,
  milestones,
  signedIn,
  onChange,
}: {
  repository: string;
  filter: IssueFilter;
  labels: Label[];
  milestones: Milestone[];
  signedIn: boolean;
  onChange: (filter: IssueFilter) => void;
}) {
  const [text, setText] = useState(filter.q);
  useEffect(() => setText(filter.q), [filter.q]);
  const available = labels.filter(
    (label) => !filter.labels.includes(label.name.toLowerCase()),
  );
  return (
    <div className="issue-filters">
      <div className="filter-row">
        <form
          className="filter-search"
          onSubmit={(event) => {
            event.preventDefault();
            onChange({ ...filter, q: text.trim() });
          }}
        >
          <input
            aria-label="Search issues"
            placeholder="Search issues"
            value={text}
            maxLength={100}
            onChange={(event) => setText(event.target.value)}
            onBlur={() => {
              if (text.trim() !== filter.q)
                onChange({ ...filter, q: text.trim() });
            }}
          />
        </form>
        <select
          aria-label="Filter by label"
          value=""
          disabled={filter.labels.length >= 5 || !available.length}
          onChange={(event) => {
            if (event.target.value)
              onChange({
                ...filter,
                labels: [...filter.labels, event.target.value],
              });
          }}
        >
          <option value="">
            {filter.labels.length ? "Add label" : "All labels"}
          </option>
          {available.map((label) => (
            <option key={label.id} value={label.name.toLowerCase()}>
              {label.name}
            </option>
          ))}
        </select>
        <select
          aria-label="Filter by milestone"
          value={filter.milestone}
          onChange={(event) =>
            onChange({ ...filter, milestone: event.target.value })
          }
        >
          <option value="">All milestones</option>
          <option value="none">No milestone</option>
          {milestones.map((milestone) => (
            <option key={milestone.id} value={milestone.title}>
              {milestone.title}
            </option>
          ))}
        </select>
        <input
          aria-label="Filter by assignee"
          placeholder="Assignee"
          value={filter.assignee}
          maxLength={39}
          onChange={(event) =>
            onChange({
              ...filter,
              assignee: event.target.value.trim().toLowerCase(),
            })
          }
        />
        <input
          aria-label="Filter by author"
          placeholder="Author"
          value={filter.author}
          maxLength={39}
          onChange={(event) =>
            onChange({
              ...filter,
              author: event.target.value.trim().toLowerCase(),
            })
          }
        />
        <select
          aria-label="Sort issues"
          value={filter.sort}
          onChange={(event) =>
            onChange({
              ...filter,
              sort: event.target.value as IssueFilter["sort"],
            })
          }
        >
          <option value="">Newest</option>
          <option value="oldest">Oldest</option>
          <option value="updated">Recently updated</option>
        </select>
      </div>
      {(filter.labels.length > 0 ||
        filter.assignee ||
        filter.milestone ||
        filter.author ||
        filter.q) && (
        <div className="assigned-labels active-filters">
          {filter.labels.map((name) => (
            <span className="assigned-label" key={name}>
              <Badge>{name}</Badge>
              <button
                aria-label={`Remove label filter ${name}`}
                onClick={() =>
                  onChange({
                    ...filter,
                    labels: filter.labels.filter((item) => item !== name),
                  })
                }
              >
                ×
              </button>
            </span>
          ))}
          <button
            type="button"
            className="text-button"
            onClick={() =>
              onChange({ ...emptyIssueFilter, state: filter.state })
            }
          >
            <X size={13} /> Clear filters
          </button>
        </div>
      )}
      {signedIn && (
        <IssueSavedSearches
          repository={repository}
          filter={filter}
          onApply={onChange}
        />
      )}
    </div>
  );
}

export function Pagination({
  page,
  total,
  perPage,
  onPage,
}: {
  page: number;
  total: number;
  perPage: number;
  onPage: (page: number) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / perPage));
  if (pages <= 1) return null;
  return (
    <nav className="pagination" aria-label="Pagination">
      <button
        className="button small-button"
        disabled={page <= 1}
        onClick={() => onPage(page - 1)}
      >
        Previous
      </button>
      <span className="muted small-text">
        Page {page} of {pages}
      </span>
      <button
        className="button small-button"
        disabled={page >= pages}
        onClick={() => onPage(page + 1)}
      >
        Next
      </button>
    </nav>
  );
}

/** Inputs for a structured issue form. Values are keyed by field ID. */
export function IssueFormFields({
  fields,
  values,
  onChange,
}: {
  fields: IssueFormField[];
  values: Record<string, string>;
  onChange: (values: Record<string, string>) => void;
}) {
  const set = (id: string, value: string) =>
    onChange({ ...values, [id]: value });
  return (
    <>
      {fields.map((field) => {
        const label = `${field.label}${field.required ? " (required)" : ""}`;
        if (field.type === "checkbox")
          return (
            <label className="checkbox-row" key={field.id}>
              <input
                type="checkbox"
                checked={values[field.id] === "true"}
                required={field.required}
                onChange={(event) =>
                  set(field.id, event.target.checked ? "true" : "")
                }
              />
              {label}
            </label>
          );
        return (
          <label key={field.id}>
            {label}
            {field.type === "dropdown" ? (
              <select
                value={values[field.id] || ""}
                required={field.required}
                onChange={(event) => set(field.id, event.target.value)}
              >
                <option value="">Choose…</option>
                {(field.options || []).map((option) => (
                  <option key={option}>{option}</option>
                ))}
              </select>
            ) : field.type === "textarea" ? (
              <textarea
                rows={4}
                maxLength={8000}
                value={values[field.id] || ""}
                required={field.required}
                onChange={(event) => set(field.id, event.target.value)}
              />
            ) : (
              <input
                maxLength={300}
                value={values[field.id] || ""}
                required={field.required}
                onChange={(event) => set(field.id, event.target.value)}
              />
            )}
          </label>
        );
      })}
    </>
  );
}

/** Builds the payload's form values, dropping empty answers. */
export function formValues(
  fields: IssueFormField[],
  values: Record<string, string>,
) {
  const result: Record<string, string> = {};
  for (const field of fields) {
    const value = (values[field.id] || "").trim();
    if (value) result[field.id] = value;
  }
  return result;
}

export function SubIssuesPanel({
  endpoint,
  repository,
  number,
  canTriage,
  onChange,
}: {
  endpoint: string;
  repository: { owner: string; name: string };
  number: number;
  canTriage: boolean;
  onChange: () => void;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [title, setTitle] = useState("");
  const [existing, setExisting] = useState("");
  const [busy, setBusy] = useState(false);
  const path = `${endpoint}/issues/${number}/sub-issues`;
  const subs = useData<SubIssues>(path, version);
  const refresh = () => {
    setVersion((value) => value + 1);
    onChange();
  };
  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await action();
      refresh();
    } catch (saveError) {
      setError((saveError as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const base = `/repos/${repository.owner}/${repository.name}/issues`;
  return (
    <section className="issue-labels" aria-label="Sub-issues">
      <h4>Sub-issues</h4>
      <ErrorMessage error={error || subs.error} />
      {subs.data?.parent && (
        <p className="small-text">
          Part of{" "}
          <Link href={`${base}/${subs.data.parent.number}`}>
            #{subs.data.parent.number} {subs.data.parent.title}
          </Link>
          {canTriage && (
            <button
              type="button"
              className="text-button"
              disabled={busy}
              onClick={() =>
                run(() =>
                  put(`${endpoint}/issues/${number}/parent`, { parent: null }),
                )
              }
            >
              Detach
            </button>
          )}
        </p>
      )}
      {subs.data && subs.data.total > 0 && (
        <>
          <p className="small-text">
            {subs.data.closed} of {subs.data.total} closed
          </p>
          <progress
            className="sub-issue-progress"
            max={subs.data.total}
            value={subs.data.closed}
            aria-label={`${subs.data.closed} of ${subs.data.total} sub-issues closed`}
          />
          <ul className="sub-issue-list">
            {subs.data.children.map((child) => (
              <li key={child.number}>
                {child.state === "closed" ? (
                  <Check size={14} className="muted" />
                ) : (
                  <CircleDot size={14} className="green-text" />
                )}
                <Link href={`${base}/${child.number}`}>
                  #{child.number} {child.title}
                </Link>
                {canTriage && (
                  <button
                    type="button"
                    aria-label={`Remove sub-issue #${child.number}`}
                    disabled={busy}
                    onClick={() =>
                      run(() =>
                        put(`${endpoint}/issues/${child.number}/parent`, {
                          parent: null,
                        }),
                      )
                    }
                  >
                    ×
                  </button>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
      {subs.data && !subs.data.total && !subs.data.parent && (
        <p className="muted small-text">No sub-issues.</p>
      )}
      {canTriage && (
        <>
          <form
            className="inline-save"
            onSubmit={(event) => {
              event.preventDefault();
              void run(async () => {
                await post(`${endpoint}/issues`, { title, parent: number });
                setTitle("");
              });
            }}
          >
            <input
              aria-label={`New sub-issue title for #${number}`}
              placeholder="Create a sub-issue"
              value={title}
              maxLength={200}
              onChange={(event) => setTitle(event.target.value)}
              required
            />
            <button className="button small-button" disabled={busy}>
              Add
            </button>
          </form>
          <form
            className="inline-save"
            onSubmit={(event) => {
              event.preventDefault();
              void run(async () => {
                await put(`${endpoint}/issues/${Number(existing)}/parent`, {
                  parent: number,
                });
                setExisting("");
              });
            }}
          >
            <input
              aria-label={`Existing issue number to nest under #${number}`}
              placeholder="Existing issue #"
              type="number"
              min="1"
              value={existing}
              onChange={(event) => setExisting(event.target.value)}
              required
            />
            <button className="button small-button" disabled={busy}>
              Nest
            </button>
          </form>
        </>
      )}
    </section>
  );
}

export function IssueReferencesPanel({
  endpoint,
  number,
  version,
}: {
  endpoint: string;
  number: number;
  version: number;
}) {
  const refs = useData<IssueReferences>(
    `${endpoint}/issues/${number}/references`,
    version,
  );
  if (!refs.data) return null;
  const { mentions, referenced_by: referencedBy } = refs.data;
  if (!mentions.length && !referencedBy.length) return null;
  const link = (item: IssueReferences["mentions"][number]) => (
    <li key={`${item.kind}-${item.owner}-${item.repository}-${item.number}`}>
      <Link
        href={`/repos/${item.owner}/${item.repository}/${item.kind === "pull" ? "pulls" : "issues"}/${item.number}`}
      >
        {item.kind === "pull" ? "Unite request " : ""}
        {item.owner}/{item.repository}#{item.number} {item.title}
      </Link>{" "}
      <Badge kind={item.state === "open" ? "green" : ""}>{item.state}</Badge>
    </li>
  );
  return (
    <section className="issue-labels" aria-label="References">
      {!!referencedBy.length && (
        <>
          <h4>Referenced by</h4>
          <ul className="sub-issue-list">{referencedBy.map(link)}</ul>
        </>
      )}
      {!!mentions.length && (
        <>
          <h4>Mentions</h4>
          <ul className="sub-issue-list">{mentions.map(link)}</ul>
        </>
      )}
    </section>
  );
}

export function IssueTransfer({
  endpoint,
  repository,
  number,
}: {
  endpoint: string;
  repository: { owner: string; name: string };
  number: number;
}) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [target, setTarget] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const repos = useData<Repo[]>(open ? "/repos?mine=true" : null);
  const choices = (repos.data || []).filter(
    (repo) =>
      repo.can_triage &&
      !repo.archived &&
      !(repo.owner === repository.owner && repo.name === repository.name),
  );
  if (!open)
    return (
      <button
        type="button"
        className="button small-button"
        onClick={() => setOpen(true)}
      >
        Transfer issue
      </button>
    );
  return (
    <form
      className="issue-labels"
      onSubmit={async (event) => {
        event.preventDefault();
        setBusy(true);
        setError("");
        try {
          const moved = await post<{
            owner: string;
            repository: string;
            number: number;
          }>(`${endpoint}/issues/${number}/transfer`, { repository: target });
          router.push(
            `/repos/${moved.owner}/${moved.repository}/issues/${moved.number}`,
          );
        } catch (transferError) {
          setError((transferError as Error).message);
          setBusy(false);
        }
      }}
    >
      <h4>Transfer issue</h4>
      <p className="muted small-text">
        Labels, milestone, board status, and links stay behind. Comments,
        assignees who can access the destination, and the discussion move with
        the issue.
      </p>
      <ErrorMessage error={error || repos.error} />
      {repos.loading ? (
        <Loading />
      ) : (
        <label>
          Destination repository
          <select
            value={target}
            onChange={(event) => setTarget(event.target.value)}
            required
          >
            <option value="">Choose a repository…</option>
            {choices.map((repo) => (
              <option
                key={repo.id}
                value={`${repo.owner}/${repo.name}`}
              >{`${repo.owner}/${repo.name}`}</option>
            ))}
          </select>
        </label>
      )}
      <div className="form-actions">
        <button
          type="button"
          className="button small-button"
          onClick={() => setOpen(false)}
        >
          Cancel
        </button>
        <button
          className="button primary small-button"
          disabled={busy || !target}
        >
          {busy ? "Transferring…" : "Transfer"}
        </button>
      </div>
    </form>
  );
}

export function CommentHistory({ path }: { path: string }) {
  const [open, setOpen] = useState(false);
  const history = useData<{ body: string; edited_at: string }[]>(
    open ? `${path}/history` : null,
  );
  return (
    <>
      <button
        type="button"
        className="text-button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <History size={12} /> edited
      </button>
      {open && (
        <div className="comment-history" aria-label="Edit history">
          <ErrorMessage error={history.error} />
          {history.loading ? (
            <Loading />
          ) : (
            history.data?.map((revision, index) => (
              <Fragment key={index}>
                <p className="muted small-text">
                  Before edit on {date(revision.edited_at)}
                </p>
                <blockquote>{revision.body}</blockquote>
              </Fragment>
            ))
          )}
        </div>
      )}
    </>
  );
}

/** Edits an issue's title and description in place. */
export function IssueEditor({
  endpoint,
  issue,
  onSaved,
}: {
  endpoint: string;
  issue: Issue;
  onSaved: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState(issue.title);
  const [body, setBody] = useState(issue.body);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  if (!open)
    return (
      <button
        type="button"
        className="text-button"
        onClick={() => {
          setTitle(issue.title);
          setBody(issue.body);
          setOpen(true);
        }}
      >
        Edit issue
      </button>
    );
  return (
    <form
      className="panel form-panel"
      onSubmit={async (event) => {
        event.preventDefault();
        setBusy(true);
        setError("");
        try {
          await patch(`${endpoint}/issues/${issue.number}`, { title, body });
          setOpen(false);
          onSaved();
        } catch (saveError) {
          setError((saveError as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <ErrorMessage error={error} />
      <label>
        Issue title
        <input
          value={title}
          maxLength={200}
          onChange={(event) => setTitle(event.target.value)}
          required
        />
      </label>
      <label>
        Issue description
        <textarea
          value={body}
          rows={6}
          maxLength={20000}
          onChange={(event) => setBody(event.target.value)}
        />
      </label>
      <div className="form-actions">
        <button
          type="button"
          className="button small-button"
          onClick={() => setOpen(false)}
        >
          Cancel
        </button>
        <button className="button primary small-button" disabled={busy}>
          {busy ? "Saving…" : "Save issue"}
        </button>
      </div>
    </form>
  );
}

function fieldID(label: string, position: number) {
  const id = label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .replace(/^([^a-z])/, "f_$1")
    .slice(0, 40);
  return id || `field_${position + 1}`;
}

/** Edits the structured fields of an issue form template. */
export function TemplateFieldsEditor({
  index,
  fields,
  disabled,
  onChange,
}: {
  index: number;
  fields: IssueFormField[];
  disabled: boolean;
  onChange: (fields: IssueFormField[]) => void;
}) {
  const update = (position: number, change: Partial<IssueFormField>) =>
    onChange(
      fields.map((field, i) =>
        i === position ? { ...field, ...change } : field,
      ),
    );
  return (
    <fieldset className="template-fields" disabled={disabled}>
      <legend>Form fields for template {index + 1}</legend>
      {!fields.length && (
        <p className="muted small-text">
          No form fields. People fill in the suggested description instead.
        </p>
      )}
      {fields.map((field, position) => (
        <div className="template-field" key={position}>
          <input
            aria-label={`Template ${index + 1} field ${position + 1} label`}
            placeholder="Question"
            value={field.label}
            maxLength={100}
            onChange={(event) => {
              const label = event.target.value;
              // Keep an ID once a saved form uses it so existing answers
              // stay mapped; generate one from the label for new fields.
              const automatic =
                field.id.startsWith("field_") ||
                field.id === fieldID(field.label, position);
              update(position, {
                label,
                id: automatic ? fieldID(label, position) : field.id,
              });
            }}
          />
          <select
            aria-label={`Template ${index + 1} field ${position + 1} type`}
            value={field.type}
            onChange={(event) =>
              update(position, {
                type: event.target.value as IssueFormField["type"],
                options:
                  event.target.value === "dropdown"
                    ? field.options?.length
                      ? field.options
                      : ["Option 1"]
                    : [],
              })
            }
          >
            <option value="text">Short answer</option>
            <option value="textarea">Long answer</option>
            <option value="dropdown">Dropdown</option>
            <option value="checkbox">Checkbox</option>
          </select>
          {field.type === "dropdown" && (
            <input
              aria-label={`Template ${index + 1} field ${position + 1} options`}
              placeholder="Options, separated by commas"
              value={(field.options || []).join(", ")}
              onChange={(event) =>
                update(position, {
                  options: event.target.value
                    .split(",")
                    .map((option) => option.trim())
                    .filter(Boolean),
                })
              }
            />
          )}
          <label className="checkbox-row">
            <input
              type="checkbox"
              checked={!!field.required}
              onChange={(event) =>
                update(position, { required: event.target.checked })
              }
            />
            Required
          </label>
          <button
            type="button"
            aria-label={`Remove field ${position + 1} from template ${index + 1}`}
            onClick={() => onChange(fields.filter((_, i) => i !== position))}
          >
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        className="text-button"
        disabled={fields.length >= 20}
        onClick={() =>
          onChange([
            ...fields,
            {
              id: `field_${fields.length + 1}`,
              label: "",
              type: "textarea",
              required: false,
              options: [],
            },
          ])
        }
      >
        Add form field
      </button>
    </fieldset>
  );
}
