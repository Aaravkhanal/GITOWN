"use client";

import { useState } from "react";
import { Bell } from "lucide-react";
import { put, type RepositoryWatch } from "@/lib/api";
import { useData } from "./ui";

const watchModes: [RepositoryWatch["mode"], string][] = [
  ["participating", "Participating"],
  ["watching", "Watching"],
  ["ignoring", "Ignoring"],
];

// WatchMenu chooses how a repository notifies the signed-in person:
// watching adds every new issue and Unite request, participating limits
// updates to threads they joined, and ignoring silences the repository.
export function WatchMenu({ endpoint }: { endpoint: string }) {
  const [version, setVersion] = useState(0);
  const [error, setError] = useState("");
  const watch = useData<RepositoryWatch>(`${endpoint}/subscription`, version);
  if (!watch.data?.mode) return null;
  return (
    <label className="watch-menu" title="Repository notifications">
      <Bell size={15} />
      <select
        aria-label="Repository notifications"
        value={watch.data.mode}
        onChange={async (event) => {
          setError("");
          try {
            await put(`${endpoint}/subscription`, { mode: event.target.value });
            setVersion((value) => value + 1);
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        {watchModes.map(([mode, label]) => (
          <option key={mode} value={mode}>
            {label}
          </option>
        ))}
      </select>
      {error && (
        <span className="error-text" role="alert">
          {error}
        </span>
      )}
    </label>
  );
}
