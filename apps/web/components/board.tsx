"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { ArrowDown, ArrowUp, GitPullRequest, Settings, X } from "lucide-react";
import {
  apiPage,
  post,
  put,
  remove,
  api,
  type BoardColumn,
  type BoardField,
  type BoardItem,
  type BoardSettings,
  type Pull,
} from "@/lib/api";
import { Pagination } from "./issues";
import { Badge, ErrorMessage, Loading, useData } from "./ui";

const boardPageSize = 100;

function itemPath(item: BoardItem) {
  return `/repos/${item.owner}/${item.repository}/${item.kind === "pull" ? "pulls" : "issues"}/${item.number}`;
}

function itemLabel(item: BoardItem) {
  return item.kind === "pull"
    ? `unite request #${item.number}`
    : `issue #${item.number}`;
}

function useBoardItems(path: string, page: number, version: number) {
  const [state, setState] = useState<{
    items: BoardItem[];
    total: number;
    columns?: BoardColumn[];
    loading: boolean;
    error?: string;
  }>({ items: [], total: 0, loading: true });
  const url = `${path}${path.includes("?") ? "&" : "?"}page=${page}&per_page=${boardPageSize}`;
  useEffect(() => {
    let current = true;
    const load = url.startsWith("/districts/")
      ? api<{ items: BoardItem[]; total: number; columns: BoardColumn[] }>(url)
      : apiPage<BoardItem>(url);
    load
      .then((result) => {
        if (current) setState({ ...result, loading: false });
      })
      .catch((error) => {
        if (current)
          setState((previous) => ({
            ...previous,
            error: (error as Error).message,
            loading: false,
          }));
      });
    return () => {
      current = false;
    };
  }, [url, version]);
  return state;
}

function FieldValue({
  field,
  item,
  editable,
  onSave,
}: {
  field: BoardField;
  item: BoardItem;
  editable: boolean;
  onSave: (value: string) => void;
}) {
  const value = item.fields[field.id] || "";
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const label = `${field.name} for ${itemLabel(item)}`;
  if (!editable) return <>{value || "—"}</>;
  if (field.kind === "single_select")
    return (
      <select
        aria-label={label}
        value={value}
        onChange={(event) => onSave(event.target.value)}
      >
        <option value="">—</option>
        {field.options.map((option) => (
          <option key={option}>{option}</option>
        ))}
      </select>
    );
  return (
    <input
      aria-label={label}
      type={
        field.kind === "number"
          ? "number"
          : field.kind === "date"
            ? "date"
            : "text"
      }
      value={draft}
      maxLength={200}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={() => {
        if (draft !== value) onSave(draft);
      }}
    />
  );
}

function monthKey(date: string) {
  return date.slice(0, 7);
}

function monthsBetween(first: string, last: string) {
  const months: string[] = [];
  let [year, month] = first.split("-").map(Number);
  const [endYear, endMonth] = last.split("-").map(Number);
  while (
    (year < endYear || (year === endYear && month <= endMonth)) &&
    months.length < 24
  ) {
    months.push(`${year}-${String(month).padStart(2, "0")}`);
    month += 1;
    if (month > 12) {
      month = 1;
      year += 1;
    }
  }
  return months;
}

function monthName(key: string) {
  const [year, month] = key.split("-").map(Number);
  return new Date(Date.UTC(year, month - 1, 1)).toLocaleDateString(undefined, {
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  });
}

/** A month-by-month timeline positioned by each item's due date. */
function Roadmap({ items }: { items: BoardItem[] }) {
  const dated = items
    .map((item) => ({ item, due: item.due_date || item.milestone_due }))
    .filter((entry): entry is { item: BoardItem; due: string } => !!entry.due)
    .sort((a, b) => a.due.localeCompare(b.due));
  const undated = items.filter((item) => !item.due_date && !item.milestone_due);
  const today = new Date().toISOString().slice(0, 10);
  const months = dated.length
    ? monthsBetween(
        monthKey(dated[0].due < today ? dated[0].due : today),
        monthKey(dated[dated.length - 1].due),
      )
    : [];
  const groups = Array.from(
    new Set(dated.map((entry) => entry.item.iteration || "")),
  );
  return (
    <div className="roadmap">
      {dated.length ? (
        <div className="panel roadmap-scroll">
          <div
            className="roadmap-grid"
            style={{
              gridTemplateColumns: `minmax(180px, 1.4fr) repeat(${months.length}, minmax(72px, 1fr))`,
            }}
            role="table"
            aria-label="Roadmap timeline"
          >
            <div className="roadmap-head" role="columnheader">
              Work item
            </div>
            {months.map((month) => (
              <div
                key={month}
                role="columnheader"
                className={`roadmap-head ${month === monthKey(today) ? "current" : ""}`}
              >
                {monthName(month)}
              </div>
            ))}
            {groups.map((group) => (
              <div
                className="roadmap-group"
                key={group || "none"}
                role="rowgroup"
              >
                <div
                  className="roadmap-iteration"
                  style={{ gridColumn: `1 / span ${months.length + 1}` }}
                >
                  {group || "No iteration"}
                </div>
                {dated
                  .filter((entry) => (entry.item.iteration || "") === group)
                  .map(({ item, due }) => {
                    const column = months.indexOf(monthKey(due));
                    return (
                      <div
                        className="roadmap-row"
                        role="row"
                        key={item.item_id}
                      >
                        <div className="roadmap-title" role="cell">
                          <Link href={itemPath(item)}>
                            {item.kind === "pull" && (
                              <GitPullRequest size={12} />
                            )}
                            #{item.number} {item.title}
                          </Link>
                        </div>
                        {months.map((month, index) => (
                          <div role="cell" className="roadmap-cell" key={month}>
                            {index === column && (
                              <span
                                className={`roadmap-marker ${item.state === "open" ? (due < today ? "late" : "") : "done"}`}
                                title={`Due ${due.slice(0, 10)}`}
                              >
                                {due.slice(5, 10)}
                              </span>
                            )}
                          </div>
                        ))}
                      </div>
                    );
                  })}
              </div>
            ))}
          </div>
        </div>
      ) : (
        <p className="muted">
          Set due dates or milestone due dates to place work on the timeline.
        </p>
      )}
      {!!undated.length && (
        <section
          className="panel roadmap-undated"
          aria-label="Unscheduled work"
        >
          <h3>Unscheduled</h3>
          <ul>
            {undated.map((item) => (
              <li key={item.item_id}>
                <Link href={itemPath(item)}>
                  #{item.number} {item.title}
                </Link>{" "}
                <span className="muted small-text">{item.iteration}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

function BoardSettingsPanel({
  endpoint,
  settings,
  onSaved,
}: {
  endpoint: string;
  settings: BoardSettings;
  onSaved: () => void;
}) {
  const [columns, setColumns] = useState<BoardColumn[]>(settings.columns);
  const [automation, setAutomation] = useState(settings.automation);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [fieldName, setFieldName] = useState("");
  const [fieldKind, setFieldKind] = useState<BoardField["kind"]>("text");
  const [fieldOptions, setFieldOptions] = useState("");
  async function run(action: () => Promise<unknown>, message: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await action();
      setNotice(message);
      onSaved();
    } catch (saveError) {
      setError((saveError as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const move = (index: number, offset: number) => {
    const next = [...columns];
    [next[index], next[index + offset]] = [next[index + offset], next[index]];
    setColumns(next);
  };
  const lastEditable = columns.length - 2;
  return (
    <section className="panel form-panel" aria-label="Board settings">
      <h3>Columns</h3>
      <p className="muted small-text">
        Done always stays last; moving an issue there closes it.
      </p>
      <ErrorMessage error={error} />
      {notice && <p className="green-text">{notice}</p>}
      {columns.map((column, index) => (
        <div className="board-column-setting" key={index}>
          <input
            aria-label={`Column ${index + 1} name`}
            value={column.name}
            maxLength={30}
            onChange={(event) =>
              setColumns(
                columns.map((item, i) =>
                  i === index ? { ...item, name: event.target.value } : item,
                ),
              )
            }
          />
          {column.key !== "done" && (
            <>
              <button
                type="button"
                aria-label={`Move column ${column.name} up`}
                disabled={index === 0}
                onClick={() => move(index, -1)}
              >
                <ArrowUp size={14} />
              </button>
              <button
                type="button"
                aria-label={`Move column ${column.name} down`}
                disabled={index >= lastEditable}
                onClick={() => move(index, 1)}
              >
                <ArrowDown size={14} />
              </button>
              <button
                type="button"
                aria-label={`Remove column ${column.name}`}
                disabled={columns.length <= 2}
                onClick={() =>
                  setColumns(columns.filter((_, i) => i !== index))
                }
              >
                <X size={14} />
              </button>
            </>
          )}
        </div>
      ))}
      <div className="form-actions">
        <button
          type="button"
          className="button small-button"
          disabled={columns.length >= 10}
          onClick={() => {
            const used = new Set(columns.map((column) => column.key));
            let n = columns.length;
            while (used.has(`column-${n}`)) n += 1;
            setColumns([
              ...columns.slice(0, -1),
              { key: `column-${n}`, name: "New column" },
              columns[columns.length - 1],
            ]);
          }}
        >
          Add column
        </button>
      </div>
      <label className="checkbox-row">
        <input
          type="checkbox"
          checked={automation}
          onChange={(event) => setAutomation(event.target.checked)}
        />
        Automatically add new unite requests and move them to Done when they are
        merged or closed
      </label>
      <button
        className="button primary small-button"
        disabled={busy}
        onClick={() =>
          run(
            () => put(`${endpoint}/board/settings`, { columns, automation }),
            "Board saved.",
          )
        }
      >
        Save board
      </button>
      <h3>Custom fields</h3>
      {settings.fields.length ? (
        <ul className="sub-issue-list">
          {settings.fields.map((field) => (
            <li key={field.id}>
              <strong>{field.name}</strong>{" "}
              <span className="muted small-text">
                {field.kind.replace("_", " ")}
                {field.options.length ? `: ${field.options.join(", ")}` : ""}
              </span>
              <button
                type="button"
                aria-label={`Delete field ${field.name}`}
                disabled={busy}
                onClick={() =>
                  run(
                    () => remove(`${endpoint}/board/fields/${field.id}`),
                    "Field deleted.",
                  )
                }
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted small-text">No custom fields yet.</p>
      )}
      <form
        className="form-grid"
        onSubmit={(event) => {
          event.preventDefault();
          void run(async () => {
            await post(`${endpoint}/board/fields`, {
              name: fieldName,
              kind: fieldKind,
              options:
                fieldKind === "single_select"
                  ? fieldOptions
                      .split(",")
                      .map((option) => option.trim())
                      .filter(Boolean)
                  : [],
            });
            setFieldName("");
            setFieldOptions("");
          }, "Field added.");
        }}
      >
        <label>
          Field name
          <input
            value={fieldName}
            maxLength={40}
            onChange={(event) => setFieldName(event.target.value)}
            required
          />
        </label>
        <label>
          Field type
          <select
            value={fieldKind}
            onChange={(event) =>
              setFieldKind(event.target.value as BoardField["kind"])
            }
          >
            <option value="text">Text</option>
            <option value="number">Number</option>
            <option value="date">Date</option>
            <option value="single_select">Single select</option>
          </select>
        </label>
        {fieldKind === "single_select" && (
          <label>
            Options, separated by commas
            <input
              value={fieldOptions}
              onChange={(event) => setFieldOptions(event.target.value)}
              required
            />
          </label>
        )}
        <button className="button small-button" disabled={busy}>
          Add field
        </button>
      </form>
    </section>
  );
}

function AddPullToBoard({
  endpoint,
  items,
  onAdded,
}: {
  endpoint: string;
  items: BoardItem[];
  onAdded: () => void;
}) {
  const pulls = useData<Pull[]>(`${endpoint}/pulls`);
  const [error, setError] = useState("");
  const onBoard = new Set(
    items.filter((item) => item.kind === "pull").map((item) => item.number),
  );
  const available = (pulls.data || []).filter(
    (pull) => pull.state === "open" && !onBoard.has(pull.number),
  );
  if (!available.length) return null;
  return (
    <label className="label-picker">
      Add unite request
      <ErrorMessage error={error} />
      <select
        value=""
        onChange={async (event) => {
          if (!event.target.value) return;
          setError("");
          try {
            await put(`${endpoint}/pulls/${event.target.value}/board`, {
              status: "",
            });
            onAdded();
          } catch (addError) {
            setError((addError as Error).message);
          }
        }}
      >
        <option value="">Choose a unite request…</option>
        {available.map((pull) => (
          <option key={pull.number} value={pull.number}>
            #{pull.number} {pull.title}
          </option>
        ))}
      </select>
    </label>
  );
}

export function BoardView({
  endpoint,
  district,
  canTriage,
  canConfigure = false,
}: {
  endpoint?: string;
  district?: string;
  canTriage: boolean;
  canConfigure?: boolean;
}) {
  const [version, setVersion] = useState(0);
  const [page, setPage] = useState(1);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [view, setView] = useState<"board" | "table" | "roadmap">("board");
  const [configuring, setConfiguring] = useState(false);
  const [dragOver, setDragOver] = useState("");
  const settings = useData<BoardSettings>(
    endpoint ? `${endpoint}/board/settings` : null,
    version,
  );
  const board = useBoardItems(
    district ? `/districts/${district}/board` : `${endpoint}/board`,
    page,
    version,
  );
  const columns = settings.data?.columns || board.columns || [];
  const fields = settings.data?.fields || [];
  const refresh = () => setVersion((value) => value + 1);
  const itemEndpoint = (item: BoardItem) =>
    `/repos/${item.owner}/${item.repository}`;
  async function move(item: BoardItem, status: string) {
    if (!canTriage || item.status === status) return;
    setBusy(item.item_id);
    setError("");
    try {
      await put(
        `${itemEndpoint(item)}/${item.kind === "pull" ? "pulls" : "issues"}/${item.number}/board`,
        { status },
      );
      refresh();
    } catch (saveError) {
      setError((saveError as Error).message);
    } finally {
      setBusy(null);
      setDragOver("");
    }
  }
  async function saveField(item: BoardItem, field: BoardField, value: string) {
    setError("");
    try {
      await put(`${itemEndpoint(item)}/board/values`, {
        kind: item.kind,
        number: item.number,
        field_id: field.id,
        value,
      });
      refresh();
    } catch (saveError) {
      setError((saveError as Error).message);
    }
  }
  const items = board.items;
  return (
    <>
      <div className="section-heading">
        <div>
          <h2>{district ? "District board" : "Issue board"}</h2>
          <p className="muted">
            Move work across the columns. Moving an issue to{" "}
            {columns[columns.length - 1]?.name || "Done"} closes it; moving it
            back reopens it.
          </p>
        </div>
        <div className="heading-actions">
          {(["board", "table", "roadmap"] as const).map((option) => (
            <button
              key={option}
              className={`button small-button ${view === option ? "primary" : ""}`}
              type="button"
              aria-pressed={view === option}
              onClick={() => setView(option)}
            >
              {option}
            </button>
          ))}
          {canConfigure && endpoint && (
            <button
              className="button small-button"
              type="button"
              aria-expanded={configuring}
              onClick={() => setConfiguring(!configuring)}
            >
              <Settings size={14} /> Configure
            </button>
          )}
          {endpoint && (
            <Link className="button" href={`${endpoint}/issues`}>
              View issues
            </Link>
          )}
        </div>
      </div>
      <ErrorMessage error={error || board.error || settings.error} />
      {configuring && settings.data && endpoint && (
        <BoardSettingsPanel
          key={version}
          endpoint={endpoint}
          settings={settings.data}
          onSaved={refresh}
        />
      )}
      {canTriage && endpoint && (
        <AddPullToBoard endpoint={endpoint} items={items} onAdded={refresh} />
      )}
      {board.loading && !items.length ? (
        <Loading />
      ) : view === "table" ? (
        <div className="table-scroll">
          <table className="panel data-table">
            <thead>
              <tr>
                <th>Item</th>
                {district && <th>Repository</th>}
                <th>Status</th>
                <th>Priority</th>
                <th>Estimate</th>
                <th>Iteration</th>
                <th>Due</th>
                {fields.map((field) => (
                  <th key={field.id}>{field.name}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.item_id}>
                  <td>
                    <Link href={itemPath(item)}>
                      {item.kind === "pull" && <GitPullRequest size={12} />}#
                      {item.number} {item.title}
                    </Link>
                  </td>
                  {district && (
                    <td>
                      {item.owner}/{item.repository}
                    </td>
                  )}
                  <td>
                    {columns.find((column) => column.key === item.status)
                      ?.name || item.status}
                  </td>
                  <td>{item.priority}</td>
                  <td>{item.estimate ?? "—"}</td>
                  <td>{item.iteration || "—"}</td>
                  <td>
                    {item.due_date?.slice(0, 10) ||
                      item.milestone_due?.slice(0, 10) ||
                      "—"}
                  </td>
                  {fields.map((field) => (
                    <td key={field.id}>
                      <FieldValue
                        field={field}
                        item={item}
                        editable={canTriage}
                        onSave={(value) => void saveField(item, field, value)}
                      />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : view === "roadmap" ? (
        <Roadmap items={items} />
      ) : (
        <div className="issue-board">
          {columns.map((column) => {
            const inColumn = items.filter((item) => item.status === column.key);
            return (
              <section
                className={`board-column panel ${dragOver === column.key ? "drop-target" : ""}`}
                key={column.key}
                aria-label={`${column.name} column`}
                onDragOver={(event) => {
                  if (!canTriage) return;
                  event.preventDefault();
                  setDragOver(column.key);
                }}
                onDragLeave={() => setDragOver("")}
                onDrop={(event) => {
                  const id = event.dataTransfer.getData("text/plain");
                  const item = items.find((entry) => entry.item_id === id);
                  if (item) void move(item, column.key);
                }}
              >
                <h3>
                  {column.name} <Badge>{inColumn.length}</Badge>
                </h3>
                {inColumn.length ? (
                  inColumn.map((item) => (
                    <article
                      className="board-card"
                      key={item.item_id}
                      draggable={canTriage}
                      onDragStart={(event) =>
                        event.dataTransfer.setData("text/plain", item.item_id)
                      }
                    >
                      <strong>
                        <Link href={itemPath(item)}>
                          {item.kind === "pull" && (
                            <GitPullRequest
                              size={13}
                              aria-label="Unite request"
                            />
                          )}{" "}
                          {item.title}
                        </Link>
                      </strong>
                      <p>
                        {district && `${item.repository} `}#{item.number} ·{" "}
                        {item.author}
                        {item.priority !== "none" && ` · ${item.priority}`}
                        {item.estimate != null && ` · ${item.estimate} pts`}
                        {item.due_date
                          ? ` · due ${item.due_date.slice(0, 10)}`
                          : ""}
                        {item.milestone ? ` · ${item.milestone}` : ""}
                      </p>
                      {fields
                        .filter((field) => item.fields[field.id])
                        .map((field) => (
                          <Badge key={field.id}>
                            {field.name}: {item.fields[field.id]}
                          </Badge>
                        ))}
                      {canTriage && (
                        <label>
                          Status for {itemLabel(item)}
                          <select
                            disabled={busy === item.item_id}
                            value={item.status}
                            onChange={(event) =>
                              void move(item, event.target.value)
                            }
                          >
                            {columns.map((option) => (
                              <option key={option.key} value={option.key}>
                                {option.name}
                              </option>
                            ))}
                          </select>
                        </label>
                      )}
                      {canTriage && item.kind === "pull" && (
                        <button
                          type="button"
                          className="text-button"
                          onClick={async () => {
                            setError("");
                            try {
                              await remove(
                                `${itemEndpoint(item)}/pulls/${item.number}/board`,
                              );
                              refresh();
                            } catch (removeError) {
                              setError((removeError as Error).message);
                            }
                          }}
                        >
                          Remove from board
                        </button>
                      )}
                    </article>
                  ))
                ) : (
                  <p className="muted small-text">Nothing here.</p>
                )}
              </section>
            );
          })}
        </div>
      )}
      <Pagination
        page={page}
        total={board.total}
        perPage={boardPageSize}
        onPage={setPage}
      />
    </>
  );
}
