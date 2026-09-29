"use client";

import Link from "next/link";
import { useState } from "react";
import { put, type BoardItem } from "@/lib/api";
import { Badge, ErrorMessage, Loading, useData } from "./ui";

const columns: { status: BoardItem["status"]; title: string }[] = [
  { status: "todo", title: "To do" },
  { status: "progress", title: "In progress" },
  { status: "done", title: "Done" },
];

export function BoardView({
  endpoint,
  canTriage,
}: {
  endpoint: string;
  canTriage: boolean;
}) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<number | null>(null);
  const [view, setView] = useState<"board" | "table" | "roadmap">("board");
  const [dragOver, setDragOver] = useState<BoardItem["status"] | "">("");
  const board = useData<BoardItem[]>(`${endpoint}/board`, version);
  async function move(item: BoardItem, status: BoardItem["status"]) {
    if (!canTriage || item.status === status) return;
    setBusy(item.number);
    setError("");
    try {
      await put(`${endpoint}/issues/${item.number}/board`, { status });
      setVersion((value) => value + 1);
    } catch (saveError) {
      setError((saveError as Error).message);
    } finally {
      setBusy(null);
      setDragOver("");
    }
  }
  const items = board.data || [];
  return (
    <>
      <div className="section-heading">
        <div>
          <h2>Issue board</h2>
          <p className="muted">
            Move work from To do to Done. Moving an issue to Done closes it;
            moving it back reopens it.
          </p>
        </div>
        <div className="heading-actions">
          {(["board", "table", "roadmap"] as const).map((option) => (
            <button
              key={option}
              className={`button small-button ${view === option ? "primary" : ""}`}
              type="button"
              onClick={() => setView(option)}
            >
              {option}
            </button>
          ))}
          <Link className="button" href={`${endpoint}/issues`}>
            View issues
          </Link>
        </div>
      </div>
      <ErrorMessage error={error || board.error} />
      {board.loading ? (
        <Loading />
      ) : view === "table" ? (
        <table className="panel data-table">
          <thead>
            <tr>
              <th>Issue</th>
              <th>Status</th>
              <th>Priority</th>
              <th>Iteration</th>
              <th>Due</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <tr key={item.issue_id}>
                <td>
                  <Link href={`${endpoint}/issues`}>
                    #{item.number} {item.title}
                  </Link>
                </td>
                <td>{item.status}</td>
                <td>{item.priority}</td>
                <td>{item.iteration || "—"}</td>
                <td>{item.due_date?.slice(0, 10) || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : view === "roadmap" ? (
        <div className="issue-board">
          {Array.from(new Set(items.map((item) => item.iteration || "No iteration"))).map(
            (iteration) => (
              <section className="board-column panel" key={iteration}>
                <h3>{iteration}</h3>
                {items
                  .filter((item) => (item.iteration || "No iteration") === iteration)
                  .map((item) => (
                    <article className="board-card" key={item.issue_id}>
                      <strong>
                        #{item.number} {item.title}
                      </strong>
                      <p>
                        {item.priority}
                        {item.due_date ? ` · due ${item.due_date.slice(0, 10)}` : ""}
                      </p>
                    </article>
                  ))}
              </section>
            ),
          )}
        </div>
      ) : (
        <div className="issue-board">
          {columns.map((column) => {
            const items =
              board.data?.filter((item) => item.status === column.status) || [];
            return (
              <section
                className={`board-column panel ${dragOver === column.status ? "drop-target" : ""}`}
                key={column.status}
                aria-label={`${column.title} column`}
                onDragOver={(event) => {
                  if (!canTriage) return;
                  event.preventDefault();
                  setDragOver(column.status);
                }}
                onDrop={(event) => {
                  const number = Number(event.dataTransfer.getData("text/plain"));
                  const item = (board.data || []).find(
                    (entry) => entry.number === number,
                  );
                  if (item) void move(item, column.status);
                }}
              >
                <h3>
                  {column.title} <Badge>{items.length}</Badge>
                </h3>
                {items.length ? (
                  items.map((item) => (
                    <article
                      className="board-card"
                      key={item.issue_id}
                      draggable={canTriage}
                      onDragStart={(event) =>
                        event.dataTransfer.setData("text/plain", String(item.number))
                      }
                    >
                      <strong>{item.title}</strong>
                      <p>
                        #{item.number} · {item.author} · {item.priority}
                        {item.due_date ? ` · due ${item.due_date.slice(0, 10)}` : ""}
                      </p>
                      {canTriage && (
                        <label>
                          Status for issue #{item.number}
                          <select
                            disabled={busy === item.number}
                            value={item.status}
                            onChange={(event) =>
                              void move(
                                item,
                                event.target.value as BoardItem["status"],
                              )
                            }
                          >
                            {columns.map((option) => (
                              <option key={option.status} value={option.status}>
                                {option.title}
                              </option>
                            ))}
                          </select>
                        </label>
                      )}
                    </article>
                  ))
                ) : (
                  <p className="muted small-text">No issues here.</p>
                )}
              </section>
            );
          })}
        </div>
      )}
    </>
  );
}
