"use client";

import { useState } from "react";
import { SafeMarkdown } from "@/components/ecosystem";
import { ErrorMessage, Loading, useData } from "@/components/ui";

const keywords = new Set([
  "func",
  "return",
  "if",
  "else",
  "for",
  "range",
  "switch",
  "case",
  "break",
  "continue",
  "package",
  "import",
  "type",
  "struct",
  "interface",
  "var",
  "const",
  "map",
  "go",
  "defer",
  "select",
  "chan",
  "class",
  "def",
  "fn",
  "let",
  "mut",
  "pub",
  "impl",
  "enum",
  "match",
  "async",
  "await",
  "function",
  "const",
  "export",
  "from",
  "new",
  "nil",
  "null",
  "true",
  "false",
  "None",
  "True",
  "False",
  "self",
  "public",
  "private",
  "protected",
  "static",
  "void",
  "int",
  "string",
  "bool",
]);

export function languageOf(path: string) {
  const ext = path.split(".").pop()?.toLowerCase() || "";
  const table: Record<string, string> = {
    go: "go",
    js: "js",
    jsx: "js",
    ts: "js",
    tsx: "js",
    mjs: "js",
    py: "py",
    rs: "rs",
    rb: "rb",
    java: "java",
    c: "c",
    h: "c",
    css: "css",
    json: "json",
    yml: "yaml",
    yaml: "yaml",
    sh: "sh",
    bash: "sh",
    md: "md",
  };
  return table[ext] || "";
}

function tokens(line: string) {
  const parts: { text: string; kind: string }[] = [];
  const comment = line.match(/^(\s*)(\/\/.*|#.*)$/);
  if (comment && !line.includes("://")) {
    if (comment[1]) parts.push({ text: comment[1], kind: "" });
    parts.push({ text: comment[2], kind: "comment" });
    return parts;
  }
  const pattern =
    /("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`[^`]*`|\b\d+(?:\.\d+)?\b|\b[A-Za-z_][A-Za-z0-9_]*\b|\s+|.)/g;
  for (const match of line.matchAll(pattern)) {
    const text = match[0];
    let kind = "";
    if (text.startsWith('"') || text.startsWith("'") || text.startsWith("`"))
      kind = "string";
    else if (/^\d/.test(text)) kind = "number";
    else if (keywords.has(text)) kind = "keyword";
    parts.push({ text, kind });
  }
  return parts.length ? parts : [{ text: line || " ", kind: "" }];
}

export function SourceView({
  path,
  content,
}: {
  path: string;
  content: string;
}) {
  const markdown = path.toLowerCase().endsWith(".md");
  const [rendered, setRendered] = useState(markdown);
  const lines = content.split("\n");
  return (
    <div>
      {markdown && (
        <div className="blob-header">
          <button
            className="button small-button"
            type="button"
            onClick={() => setRendered((value) => !value)}
          >
            {rendered ? "Source" : "Rendered"}
          </button>
        </div>
      )}
      {rendered && markdown ? (
        <article className="readme-panel">
          <SafeMarkdown text={content} />
        </article>
      ) : (
        <pre>
          {lines.map((line, index) => (
            <span className="code-line" key={index}>
              <span className="line-number" aria-hidden="true">
                {index + 1}
              </span>
              <code>
                {tokens(line).map((part, partIndex) =>
                  part.kind ? (
                    <span className={`tok tok-${part.kind}`} key={partIndex}>
                      {part.text}
                    </span>
                  ) : (
                    <span key={partIndex}>{part.text || " "}</span>
                  ),
                )}
              </code>
            </span>
          ))}
        </pre>
      )}
    </div>
  );
}

export function BlameView({
  endpoint,
  branch,
  path,
}: {
  endpoint: string;
  branch: string;
  path: string;
}) {
  const blame = useData<{
    lines: {
      sha: string;
      author: string;
      summary: string;
      line: number;
      text: string;
    }[];
    truncated: boolean;
  }>(
    `${endpoint}/blame?ref=${encodeURIComponent(branch)}&path=${encodeURIComponent(path)}`,
  );
  if (blame.loading) return <Loading />;
  if (blame.error) return <ErrorMessage error={blame.error} />;
  return (
    <div className="file-history">
      {blame.data?.truncated && (
        <p className="muted padded">Showing the first 4,000 lines.</p>
      )}
      <pre>
        {blame.data?.lines.map((line) => (
          <span className="code-line" key={line.line}>
            <span className="line-number blame-meta" title={line.summary}>
              {line.sha.slice(0, 7)} {line.author}
            </span>
            <code>{line.text || " "}</code>
          </span>
        ))}
      </pre>
    </div>
  );
}

export function ComparePanel({
  endpoint,
  branches,
  current,
}: {
  endpoint: string;
  branches: string[];
  current: string;
}) {
  const [open, setOpen] = useState(false);
  const [base, setBase] = useState(branches[0] || "main");
  const [head, setHead] = useState(
    current || branches[1] || branches[0] || "main",
  );
  const compare = useData<{
    diff: string;
    diff_truncated: boolean;
    base_sha: string;
    head_sha: string;
  }>(
    open && base !== head
      ? `${endpoint}/compare?base=${encodeURIComponent(base)}&head=${encodeURIComponent(head)}`
      : null,
  );
  return (
    <section className="panel">
      <div className="section-heading">
        <h2>Compare branches</h2>
        <button
          className="button small-button"
          type="button"
          onClick={() => setOpen((value) => !value)}
        >
          {open ? "Hide compare" : "Compare"}
        </button>
      </div>
      {open && (
        <>
          <div className="inline-form">
            <label>
              Base
              <select
                value={base}
                onChange={(event) => setBase(event.target.value)}
              >
                {branches.map((name) => (
                  <option key={name}>{name}</option>
                ))}
              </select>
            </label>
            <label>
              Head
              <select
                value={head}
                onChange={(event) => setHead(event.target.value)}
              >
                {branches.map((name) => (
                  <option key={name}>{name}</option>
                ))}
              </select>
            </label>
          </div>
          {base === head ? (
            <p className="muted">Choose two different branches.</p>
          ) : compare.loading ? (
            <Loading />
          ) : (
            <>
              <ErrorMessage error={compare.error} />
              {compare.data?.diff_truncated && (
                <p className="muted">The diff is truncated.</p>
              )}
              <pre className="diff-panel">
                {(compare.data?.diff || "No changes.")
                  .split("\n")
                  .map((line, index) => (
                    <span
                      className={`diff-line ${line.startsWith("+") && !line.startsWith("+++") ? "addition" : line.startsWith("-") && !line.startsWith("---") ? "deletion" : line.startsWith("@@") ? "diff-hunk" : ""}`}
                      key={index}
                    >
                      {line || " "}
                    </span>
                  ))}
              </pre>
            </>
          )}
        </>
      )}
    </section>
  );
}
