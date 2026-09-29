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
  const board = useData<BoardItem[]>(`${endpoint}/board`, version);
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
        <Link className="button" href={`${endpoint}/issues`}>
          View issues
        </Link>
      </div>
      <ErrorMessage error={error || board.error} />
      {board.loading ? (
        <Loading />
      ) : (
        <div className="issue-board">
          {columns.map((column) => {
            const items =
              board.data?.filter((item) => item.status === column.status) || [];
            return (
              <section
                className="board-column panel"
                key={column.status}
                aria-label={`${column.title} column`}
              >
                <h3>
                  {column.title} <Badge>{items.length}</Badge>
                </h3>
                {items.length ? (
                  items.map((item) => (
                    <article className="board-card" key={item.issue_id}>
                      <strong>{item.title}</strong>
                      <p>
                        #{item.number} · {item.author} · {item.state}
                      </p>
                      {canTriage && (
                        <label>
                          Status for issue #{item.number}
                          <select
                            disabled={busy === item.number}
                            value={item.status}
                            onChange={async (event) => {
                              setBusy(item.number);
                              setError("");
                              try {
                                await put(
                                  `${endpoint}/issues/${item.number}/board`,
                                  { status: event.target.value },
                                );
                                setVersion((value) => value + 1);
                              } catch (saveError) {
                                setError((saveError as Error).message);
                              } finally {
                                setBusy(null);
                              }
                            }}
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
