-- Phase 11 "Routes": workflow definition, trigger, and orchestration data
-- model. There is deliberately no execution engine yet (see
-- docs/PRODUCT_PHASES.md) — these tables record what GITOWN has planned,
-- not what ran, so every status column here stops short of any
-- success/failure state that would imply a step actually executed.

CREATE TABLE route_runs (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    workflow_path text NOT NULL CHECK (char_length(workflow_path) BETWEEN 1 AND 300),
    workflow_name text NOT NULL CHECK (char_length(workflow_name) BETWEEN 1 AND 200),
    ref text NOT NULL CHECK (char_length(ref) BETWEEN 1 AND 300),
    sha text CHECK (sha IS NULL OR char_length(sha) = 40),
    trigger_event text NOT NULL CHECK (trigger_event IN ('push', 'pull_request', 'schedule', 'workflow_dispatch')),
    trigger_actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
    trigger_detail text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'cancelled', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX route_runs_repository ON route_runs(repository_id, created_at DESC);

CREATE TABLE route_jobs (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES route_runs(id) ON DELETE CASCADE,
    job_key text NOT NULL CHECK (char_length(job_key) BETWEEN 1 AND 200),
    matrix jsonb NOT NULL DEFAULT '{}'::jsonb,
    needs text[] NOT NULL DEFAULT '{}',
    environment text,
    timeout_minutes int CHECK (timeout_minutes IS NULL OR (timeout_minutes > 0 AND timeout_minutes <= 1440)),
    -- steps is an opaque record of {name, run, uses} strings taken verbatim
    -- from the workflow file. Nothing in this codebase ever interprets or
    -- executes the run/uses value; it exists purely for display and for a
    -- future executor to consume once sandboxing has been reviewed.
    steps jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL DEFAULT 'blocked' CHECK (status IN ('blocked', 'ready', 'cancelled', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX route_jobs_run ON route_jobs(run_id);

CREATE TABLE route_run_logs (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES route_runs(id) ON DELETE CASCADE,
    job_id uuid REFERENCES route_jobs(id) ON DELETE CASCADE,
    message text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 2000),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX route_run_logs_run ON route_run_logs(run_id, created_at);

CREATE TABLE route_run_artifacts (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES route_runs(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$'),
    content_type text NOT NULL DEFAULT 'application/octet-stream',
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    storage_path text NOT NULL,
    uploaded_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(run_id, name)
);

-- A protected deployment environment for a repository. A job naming this
-- environment cannot leave 'blocked' until every listed approver (a
-- username, or a crew:<slug> entry resolved the same way branch-protection
-- required reviewers already are) has approved that specific job.
CREATE TABLE route_environments (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,49}$'),
    required_approvers text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(repository_id, name)
);

CREATE TABLE route_job_approvals (
    id uuid PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES route_jobs(id) ON DELETE CASCADE,
    approver_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(job_id, approver_id)
);

-- A minimal schedule row per (repository, workflow file). The worker tick
-- checks the stored cron fields directly against the current UTC minute
-- rather than precomputing a next-run timestamp, which keeps the cron
-- support intentionally small: '*' or an exact comma-separated list per
-- field, no ranges or steps.
CREATE TABLE route_schedules (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    workflow_path text NOT NULL CHECK (char_length(workflow_path) BETWEEN 1 AND 300),
    cron text NOT NULL,
    last_fired_minute timestamptz,
    UNIQUE(repository_id, workflow_path)
);
