"use client";

import { useState } from "react";
import {
  CheckCircle2,
  CircleSlash,
  Clock,
  Play,
  ShieldAlert,
  Workflow,
  XCircle,
} from "lucide-react";
import { date, post, put, type Repo } from "@/lib/api";
import { Badge, ErrorMessage, Loading, useData } from "@/components/ui";
import { RouteDesk } from "@/components/phase12";

type WorkflowSummary = {
  path: string;
  name?: string;
  valid: boolean;
  error?: string;
};

type RouteRun = {
  id: string;
  workflow_path: string;
  workflow_name: string;
  ref: string;
  sha: string;
  trigger_event: string;
  trigger_detail: string;
  status: "queued" | "cancelled" | "expired";
  created_at: string;
};

type RouteJob = {
  id: string;
  job_key: string;
  matrix: Record<string, string>;
  needs: string[];
  environment: string;
  timeout_minutes: number | null;
  status: "blocked" | "ready" | "cancelled" | "expired";
};

type RouteLog = { message: string; created_at: string };

const statusMeta: Record<string, { icon: typeof Clock; kind: string }> = {
  queued: { icon: Clock, kind: "" },
  ready: { icon: Play, kind: "green" },
  blocked: { icon: ShieldAlert, kind: "" },
  cancelled: { icon: CircleSlash, kind: "" },
  expired: { icon: XCircle, kind: "red" },
};

// RoutesPanel is the Phase 11 "Routes" surface: it shows the workflow files
// GITOWN found in this repository, the runs they've queued, and each run's
// planned job graph. There is no execution engine yet — a job reaches at
// most "ready", meaning a future sandboxed runner could pick it up, which
// this panel says plainly rather than implying anything actually happened.
export function RoutesPanel({
  endpoint,
  repo,
}: {
  endpoint: string;
  repo: Repo;
}) {
  const [version, setVersion] = useState(0);
  const [openRun, setOpenRun] = useState<string | null>(null);
  const workflows = useData<{ items: WorkflowSummary[] }>(
    `${endpoint}/routes/workflows?ref=${encodeURIComponent(repo.default_branch)}`,
    version,
  );
  const runs = useData<{ items: RouteRun[]; has_more: boolean }>(
    `${endpoint}/routes/runs`,
    version,
  );
  const [error, setError] = useState("");
  const refresh = () => setVersion((v) => v + 1);
  const canManage = repo.can_manage || repo.can_maintain;

  return (
    <div className="panel routes-panel">
      <div className="section-heading">
        <div>
          <h2>
            <Workflow size={18} /> Routes
          </h2>
          <p>
            GITOWN plans workflow runs from files in{" "}
            <code>.gitown/workflows/</code> — parsing, trigger matching, and job
            scheduling are real. Actually executing a step requires a sandboxed
            runner, which is not available yet pending a security review, so a
            run stays queued rather than completing.
          </p>
        </div>
      </div>
      <ErrorMessage error={error} />
      {workflows.loading ? (
        <Loading />
      ) : workflows.data?.items.length ? (
        <div className="routes-workflow-list">
          {workflows.data.items.map((wf) => (
            <div className="routes-workflow-row" key={wf.path}>
              <div>
                <strong>{wf.name || wf.path}</strong>
                <span className="muted small-text">{wf.path}</span>
              </div>
              {wf.valid ? (
                <Badge kind="green">Valid</Badge>
              ) : (
                <span className="form-error" title={wf.error}>
                  {wf.error}
                </span>
              )}
              {wf.valid && canManage && (
                <button
                  className="button small-button"
                  type="button"
                  onClick={async () => {
                    setError("");
                    try {
                      await post(`${endpoint}/routes/dispatch`, {
                        path: wf.path,
                        ref: repo.default_branch,
                      });
                      refresh();
                    } catch (dispatchError) {
                      setError((dispatchError as Error).message);
                    }
                  }}
                >
                  <Play size={14} /> Run
                </button>
              )}
            </div>
          ))}
        </div>
      ) : (
        <div className="empty-inline">
          No workflow files found at .gitown/workflows/*.yml on{" "}
          {repo.default_branch}.
        </div>
      )}
      <h3 className="routes-runs-heading">Runs</h3>
      {runs.loading ? (
        <Loading />
      ) : runs.data?.items.length ? (
        <div className="routes-run-list">
          {runs.data.items.map((run) => (
            <RouteRunRow
              key={run.id}
              endpoint={endpoint}
              run={run}
              canManage={!!canManage}
              expanded={openRun === run.id}
              onToggle={() => setOpenRun(openRun === run.id ? null : run.id)}
              onChanged={refresh}
              onError={setError}
            />
          ))}
        </div>
      ) : (
        <div className="empty-inline">
          No runs yet. A run is queued when a workflow's trigger matches a push,
          pull request, schedule, or manual run.
        </div>
      )}
      <RouteDesk endpoint={endpoint} canManage={!!canManage} />
    </div>
  );
}

function RouteRunRow({
  endpoint,
  run,
  canManage,
  expanded,
  onToggle,
  onChanged,
  onError,
}: {
  endpoint: string;
  run: RouteRun;
  canManage: boolean;
  expanded: boolean;
  onToggle: () => void;
  onChanged: () => void;
  onError: (message: string) => void;
}) {
  const [detailVersion, setDetailVersion] = useState(0);
  const detail = useData<{ jobs: RouteJob[]; logs: RouteLog[] }>(
    expanded ? `${endpoint}/routes/runs/${run.id}` : null,
    detailVersion,
  );
  const meta = statusMeta[run.status] || statusMeta.queued;
  return (
    <div className="routes-run-group">
      <button className="routes-run-row" type="button" onClick={onToggle}>
        <meta.icon size={16} />
        <div>
          <strong>{run.workflow_name}</strong>
          <span className="muted small-text">
            {run.trigger_event} · {run.ref.slice(0, 40)} ·{" "}
            {date(run.created_at)}
          </span>
        </div>
        <Badge kind={meta.kind}>{run.status}</Badge>
      </button>
      {expanded && (
        <div className="routes-run-detail">
          {detail.loading ? (
            <Loading />
          ) : (
            <>
              <ErrorMessage error={detail.error} />
              {detail.data?.jobs.map((job) => (
                <div className="routes-job-row" key={job.id}>
                  <div>
                    <strong>{job.job_key}</strong>
                    {job.needs.length > 0 && (
                      <span className="muted small-text">
                        {" "}
                        needs {job.needs.join(", ")}
                      </span>
                    )}
                    {job.environment && (
                      <span className="muted small-text">
                        {" "}
                        · environment {job.environment}
                      </span>
                    )}
                  </div>
                  <Badge kind={statusMeta[job.status]?.kind}>
                    {job.status}
                  </Badge>
                  {job.status === "blocked" && job.environment && (
                    <button
                      className="button small-button"
                      type="button"
                      onClick={async () => {
                        try {
                          await post(
                            `${endpoint}/routes/jobs/${job.id}/approve`,
                            {},
                          );
                          setDetailVersion((v) => v + 1);
                        } catch (approveError) {
                          onError((approveError as Error).message);
                        }
                      }}
                    >
                      <CheckCircle2 size={14} /> Approve
                    </button>
                  )}
                </div>
              ))}
              <div className="routes-log-list">
                {detail.data?.logs.map((log, i) => (
                  <div className="routes-log-line" key={i}>
                    <span className="muted small-text">
                      {date(log.created_at)}
                    </span>{" "}
                    {log.message}
                  </div>
                ))}
              </div>
              {canManage && run.status === "queued" && (
                <button
                  className="button small-button danger-icon"
                  type="button"
                  onClick={async () => {
                    try {
                      await post(
                        `${endpoint}/routes/runs/${run.id}/cancel`,
                        {},
                      );
                      onChanged();
                    } catch (cancelError) {
                      onError((cancelError as Error).message);
                    }
                  }}
                >
                  Cancel run
                </button>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// RouteEnvironmentSettings lets an owner/maintainer configure which
// approvers a protected environment (e.g. "production") requires before a
// job naming it can leave 'blocked'. Placed in repository settings.
export function RouteEnvironmentSettings({ endpoint }: { endpoint: string }) {
  const [name, setName] = useState("");
  const [approvers, setApprovers] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  return (
    <div className="panel form-panel">
      <h2>
        <ShieldAlert size={18} /> Protected environments
      </h2>
      <p>
        A job naming a protected environment stays blocked until one of its
        required approvers approves it — useful today as a real gate, even
        though nothing executes the job afterward yet.
      </p>
      <ErrorMessage error={error} />
      {message && <div className="success-box">{message}</div>}
      <form
        className="collaborator-form"
        onSubmit={async (event) => {
          event.preventDefault();
          setError("");
          setMessage("");
          try {
            await put(
              `${endpoint}/routes/environments/${encodeURIComponent(name)}`,
              {
                required_approvers: approvers
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              },
            );
            setMessage(`Saved environment ${name}.`);
          } catch (saveError) {
            setError((saveError as Error).message);
          }
        }}
      >
        <label>
          Environment name
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="production"
            required
          />
        </label>
        <label>
          Required approvers
          <input
            value={approvers}
            onChange={(e) => setApprovers(e.target.value)}
            placeholder="username, crew:release-team"
          />
        </label>
        <button className="button primary" type="submit">
          Save environment
        </button>
      </form>
    </div>
  );
}
