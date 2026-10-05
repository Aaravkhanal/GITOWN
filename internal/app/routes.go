package app

// Routes is GITOWN's workflow-automation product (Phase 11 of the
// roadmap). This file implements definition parsing, trigger matching, and
// run/job orchestration — deliberately nothing else. There is no execution
// engine: a "run" records what GITOWN has planned in response to a push,
// pull request, schedule, or manual dispatch, and a "job" reaches at most
// 'ready', meaning an executor could pick it up. No executor exists yet,
// pending the sandboxing security review the roadmap calls for, so no code
// in this file (or anywhere else touched by this change) ever spawns a
// process or evaluates a workflow-file-derived string as code. See
// docs/PRODUCT_PHASES.md for the product framing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

const (
	routesDir             = ".gitown/workflows"
	routesMaxFileBytes    = 64 * 1024
	routesMaxJobsInFile   = 20
	routesMaxStepsPerJob  = 30
	routesMaxMatrixDims   = 4
	routesMaxMatrixValues = 8
	routesMaxJobInstances = 64
	routesMaxTotalJobs    = 100
	routesMaxReuseDepth   = 3
	routesExpireAfter     = 24 * time.Hour
	routesWorkerInterval  = 30 * time.Second
)

// --- Workflow file schema -------------------------------------------------
//
// Deliberately typed and narrow: yaml.v3 unmarshals directly into these
// structs with no loose interface{} maps and no custom UnmarshalYAML hooks,
// so parsing itself has no code path that could be steered by file content.

type routesFile struct {
	Name string                  `yaml:"name"`
	On   routesTriggerBlock      `yaml:"on"`
	Jobs map[string]routesJobDef `yaml:"jobs"`
}

type routesTriggerBlock struct {
	Push             *routesRefTrigger    `yaml:"push"`
	PullRequest      *routesRefTrigger    `yaml:"pull_request"`
	Schedule         []routesScheduleItem `yaml:"schedule"`
	WorkflowDispatch *struct{}            `yaml:"workflow_dispatch"`
}

type routesRefTrigger struct {
	Branches []string `yaml:"branches"`
}

type routesScheduleItem struct {
	Cron string `yaml:"cron"`
}

type routesJobDef struct {
	Needs          []string        `yaml:"needs"`
	Environment    string          `yaml:"environment"`
	TimeoutMinutes int             `yaml:"timeout-minutes"`
	Strategy       *routesStrategy `yaml:"strategy"`
	Uses           string          `yaml:"uses"`
	Steps          []routesStepDef `yaml:"steps"`
}

type routesStrategy struct {
	Matrix map[string][]string `yaml:"matrix"`
}

// routesStepDef's Run and Uses fields are opaque strings taken verbatim
// from the workflow file. They are stored and displayed; nothing in this
// codebase ever executes, shells out with, or evaluates their content.
type routesStepDef struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
	Uses string `yaml:"uses"`
}

// --- Resolution output -----------------------------------------------------

type resolvedJob struct {
	Key            string
	Needs          []string
	Environment    string
	TimeoutMinutes *int
	Matrix         map[string]string
	Steps          []routesStepDef
}

var routesJobKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var routesSecretRef = regexp.MustCompile(`\$\{\{\s*secrets\.([A-Za-z0-9_]+)\s*\}\}`)

func parseRoutesFile(data []byte) (*routesFile, error) {
	if len(data) == 0 {
		return nil, errors.New("the workflow file is empty")
	}
	if len(data) > routesMaxFileBytes {
		return nil, fmt.Errorf("workflow files are limited to %d KB", routesMaxFileBytes/1024)
	}
	var f routesFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("invalid workflow YAML: %w", err)
	}
	if strings.TrimSpace(f.Name) == "" {
		return nil, errors.New("a workflow needs a name")
	}
	if len(f.Name) > 200 {
		return nil, errors.New("the workflow name is too long")
	}
	if len(f.Jobs) == 0 {
		return nil, errors.New("a workflow needs at least one job")
	}
	if len(f.Jobs) > routesMaxJobsInFile {
		return nil, fmt.Errorf("a workflow may define up to %d jobs", routesMaxJobsInFile)
	}
	for key, job := range f.Jobs {
		if !routesJobKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("job id %q must be alphanumeric (with - or _), up to 64 characters", key)
		}
		if job.Uses != "" && len(job.Steps) > 0 {
			return nil, fmt.Errorf("job %q cannot set both uses and steps", key)
		}
		if job.Uses == "" && len(job.Steps) == 0 {
			return nil, fmt.Errorf("job %q needs either uses or at least one step", key)
		}
		if len(job.Steps) > routesMaxStepsPerJob {
			return nil, fmt.Errorf("job %q may define up to %d steps", key, routesMaxStepsPerJob)
		}
		for i, step := range job.Steps {
			if step.Run == "" && step.Uses == "" {
				return nil, fmt.Errorf("job %q step %d needs a run or uses value", key, i+1)
			}
			if len(step.Name) > 200 || len(step.Run) > 4000 || len(step.Uses) > 300 {
				return nil, fmt.Errorf("job %q step %d has an oversized field", key, i+1)
			}
		}
		if job.TimeoutMinutes < 0 || job.TimeoutMinutes > 1440 {
			return nil, fmt.Errorf("job %q timeout-minutes must be between 1 and 1440", key)
		}
		if job.Environment != "" && !slug.MatchString(job.Environment) {
			return nil, fmt.Errorf("job %q environment name is invalid", key)
		}
		for _, need := range job.Needs {
			if _, ok := f.Jobs[need]; !ok {
				return nil, fmt.Errorf("job %q needs unknown job %q", key, need)
			}
		}
		if job.Strategy != nil {
			if len(job.Strategy.Matrix) > routesMaxMatrixDims {
				return nil, fmt.Errorf("job %q may vary up to %d matrix dimensions", key, routesMaxMatrixDims)
			}
			total := 1
			for dim, values := range job.Strategy.Matrix {
				if len(values) == 0 || len(values) > routesMaxMatrixValues {
					return nil, fmt.Errorf("job %q matrix dimension %q must list 1-%d values", key, dim, routesMaxMatrixValues)
				}
				total *= len(values)
			}
			if total > routesMaxJobInstances {
				return nil, fmt.Errorf("job %q matrix expands to %d combinations, which is over the %d limit", key, total, routesMaxJobInstances)
			}
		}
	}
	if cycle := routesDetectCycle(f.Jobs); cycle != "" {
		return nil, fmt.Errorf("the job graph has a cycle at %q", cycle)
	}
	if len(f.On.Schedule) > 1 {
		return nil, errors.New("a workflow can declare at most one schedule")
	}
	for _, item := range f.On.Schedule {
		if _, err := parseRoutesCron(item.Cron); err != nil {
			return nil, err
		}
	}
	return &f, nil
}

// syncRouteSchedules follows the repository's current default branch. It
// replaces the registry only after every candidate workflow has been read,
// so a transient Git error cannot erase a previously registered schedule.
func (a *App) syncRouteSchedules(ctx context.Context, repo *Repository) error {
	tree, err := a.git.Browse(ctx, repo.ID, repo.DefaultBranch, routesDir)
	if err != nil && !errors.Is(err, gitstore.ErrNotFound) {
		return err
	}
	schedules := map[string]string{}
	if err == nil {
		load := a.routesGitLoader(repo.ID, repo.DefaultBranch)
		for _, entry := range tree.Entries {
			if entry.Type != "blob" || !(strings.HasSuffix(entry.Name, ".yml") || strings.HasSuffix(entry.Name, ".yaml")) {
				continue
			}
			path := routesDir + "/" + entry.Name
			data, readErr := load(ctx, path)
			if readErr != nil {
				return readErr
			}
			file, parseErr := parseRoutesFile(data)
			if parseErr == nil && len(file.On.Schedule) == 1 {
				schedules[path] = file.On.Schedule[0].Cron
			}
		}
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	paths := make([]string, 0, len(schedules))
	for path := range schedules {
		paths = append(paths, path)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM route_schedules WHERE repository_id=$1 AND NOT (workflow_path=ANY($2::text[]))`, repo.ID, paths); err != nil {
		return err
	}
	for path, cron := range schedules {
		if _, err = tx.Exec(ctx, `INSERT INTO route_schedules(id,repository_id,workflow_path,cron) VALUES($1,$2,$3,$4)
			ON CONFLICT(repository_id,workflow_path) DO UPDATE SET cron=excluded.cron,
			last_fired_minute=CASE WHEN route_schedules.cron=excluded.cron THEN route_schedules.last_fired_minute ELSE NULL END`, auth.ID(), repo.ID, path, cron); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func routesDetectCycle(jobs map[string]routesJobDef) string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := map[string]int{}
	var visit func(key string) string
	visit = func(key string) string {
		switch state[key] {
		case gray:
			return key
		case black:
			return ""
		}
		state[key] = gray
		for _, need := range jobs[key].Needs {
			if cyc := visit(need); cyc != "" {
				return cyc
			}
		}
		state[key] = black
		return ""
	}
	keys := make([]string, 0, len(jobs))
	for k := range jobs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if cyc := visit(k); cyc != "" {
			return cyc
		}
	}
	return ""
}

// --- Reusable workflows and matrix expansion --------------------------------

type routesLoader func(ctx context.Context, path string) ([]byte, error)

// resolveRoutesJobs expands `uses:` job references (up to routesMaxReuseDepth)
// and matrix strategies into a flat list of concrete job instances. It never
// reads or writes anything itself — load is supplied by the caller so this
// stays testable without a repository.
func resolveRoutesJobs(ctx context.Context, load routesLoader, path string, file *routesFile, visited map[string]bool, depth int) ([]resolvedJob, error) {
	if depth > routesMaxReuseDepth {
		return nil, fmt.Errorf("workflow reuse is nested more than %d levels deep", routesMaxReuseDepth)
	}
	if visited[path] {
		return nil, fmt.Errorf("workflow %q is referenced in a cycle", path)
	}
	visited[path] = true
	defer delete(visited, path)

	keys := make([]string, 0, len(file.Jobs))
	for k := range file.Jobs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// producedBy maps each job id as it appears in `needs:` in THIS file
	// (a sibling key, never a prefixed or matrix-suffixed one) to every
	// final job key it expands to — one entry for a plain job, several for
	// a matrix job, and every nested job's key for a `uses:` job. Needs are
	// rewritten against this map in a second pass below, so a job that
	// depends on a reusable-workflow job waits on everything that workflow
	// produced, and a job that depends on a matrix job waits on every
	// instance of it.
	producedBy := map[string][]string{}
	var out []resolvedJob
	for _, key := range keys {
		job := file.Jobs[key]
		if job.Uses != "" {
			refPath, err := routesResolveUsesPath(path, job.Uses)
			if err != nil {
				return nil, fmt.Errorf("job %q: %w", key, err)
			}
			data, err := load(ctx, refPath)
			if err != nil {
				return nil, fmt.Errorf("job %q references %q, which could not be read: %w", key, job.Uses, err)
			}
			refFile, err := parseRoutesFile(data)
			if err != nil {
				return nil, fmt.Errorf("job %q references an invalid workflow %q: %w", key, job.Uses, err)
			}
			nested, err := resolveRoutesJobs(ctx, load, refPath, refFile, visited, depth+1)
			if err != nil {
				return nil, err
			}
			for i := range nested {
				for j := range nested[i].Needs {
					nested[i].Needs[j] = key + "/" + nested[i].Needs[j]
				}
				nested[i].Key = key + "/" + nested[i].Key
				producedBy[key] = append(producedBy[key], nested[i].Key)
				out = append(out, nested[i])
			}
			continue
		}
		instances, err := expandRoutesMatrix(key, job)
		if err != nil {
			return nil, err
		}
		for _, inst := range instances {
			producedBy[key] = append(producedBy[key], inst.Key)
		}
		out = append(out, instances...)
		if len(out) > routesMaxTotalJobs {
			return nil, fmt.Errorf("this workflow resolves to more than %d jobs", routesMaxTotalJobs)
		}
	}
	// Second pass: rewrite every job's Needs from sibling job ids to the
	// final keys those ids produced. A nested job's own internal Needs
	// (already prefixed above, referencing siblings inside its own file)
	// are untouched here since their originating ids aren't in this file's
	// producedBy map.
	for i := range out {
		if out[i].Needs == nil {
			continue
		}
		rewritten := make([]string, 0, len(out[i].Needs))
		seen := map[string]bool{}
		for _, need := range out[i].Needs {
			targets, ok := producedBy[need]
			if !ok {
				targets = []string{need}
			}
			for _, t := range targets {
				if !seen[t] {
					seen[t] = true
					rewritten = append(rewritten, t)
				}
			}
		}
		out[i].Needs = rewritten
	}
	return out, nil
}

func routesResolveUsesPath(fromPath, uses string) (string, error) {
	uses = strings.TrimPrefix(strings.TrimSpace(uses), "./")
	if uses == "" || strings.Contains(uses, "..") || strings.HasPrefix(uses, "/") {
		return "", errors.New("uses must be a relative path within .gitown/workflows")
	}
	if !strings.HasPrefix(uses, routesDir+"/") {
		return "", fmt.Errorf("uses must reference a file under %s/", routesDir)
	}
	return uses, nil
}

func expandRoutesMatrix(key string, job routesJobDef) ([]resolvedJob, error) {
	var timeout *int
	if job.TimeoutMinutes > 0 {
		t := job.TimeoutMinutes
		timeout = &t
	}
	base := resolvedJob{Key: key, Needs: append([]string{}, job.Needs...), Environment: job.Environment, TimeoutMinutes: timeout, Steps: job.Steps}
	if job.Strategy == nil || len(job.Strategy.Matrix) == 0 {
		base.Matrix = map[string]string{}
		return []resolvedJob{base}, nil
	}
	dims := make([]string, 0, len(job.Strategy.Matrix))
	for dim := range job.Strategy.Matrix {
		dims = append(dims, dim)
	}
	sort.Strings(dims)
	var combos []map[string]string
	combos = append(combos, map[string]string{})
	for _, dim := range dims {
		var next []map[string]string
		for _, existing := range combos {
			for _, value := range job.Strategy.Matrix[dim] {
				combo := map[string]string{}
				for k, v := range existing {
					combo[k] = v
				}
				combo[dim] = value
				next = append(next, combo)
			}
		}
		combos = next
	}
	if len(combos) > routesMaxJobInstances {
		return nil, fmt.Errorf("job %q matrix expands to %d combinations, which is over the %d limit", key, len(combos), routesMaxJobInstances)
	}
	instances := make([]resolvedJob, 0, len(combos))
	for i, combo := range combos {
		inst := base
		inst.Matrix = combo
		if len(combos) > 1 {
			parts := make([]string, 0, len(dims))
			for _, dim := range dims {
				parts = append(parts, dim+"="+combo[dim])
			}
			inst.Key = fmt.Sprintf("%s (%s)", key, strings.Join(parts, ", "))
			_ = i
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

// --- Minimal cron matching --------------------------------------------------
//
// Deliberately small: five space-separated fields (minute hour day month
// weekday), each either "*" or a comma-separated list of exact integers.
// No ranges, steps, or names. The worker tick checks the current UTC
// minute against these fields directly rather than precomputing a next-run
// time.

type routesCron struct {
	minute, hour, day, month, weekday []int // nil field slice means "*"
	wildcard                          [5]bool
}

func parseRoutesCron(expr string) (*routesCron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron %q must have 5 fields: minute hour day month weekday", expr)
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	c := &routesCron{}
	slots := []*[]int{&c.minute, &c.hour, &c.day, &c.month, &c.weekday}
	for i, field := range fields {
		if field == "*" {
			c.wildcard[i] = true
			continue
		}
		for _, raw := range strings.Split(field, ",") {
			n, err := strconv.Atoi(raw)
			if err != nil || n < bounds[i][0] || n > bounds[i][1] {
				return nil, fmt.Errorf("cron field %d (%q) is invalid", i+1, field)
			}
			*slots[i] = append(*slots[i], n)
		}
		if len(*slots[i]) == 0 {
			return nil, fmt.Errorf("cron field %d (%q) is invalid", i+1, field)
		}
	}
	return c, nil
}

func (c *routesCron) matches(t time.Time) bool {
	t = t.UTC()
	check := func(wildcard bool, values []int, got int) bool {
		if wildcard {
			return true
		}
		for _, v := range values {
			if v == got {
				return true
			}
		}
		return false
	}
	return check(c.wildcard[0], c.minute, t.Minute()) &&
		check(c.wildcard[1], c.hour, t.Hour()) &&
		check(c.wildcard[2], c.day, t.Day()) &&
		check(c.wildcard[3], c.month, int(t.Month())) &&
		check(c.wildcard[4], c.weekday, int(t.Weekday()))
}

// --- Discovering workflow files in a repository -----------------------------

type routesFileSummary struct {
	Path  string `json:"path"`
	Name  string `json:"name,omitempty"`
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

func (a *App) routesGitLoader(repoID, ref string) routesLoader {
	return func(ctx context.Context, path string) ([]byte, error) {
		return a.git.Blob(ctx, repoID, ref, path)
	}
}

func (a *App) listRoutesFiles(ctx context.Context, repo *Repository, ref string) ([]routesFileSummary, error) {
	tree, err := a.git.Browse(ctx, repo.ID, ref, routesDir)
	if err != nil {
		if errors.Is(err, gitstore.ErrNotFound) {
			return []routesFileSummary{}, nil
		}
		return nil, err
	}
	load := a.routesGitLoader(repo.ID, ref)
	out := []routesFileSummary{}
	for _, entry := range tree.Entries {
		if entry.Type != "blob" || !(strings.HasSuffix(entry.Name, ".yml") || strings.HasSuffix(entry.Name, ".yaml")) {
			continue
		}
		path := routesDir + "/" + entry.Name
		summary := routesFileSummary{Path: path}
		data, err := load(ctx, path)
		if err != nil {
			summary.Error = "could not be read"
			out = append(out, summary)
			continue
		}
		file, err := parseRoutesFile(data)
		if err != nil {
			summary.Error = err.Error()
			out = append(out, summary)
			continue
		}
		if _, resolveErr := resolveRoutesJobs(ctx, load, path, file, map[string]bool{}, 0); resolveErr != nil {
			summary.Error = resolveErr.Error()
			out = append(out, summary)
			continue
		}
		summary.Name = file.Name
		summary.Valid = true
		out = append(out, summary)
	}
	return out, nil
}

// --- Trigger evaluation and run creation ------------------------------------

// evaluateRouteTriggers is called, best-effort, after a push or pull-request
// event has already been committed to the database (see ops.go and
// collaboration.go). It reads workflow files at ref, matches them against
// the event, and creates a queued run for each match. Failures are logged,
// never surfaced to the triggering request — this is planning bookkeeping,
// not a required side effect.
func (a *App) evaluateRouteTriggers(ctx context.Context, repo *Repository, actorID, ref, sha, event, detail string) {
	tree, err := a.git.Browse(ctx, repo.ID, ref, routesDir)
	if err != nil {
		return // no .gitown/workflows directory, or ref unreadable: nothing to trigger
	}
	load := a.routesGitLoader(repo.ID, ref)
	for _, entry := range tree.Entries {
		if entry.Type != "blob" || !(strings.HasSuffix(entry.Name, ".yml") || strings.HasSuffix(entry.Name, ".yaml")) {
			continue
		}
		path := routesDir + "/" + entry.Name
		data, err := load(ctx, path)
		if err != nil {
			continue
		}
		file, err := parseRoutesFile(data)
		if err != nil {
			continue
		}
		matched := (event == "push" && file.On.Push != nil && routesBranchMatches(file.On.Push.Branches, ref)) ||
			(event == "pull_request" && file.On.PullRequest != nil)
		if !matched {
			continue
		}
		if err := a.createRouteRun(ctx, repo, path, file, load, ref, sha, event, actorID, detail); err != nil {
			slog.Error("route run creation failed", "error", err, "repository", repo.Owner+"/"+repo.Name, "workflow", path)
		}
	}
}

func routesBranchMatches(branches []string, ref string) bool {
	if len(branches) == 0 {
		return true
	}
	for _, b := range branches {
		if b == ref {
			return true
		}
	}
	return false
}

func (a *App) createRouteRun(ctx context.Context, repo *Repository, path string, file *routesFile, load routesLoader, ref, sha, event, actorID, detail string) error {
	jobs, err := resolveRoutesJobs(ctx, load, path, file, map[string]bool{}, 0)
	if err != nil {
		return err
	}
	if err := a.validateRouteSecretRefs(ctx, repo, jobs); err != nil {
		return err
	}
	var queued int
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM route_runs WHERE repository_id=$1 AND status='queued'`, repo.ID).Scan(&queued); err != nil {
		return err
	}
	if queued >= routesMaxQueuedRuns {
		return errors.New("this repository already has 25 queued runs; cancel one before queueing another")
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	runID := auth.ID()
	var actor any
	if actorID != "" {
		actor = actorID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO route_runs(id,repository_id,workflow_path,workflow_name,ref,sha,trigger_event,trigger_actor_id,trigger_detail) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		runID, repo.ID, path, file.Name, ref, nullIfEmpty(sha), event, actor, detail); err != nil {
		return err
	}
	byKey := map[string]string{} // job key -> route_jobs id, for logging only
	for _, job := range jobs {
		jobID := auth.ID()
		byKey[job.Key] = jobID
		stepsJSON, err := json.Marshal(job.Steps)
		if err != nil {
			return err
		}
		matrixJSON, err := json.Marshal(job.Matrix)
		if err != nil {
			return err
		}
		status := "blocked"
		if len(job.Needs) == 0 && job.Environment == "" {
			status = "ready"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO route_jobs(id,run_id,job_key,matrix,needs,environment,timeout_minutes,steps,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			jobID, runID, job.Key, matrixJSON, job.Needs, nullIfEmpty(job.Environment), job.TimeoutMinutes, stepsJSON, status); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO route_run_logs(id,run_id,message) VALUES($1,$2,$3)`, auth.ID(), runID,
		fmt.Sprintf("Run created from %s (%s). GITOWN has planned %d job(s); execution requires a sandboxed runner, which is not yet available pending a security review.", path, event, len(jobs))); err != nil {
		return err
	}
	if err := a.fireWebhook(ctx, tx, repo.ID, repo.DistrictID, "route.run_queued", map[string]any{
		"repository": map[string]string{"owner": repo.Owner, "name": repo.Name},
		"workflow":   file.Name,
		"path":       path,
		"ref":        ref,
		"trigger":    event,
		"jobs":       len(jobs),
	}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if sha != "" {
		a.postRouteStatus(ctx, repo, sha, file.Name, "pending", "Queued — execution pending a sandboxing security review")
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// validateRouteSecretRefs checks every ${{ secrets.NAME }} reference in a
// resolved job graph names a secret that actually exists. It never reads a
// secret's value — only district-owned repositories can reference secrets
// today, reusing Phase 7's encrypted district_secrets store rather than
// adding a second one; a workflow on a personal (non-district) repository
// that references any secret fails validation with a clear message.
func (a *App) validateRouteSecretRefs(ctx context.Context, repo *Repository, jobs []resolvedJob) error {
	names := map[string]bool{}
	for _, job := range jobs {
		for _, step := range job.Steps {
			for _, m := range routesSecretRef.FindAllStringSubmatch(step.Run, -1) {
				names[m[1]] = true
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	for name := range names {
		var exists bool
		if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM route_secrets WHERE repository_id=$1 AND name=$2) OR EXISTS(SELECT 1 FROM district_secrets WHERE district_id::text=$3 AND name=$2 AND $3<>'')`, repo.ID, name, repo.DistrictID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("secret %q is not defined for this repository", name)
		}
	}
	return nil
}

func (a *App) postRouteStatus(ctx context.Context, repo *Repository, sha, workflowName, state, description string) {
	context := "routes/" + strings.ToLower(strings.ReplaceAll(workflowName, " ", "-"))
	if len(context) > 100 {
		context = context[:100]
	}
	if _, err := a.db.Exec(ctx, `INSERT INTO commit_statuses(repository_id,sha,context,state,description) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository_id,sha,context) DO UPDATE SET state=excluded.state,description=excluded.description,updated_at=now()`,
		repo.ID, sha, context, state, description); err != nil {
		slog.Error("route status post failed", "error", err)
	}
}

// --- Readiness recomputation and approvals ----------------------------------

// recomputeJobReadiness moves a job from 'blocked' to 'ready' once every
// entry in its environment's required_approvers list has at least one
// satisfied approver, or immediately if the job names no environment. Jobs
// with unmet `needs` stay 'blocked' — reaching 'ready' there would require
// an execution engine to have actually finished the dependency, which does
// not exist, so that transition intentionally never happens today.
func (a *App) recomputeJobReadiness(ctx context.Context, jobID string) error {
	var runID, environment string
	var needs []string
	var envName *string
	if err := a.db.QueryRow(ctx, `SELECT run_id,needs,environment FROM route_jobs WHERE id=$1 AND status='blocked'`, jobID).Scan(&runID, &needs, &envName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if len(needs) > 0 {
		return nil
	}
	if envName == nil {
		_, err := a.db.Exec(ctx, `UPDATE route_jobs SET status='ready' WHERE id=$1`, jobID)
		return err
	}
	environment = *envName
	var repoID string
	if err := a.db.QueryRow(ctx, `SELECT repository_id FROM route_runs WHERE id=$1`, runID).Scan(&repoID); err != nil {
		return err
	}
	var required []string
	if err := a.db.QueryRow(ctx, `SELECT required_approvers FROM route_environments WHERE repository_id=$1 AND name=$2`, repoID, environment).Scan(&required); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, execErr := a.db.Exec(ctx, `UPDATE route_jobs SET status='ready' WHERE id=$1`, jobID)
			return execErr
		}
		return err
	}
	if len(required) == 0 {
		_, err := a.db.Exec(ctx, `UPDATE route_jobs SET status='ready' WHERE id=$1`, jobID)
		return err
	}
	var satisfied bool
	if err := a.db.QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM unnest($2::text[]) AS requirement(name)
		WHERE NOT EXISTS(
			SELECT 1 FROM route_job_approvals ja JOIN users u ON u.id=ja.approver_id
			WHERE ja.job_id=$1 AND (
				(u.username=requirement.name AND (u.id=(SELECT owner_id FROM repositories WHERE id=$3) OR EXISTS(
					SELECT 1 FROM repository_members rm WHERE rm.repository_id=$3 AND rm.user_id=u.id AND rm.role IN ('write','maintain'))))
				OR EXISTS(SELECT 1 FROM crew_members cm JOIN crews c ON c.id=cm.crew_id
					WHERE cm.user_id=ja.approver_id AND c.district_id=(SELECT district_id FROM repositories WHERE id=$3)
					AND ('crew:'||c.slug)=requirement.name)
			)
		)
	)`, jobID, required, repoID).Scan(&satisfied); err != nil {
		return err
	}
	if !satisfied {
		return nil
	}
	_, err := a.db.Exec(ctx, `UPDATE route_jobs SET status='ready' WHERE id=$1`, jobID)
	return err
}

// --- HTTP handlers -----------------------------------------------------------

func (a *App) routesWorkflows(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = repo.DefaultBranch
	}
	files, err := a.listRoutesFiles(r.Context(), repo, ref)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": files})
}

func (a *App) routesRuns(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	page, perPage, ok := pageParams(w, r, 30, 100)
	if !ok {
		return
	}
	offset := (page - 1) * perPage
	rows, err := a.db.Query(r.Context(), `SELECT id,workflow_path,workflow_name,ref,COALESCE(sha,''),trigger_event,trigger_detail,status,created_at FROM route_runs WHERE repository_id=$1 ORDER BY created_at DESC OFFSET $2 LIMIT $3`, repo.ID, offset, perPage+1)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type run struct {
		ID        string    `json:"id"`
		Path      string    `json:"workflow_path"`
		Name      string    `json:"workflow_name"`
		Ref       string    `json:"ref"`
		SHA       string    `json:"sha"`
		Event     string    `json:"trigger_event"`
		Detail    string    `json:"trigger_detail"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
	}
	items := []run{}
	for rows.Next() {
		var it run
		if err = rows.Scan(&it.ID, &it.Path, &it.Name, &it.Ref, &it.SHA, &it.Event, &it.Detail, &it.Status, &it.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, it)
	}
	hasMore := len(items) > perPage
	if hasMore {
		items = items[:perPage]
	}
	respond(w, 200, map[string]any{"items": items, "has_more": hasMore})
}

func (a *App) routesRunDetail(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	runID := r.PathValue("id")
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM route_runs WHERE id=$1 AND repository_id=$2)`, runID, repo.ID).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		fail(w, 404, "not_found", "Run not found.")
		return
	}
	jobRows, err := a.db.Query(r.Context(), `SELECT id,job_key,matrix,needs,COALESCE(environment,''),timeout_minutes,steps,status FROM route_jobs WHERE run_id=$1 ORDER BY job_key`, runID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer jobRows.Close()
	type job struct {
		ID             string          `json:"id"`
		Key            string          `json:"job_key"`
		Matrix         json.RawMessage `json:"matrix"`
		Needs          []string        `json:"needs"`
		Environment    string          `json:"environment"`
		TimeoutMinutes *int            `json:"timeout_minutes"`
		Steps          json.RawMessage `json:"steps"`
		Status         string          `json:"status"`
	}
	jobs := []job{}
	for jobRows.Next() {
		var it job
		if err = jobRows.Scan(&it.ID, &it.Key, &it.Matrix, &it.Needs, &it.Environment, &it.TimeoutMinutes, &it.Steps, &it.Status); err != nil {
			serverError(w, err)
			return
		}
		jobs = append(jobs, it)
	}
	logRows, err := a.db.Query(r.Context(), `SELECT message,created_at FROM route_run_logs WHERE run_id=$1 ORDER BY created_at LIMIT 500`, runID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer logRows.Close()
	type logLine struct {
		Message   string    `json:"message"`
		CreatedAt time.Time `json:"created_at"`
	}
	logs := []logLine{}
	for logRows.Next() {
		var it logLine
		if err = logRows.Scan(&it.Message, &it.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		logs = append(logs, it)
	}
	respond(w, 200, map[string]any{"jobs": jobs, "logs": logs, "execution_enabled": false, "queue_timeout": "24h"})
}

func (a *App) dispatchRoute(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Path string `json:"path"`
		Ref  string `json:"ref"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Ref == "" {
		in.Ref = repo.DefaultBranch
	}
	if !strings.HasPrefix(in.Path, routesDir+"/") {
		fail(w, 422, "validation_failed", "Choose a workflow file under "+routesDir+"/.")
		return
	}
	data, err := a.git.Blob(r.Context(), repo.ID, in.Ref, in.Path)
	if err != nil {
		fail(w, 404, "not_found", "That workflow file could not be read at this ref.")
		return
	}
	file, err := parseRoutesFile(data)
	if err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	if file.On.WorkflowDispatch == nil {
		fail(w, 422, "validation_failed", "This workflow does not accept manual runs (add workflow_dispatch to its on: block).")
		return
	}
	u := a.user(r)
	sha, _ := a.git.Resolve(r.Context(), repo.ID, in.Ref)
	load := a.routesGitLoader(repo.ID, in.Ref)
	if err := a.createRouteRun(r.Context(), repo, in.Path, file, load, in.Ref, sha, "workflow_dispatch", u.ID, "manually run by @"+u.Username); err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	respond(w, 201, map[string]bool{"queued": true})
}

func (a *App) cancelRouteRun(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	runID := r.PathValue("id")
	tag, err := a.db.Exec(r.Context(), `UPDATE route_runs SET status='cancelled',updated_at=now() WHERE id=$1 AND repository_id=$2 AND status='queued'`, runID, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "No queued run found to cancel.")
		return
	}
	u := a.user(r)
	if _, err = a.db.Exec(r.Context(), `UPDATE route_jobs SET status='cancelled' WHERE run_id=$1 AND status IN ('blocked','ready')`, runID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO route_run_logs(id,run_id,message) VALUES($1,$2,$3)`, auth.ID(), runID, "Run cancelled by @"+u.Username+"."); err != nil {
		serverError(w, err)
		return
	}
	_ = a.fireWebhook(r.Context(), a.db, repo.ID, repo.DistrictID, "route.run_cancelled", map[string]any{"repository": map[string]string{"owner": repo.Owner, "name": repo.Name}, "run_id": runID})
	respond(w, 200, map[string]bool{"cancelled": true})
}

func (a *App) approveRouteJob(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	jobID := r.PathValue("id")
	var runRepo, status string
	var environment *string
	if err := a.db.QueryRow(r.Context(), `SELECT r.repository_id,j.environment,j.status FROM route_jobs j JOIN route_runs r ON r.id=j.run_id WHERE j.id=$1`, jobID).Scan(&runRepo, &environment, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 404, "not_found", "Job not found.")
			return
		}
		serverError(w, err)
		return
	}
	if runRepo != repo.ID {
		fail(w, 404, "not_found", "Job not found.")
		return
	}
	if environment == nil || status != "blocked" {
		fail(w, 409, "not_approvable", "This job is not waiting for an environment approval.")
		return
	}
	var required []string
	if err := a.db.QueryRow(r.Context(), `SELECT required_approvers FROM route_environments WHERE repository_id=$1 AND name=$2`, repo.ID, *environment).Scan(&required); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 409, "not_approvable", "This environment has no approval gate.")
		} else {
			serverError(w, err)
		}
		return
	}
	if len(required) == 0 {
		fail(w, 409, "not_approvable", "This environment has no required approvers.")
		return
	}
	var eligible bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM users approver WHERE approver.id=$2 AND (
			(approver.username=ANY($3::text[]) AND (approver.id=$4 OR EXISTS(
				SELECT 1 FROM repository_members rm WHERE rm.repository_id=$1 AND rm.user_id=approver.id AND rm.role IN ('write','maintain'))))
			OR EXISTS(SELECT 1 FROM crew_members cm JOIN crews c ON c.id=cm.crew_id
				WHERE cm.user_id=approver.id AND c.district_id=(SELECT district_id FROM repositories WHERE id=$1)
				AND ('crew:'||c.slug)=ANY($3::text[]))
		)
	)`, repo.ID, u.ID, required, repo.OwnerID).Scan(&eligible); err != nil {
		serverError(w, err)
		return
	}
	if !eligible {
		fail(w, 403, "not_required_approver", "You are not a required approver for this environment.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO route_job_approvals(id,job_id,approver_id) VALUES($1,$2,$3) ON CONFLICT(job_id,approver_id) DO NOTHING`, auth.ID(), jobID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := a.recomputeJobReadiness(r.Context(), jobID); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"approved": true})
}

func (a *App) routeEnvironments(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT name,required_approvers,network,cpu_millis,memory_mb,disk_mb,isolation FROM route_environments WHERE repository_id=$1 ORDER BY name`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type env struct {
		Name      string   `json:"name"`
		Approvers []string `json:"required_approvers"`
		Network   string   `json:"network"`
		CPU       int      `json:"cpu_millis"`
		MemoryMB  int      `json:"memory_mb"`
		DiskMB    int      `json:"disk_mb"`
		Isolation string   `json:"isolation"`
	}
	items := []env{}
	for rows.Next() {
		var it env
		if err = rows.Scan(&it.Name, &it.Approvers, &it.Network, &it.CPU, &it.MemoryMB, &it.DiskMB, &it.Isolation); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, it)
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) updateRouteEnvironment(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	name := strings.ToLower(r.PathValue("name"))
	if !slug.MatchString(name) {
		fail(w, 422, "validation_failed", "Environment names must be lowercase alphanumeric with hyphens.")
		return
	}
	var in struct {
		RequiredApprovers []string `json:"required_approvers"`
		Network           *string  `json:"network"`
		CPU               *int     `json:"cpu_millis"`
		MemoryMB          *int     `json:"memory_mb"`
		DiskMB            *int     `json:"disk_mb"`
	}
	if !decode(w, r, &in) {
		return
	}
	approvers, msg := a.normalizeRequiredReviewers(r.Context(), repo, in.RequiredApprovers)
	if msg != "" {
		fail(w, 422, "validation_failed", msg)
		return
	}
	network := "restricted"
	cpu, memory, disk := 1000, 512, 1024
	if in.Network != nil {
		network = *in.Network
	}
	if in.CPU != nil {
		cpu = *in.CPU
	}
	if in.MemoryMB != nil {
		memory = *in.MemoryMB
	}
	if in.DiskMB != nil {
		disk = *in.DiskMB
	}
	if msg = routeResourceCaps(network, cpu, memory, disk); msg != "" {
		fail(w, 422, "validation_failed", msg)
		return
	}
	var networkArg, cpuArg, memoryArg, diskArg any
	if in.Network != nil {
		networkArg = network
	}
	if in.CPU != nil {
		cpuArg = cpu
	}
	if in.MemoryMB != nil {
		memoryArg = memory
	}
	if in.DiskMB != nil {
		diskArg = disk
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO route_environments(id,repository_id,name,required_approvers,network,cpu_millis,memory_mb,disk_mb) VALUES($1,$2,$3,$4,COALESCE($5,'restricted'),COALESCE($6,1000),COALESCE($7,512),COALESCE($8,1024)) ON CONFLICT(repository_id,name) DO UPDATE SET required_approvers=excluded.required_approvers, network=COALESCE($5,route_environments.network), cpu_millis=COALESCE($6,route_environments.cpu_millis), memory_mb=COALESCE($7,route_environments.memory_mb), disk_mb=COALESCE($8,route_environments.disk_mb)`,
		auth.ID(), repo.ID, name, approvers, networkArg, cpuArg, memoryArg, diskArg); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"name": name, "required_approvers": approvers, "network": network, "cpu_millis": cpu, "memory_mb": memory, "disk_mb": disk, "isolation": "untrusted_execution_disabled"})
}

// --- Scheduled trigger and expiry worker -------------------------------------

func (a *App) startRoutesWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(routesWorkerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if err := a.fireDueRouteSchedules(ctx); err != nil && ctx.Err() == nil {
				slog.Error("route schedule tick failed", "error", err)
			}
			if err := a.expireStaleRouteRuns(ctx); err != nil && ctx.Err() == nil {
				slog.Error("route expiry sweep failed", "error", err)
			}
		}
	}()
}

func (a *App) fireDueRouteSchedules(ctx context.Context) error {
	now := time.Now().UTC()
	minuteStart := now.Truncate(time.Minute)
	rows, err := a.db.Query(ctx, `SELECT rs.id,rs.repository_id,rs.workflow_path,rs.cron,r.owner_id,u.username,r.name,r.default_branch,COALESCE(r.district_id::text,'')
		FROM route_schedules rs JOIN repositories r ON r.id=rs.repository_id JOIN users u ON u.id=r.owner_id
		WHERE r.deleted_at IS NULL AND r.archived_at IS NULL AND (rs.last_fired_minute IS NULL OR rs.last_fired_minute < $1)`, minuteStart)
	if err != nil {
		return err
	}
	type due struct {
		id, repoID, path, cron, ownerID, owner, name, branch, districtID string
	}
	var items []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.id, &d.repoID, &d.path, &d.cron, &d.ownerID, &d.owner, &d.name, &d.branch, &d.districtID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, d)
	}
	rows.Close()
	for _, d := range items {
		cron, err := parseRoutesCron(d.cron)
		if err != nil || !cron.matches(minuteStart) {
			continue
		}
		repo := &Repository{ID: d.repoID, OwnerID: d.ownerID, Owner: d.owner, Name: d.name, DefaultBranch: d.branch, DistrictID: d.districtID}
		data, err := a.git.Blob(ctx, repo.ID, repo.DefaultBranch, d.path)
		if err != nil {
			continue
		}
		file, err := parseRoutesFile(data)
		if err != nil || len(file.On.Schedule) == 0 {
			continue
		}
		sha, _ := a.git.Resolve(ctx, repo.ID, repo.DefaultBranch)
		load := a.routesGitLoader(repo.ID, repo.DefaultBranch)
		registered := false
		for _, item := range file.On.Schedule {
			registered = registered || item.Cron == d.cron
		}
		if !registered {
			continue
		}
		tag, err := a.db.Exec(ctx, `UPDATE route_schedules SET last_fired_minute=$1 WHERE id=$2 AND (last_fired_minute IS NULL OR last_fired_minute<$1)`, minuteStart, d.id)
		if err != nil {
			slog.Error("route schedule claim failed", "error", err)
			continue
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := a.createRouteRun(ctx, repo, d.path, file, load, repo.DefaultBranch, sha, "schedule", "", "scheduled run"); err != nil {
			slog.Error("scheduled route run failed", "error", err, "path", d.path)
		}
	}
	return nil
}

// expireStaleRouteRuns marks any run that has sat queued for more than
// routesExpireAfter as 'expired' — a cleanup behavior, not step-level
// timeout enforcement (which needs an executor GITOWN does not have).
func (a *App) expireStaleRouteRuns(ctx context.Context) error {
	rows, err := a.db.Query(ctx, `UPDATE route_runs SET status='expired',updated_at=now() WHERE status='queued' AND created_at < now() - $1::interval RETURNING id,repository_id`, fmt.Sprintf("%d seconds", int(routesExpireAfter.Seconds())))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var runID, repoID string
		if err = rows.Scan(&runID, &repoID); err != nil {
			return err
		}
		if _, err = a.db.Exec(ctx, `UPDATE route_jobs SET status='expired' WHERE run_id=$1 AND status IN ('blocked','ready')`, runID); err != nil {
			slog.Error("route job expiry failed", "error", err)
		}
		if _, err = a.db.Exec(ctx, `INSERT INTO route_run_logs(id,run_id,message) VALUES($1,$2,'Run expired after sitting unexecuted for over 24 hours.')`, auth.ID(), runID); err != nil {
			slog.Error("route expiry log failed", "error", err)
		}
	}
	return nil
}
