# Routes: workflow planning preview

Routes is an early Phase 11 preview. It reads `.gitown/workflows/*.yml` or `.yaml` from a repository, validates a bounded workflow definition, and records planned runs and jobs after matching pushes, opened Unite requests, or an owner's manual dispatch. The repository's Routes tab shows the workflow files, queued runs, job graph, and planning log. A protected environment can name required approvers, and a queued run can be cancelled.

**No workflow step executes.** A job marked `ready` means only that its declared dependencies and approval gate permit a future runner to pick it up. GITOWN does not currently spawn a runner, read a secret value into a job, produce job output, upload artifacts, or report a successful run. Routes posts a pending commit status; required checks must not be configured to depend on that status until a real runner exists. Queued runs expire after 24 hours.

Example definition:

```yaml
name: Verification
on:
  push:
    branches: [main]
  workflow_dispatch: {}
jobs:
  build:
    steps:
      - name: Test
        run: go test ./...
```

The `run` value above is stored for display only. It is **not executed**. Supported trigger declarations are `push`, `pull_request`, `workflow_dispatch`, and one restricted five-field UTC `schedule` expression per workflow. A push to the default branch registers or removes that workflow's schedule; a background worker checks due minutes and queues at most one run for a schedule in that minute. Existing workflow files from before this preview need a new default-branch push to register their schedules. Job definitions can use `needs`, a bounded matrix, `environment`, `timeout-minutes`, and local reusable workflows (`uses: ./.gitown/workflows/name.yml`). Reusable workflows are limited to three levels; arbitrary remote actions and scripts are never fetched.

The API under `/api/v1/repos/{owner}/{repo}/routes/` lists workflows and runs, reads a run's planned jobs and log, manually queues a workflow, cancels a queued run, manages environment approvers, and records approvals. Owners and maintainers manage dispatch, cancellation, and environment rules; a required named or crew approver may approve a matching job. Approval never executes it.

Before implementing execution, Routes needs an independently reviewed sandbox with filesystem/network isolation, immutable checked-out commits, scoped ephemeral credentials, secret redaction, strict CPU/memory/time/storage quotas, cancellation that terminates the process tree, append-only logs, artifact upload/download with retention, and terminal statuses and check integration. Never run repository-authored `run` strings in the API server process. The schedule claim is at-most-once per minute: if run creation fails after claiming the minute, that occurrence is logged and not automatically replayed.
