"use client";

import { useEffect, useState } from "react";
import { AlertCircle, Check, Copy, LoaderCircle } from "lucide-react";
import { api } from "@/lib/api";

export function useData<T>(path: string | null, version = 0) {
  const [state, setState] = useState<{
    data?: T;
    error?: string;
    loading: boolean;
  }>({ loading: !!path });
  useEffect(() => {
    let current = true;
    if (!path) {
      setState({ loading: false });
      return;
    }
    setState({ loading: true });
    api<T>(path)
      .then((data) => {
        if (current) setState({ data, loading: false });
      })
      .catch((error) => {
        if (current) setState({ error: error.message, loading: false });
      });
    return () => {
      current = false;
    };
  }, [path, version]);
  return state;
}
export function ErrorMessage({ error }: { error?: string }) {
  return error ? (
    <div className="error" role="alert">
      <AlertCircle size={17} />
      {error}
    </div>
  ) : null;
}
export function Loading() {
  return (
    <div className="loading" role="status">
      <LoaderCircle className="spin" size={20} /> Loading your workspace…
    </div>
  );
}
export function Avatar({
  name,
  small = false,
}: {
  name: string;
  small?: boolean;
}) {
  return (
    <span className={`avatar ${small ? "small" : ""}`} aria-hidden="true">
      {name.slice(0, 2).toUpperCase()}
    </span>
  );
}
export function Badge({
  children,
  kind = "",
}: {
  children: React.ReactNode;
  kind?: string;
}) {
  return <span className={`badge ${kind}`}>{children}</span>;
}
export function CopyButton({
  text,
  label = "Copy",
}: {
  text: string;
  label?: string;
}) {
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState(false);
  useEffect(() => {
    if (copied) {
      const timer = setTimeout(() => setCopied(false), 2000);
      return () => clearTimeout(timer);
    }
  }, [copied]);
  return (
    <button
      className="copy-button"
      aria-label={
        error ? "Copy failed; select text manually" : copied ? "Copied" : label
      }
      title={error ? "Select and copy the text manually" : label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          setError(false);
        } catch {
          setError(true);
        }
      }}
    >
      {copied ? <Check size={16} /> : <Copy size={16} />}
      {copied && <span>Copied</span>}
      {error && <span>Copy failed</span>}
    </button>
  );
}
