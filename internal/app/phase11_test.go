package app

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/config"
	"github.com/Aaravkhanal/GITOWN/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPhase11NoExecutionOfWorkflowContent is a safety-net test proving no
// new exec.Command/os/exec call site exists anywhere in this package beyond
// the pre-existing, fixed git/ssh-keygen invocations that predate Phase 11.
// Routes must never grow a call site that runs workflow-file-derived
// content.
func TestPhase11NoExecutionOfWorkflowContent(t *testing.T) {
	allowed := map[string]bool{
		"transport.go":                true,
		"sshgate.go":                  true,
		"signing.go":                  true,
		"receive_guard_test.go":       true, // test-only, spawns git for setup
		"collaboration_depth_test.go": true,
		"integration_test.go":         true,
		"phase3_test.go":              true,
		"phase4_test.go":              true,
		"phase5_test.go":              true,
		"phase8_test.go":              true,
		"phase69_test.go":             true,
		"phase9_test.go":              true,
		"phase_finish_test.go":        true,
		"phase11_test.go":             true, // this file, for its own git-push helper below
	}
	execPattern := regexp.MustCompile(`\bexec\.Command\b|\bexec\.CommandContext\b|"os/exec"`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if execPattern.Match(data) && !allowed[name] {
			t.Errorf("%s references exec.Command/os-exec but is not in the allowed list — Routes must never execute workflow content", name)
		}
	}
	if _, ok := allowed["routes.go"]; ok {
		t.Fatal("routes.go must never be allowed to call exec.Command")
	}
}

func TestPhase11ParseWorkflowValidation(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{"valid minimal", "name: CI\non:\n  push: {}\njobs:\n  build:\n    steps:\n      - name: test\n        run: echo hi\n", false},
		{"missing name", "on:\n  push: {}\njobs:\n  build:\n    steps: [{run: echo hi}]\n", true},
		{"no jobs", "name: CI\non:\n  push: {}\njobs: {}\n", true},
		{"unknown need", "name: CI\non:\n  push: {}\njobs:\n  build:\n    needs: [missing]\n    steps: [{run: echo hi}]\n", true},
		{"cycle", "name: CI\non:\n  push: {}\njobs:\n  a:\n    needs: [b]\n    steps: [{run: x}]\n  b:\n    needs: [a]\n    steps: [{run: x}]\n", true},
		{"both uses and steps", "name: CI\non:\n  push: {}\njobs:\n  a:\n    uses: ./.gitown/workflows/x.yml\n    steps: [{run: x}]\n", true},
		{"invalid cron", "name: CI\non:\n  schedule:\n    - cron: \"bad\"\njobs:\n  a:\n    steps: [{run: x}]\n", true},
		{"matrix too big", "name: CI\non:\n  push: {}\njobs:\n  a:\n    strategy:\n      matrix:\n        x: [1,2,3,4,5,6,7,8]\n        y: [1,2,3,4,5,6,7,8]\n        z: [1,2]\n    steps: [{run: x}]\n", true},
		{"unknown field rejected", "name: CI\non:\n  push: {}\njobs:\n  a:\n    steps: [{run: x}]\nunknown_top_level: true\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseRoutesFile([]byte(tc.yaml))
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseRoutesFile(%q): err=%v, wantErr=%v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestPhase11MatrixExpansion(t *testing.T) {
	job := routesJobDef{Steps: []routesStepDef{{Run: "x"}}, Strategy: &routesStrategy{Matrix: map[string][]string{
		"os":      {"linux", "macos"},
		"version": {"1", "2"},
	}}}
	instances, err := expandRoutesMatrix("build", job)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 4 {
		t.Fatalf("expected 4 matrix instances, got %d", len(instances))
	}
	seen := map[string]bool{}
	for _, inst := range instances {
		seen[inst.Matrix["os"]+"/"+inst.Matrix["version"]] = true
	}
	for _, want := range []string{"linux/1", "linux/2", "macos/1", "macos/2"} {
		if !seen[want] {
			t.Fatalf("missing combination %s", want)
		}
	}
}

func TestPhase11ReusableWorkflowResolution(t *testing.T) {
	shared := `name: Shared
on:
  push: {}
jobs:
  lint:
    steps:
      - run: echo lint
`
	parent := `name: Parent
on:
  push: {}
jobs:
  reused:
    uses: ./.gitown/workflows/shared.yml
  direct:
    needs: [reused]
    steps:
      - run: echo direct
`
	files := map[string][]byte{
		".gitown/workflows/shared.yml": []byte(shared),
		".gitown/workflows/parent.yml": []byte(parent),
	}
	load := func(_ context.Context, path string) ([]byte, error) { return files[path], nil }
	file, err := parseRoutesFile(files[".gitown/workflows/parent.yml"])
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := resolveRoutesJobs(context.Background(), load, ".gitown/workflows/parent.yml", file, map[string]bool{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, j := range jobs {
		keys[j.Key] = true
	}
	if !keys["reused/lint"] {
		t.Fatalf("expected reused/lint job, got %+v", keys)
	}
	if !keys["direct"] {
		t.Fatalf("expected direct job, got %+v", keys)
	}
	for _, j := range jobs {
		if j.Key == "direct" {
			if len(j.Needs) != 1 || j.Needs[0] != "reused/lint" {
				t.Fatalf("expected direct's needs to be rewritten to [reused/lint], got %v", j.Needs)
			}
		}
	}
}

func TestPhase11ReusableWorkflowCycleRejected(t *testing.T) {
	a := `name: A
on: {push: {}}
jobs:
  x:
    uses: ./.gitown/workflows/b.yml
`
	b := `name: B
on: {push: {}}
jobs:
  y:
    uses: ./.gitown/workflows/a.yml
`
	files := map[string][]byte{
		".gitown/workflows/a.yml": []byte(a),
		".gitown/workflows/b.yml": []byte(b),
	}
	load := func(_ context.Context, path string) ([]byte, error) { return files[path], nil }
	file, err := parseRoutesFile(files[".gitown/workflows/a.yml"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRoutesJobs(context.Background(), load, ".gitown/workflows/a.yml", file, map[string]bool{}, 0); err == nil {
		t.Fatal("expected a cycle error, got none")
	}
}

func TestPhase11CronMatching(t *testing.T) {
	c, err := parseRoutesCron("30 9 * * 1")
	if err != nil {
		t.Fatal(err)
	}
	mondayNineThirty := time.Date(2026, time.September, 28, 9, 30, 0, 0, time.UTC) // a Monday
	if !c.matches(mondayNineThirty) {
		t.Fatal("expected cron to match Monday 09:30 UTC")
	}
	tuesdaySameTime := mondayNineThirty.AddDate(0, 0, 1)
	if c.matches(tuesdaySameTime) {
		t.Fatal("cron should not match Tuesday")
	}
	if _, err := parseRoutesCron("* * * *"); err == nil {
		t.Fatal("expected an error for a 4-field cron expression")
	}
	if _, err := parseRoutesCron("60 * * * *"); err == nil {
		t.Fatal("expected an error for an out-of-range minute")
	}
}

// TestPhase11PushTriggerCreatesRun drives a real `git push` of a workflow
// file to a test server and asserts a run is queued with the resolved job
// graph — proving the push-trigger wiring in ops.go actually works, not
// just the pure parsing functions above.
func TestPhase11PushTriggerCreatesRun(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	application, err := New(config.Config{DataDir: storage, Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true}, pool)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "routeowner", "email": "routeowner@example.test", "password": "route-owner-password"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "ciproj", "visibility": "public", "readme": true}, 201, nil)

	writeToken := owner.token("repo:write")
	repoURL := server.URL + "/git/routeowner/ciproj.git"
	work := filepath.Join(t.TempDir(), "work")
	gitEnv := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	gitRun := func(dir, token string, ok bool, args ...string) string {
		t.Helper()
		argv := []string{"-c", "credential.helper="}
		if token != "" {
			argv = append(argv, "-c", "http.extraHeader=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("routeowner:"+token)))
		}
		cmd := exec.Command("git", append(argv, args...)...)
		cmd.Dir = dir
		cmd.Env = gitEnv
		out, gitErr := cmd.CombinedOutput()
		if ok && gitErr != nil {
			t.Fatalf("git %v failed: %s", args, out)
		}
		return string(out)
	}
	gitRun("", writeToken, true, "clone", repoURL, work)
	gitRun(work, "", true, "config", "user.name", "Route Owner")
	gitRun(work, "", true, "config", "user.email", "routeowner@example.test")

	if err := os.MkdirAll(filepath.Join(work, ".gitown/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := `name: Continuous Integration
on:
  push:
    branches: [main]
  workflow_dispatch: {}
jobs:
  build:
    steps:
      - name: run tests
        run: echo not actually executed
  deploy:
    needs: [build]
    environment: production
    steps:
      - run: echo also not executed
`
	if err := os.WriteFile(filepath.Join(work, ".gitown/workflows/ci.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(work, "", true, "add", ".")
	gitRun(work, "", true, "commit", "-m", "add workflow")
	gitRun(work, writeToken, true, "push", "origin", "HEAD:main")

	// The push handler fires evaluateRouteTriggers synchronously as part of
	// finishReceive, so the run should already exist.
	var runs struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Path   string `json:"workflow_path"`
		} `json:"items"`
	}
	owner.request("GET", "/repos/routeowner/ciproj/routes/runs", nil, 200, &runs)
	if len(runs.Items) != 1 {
		t.Fatalf("expected exactly 1 route run from the push, got %d: %+v", len(runs.Items), runs.Items)
	}
	if runs.Items[0].Status != "queued" {
		t.Fatalf("expected the run to be queued (no executor exists), got %q", runs.Items[0].Status)
	}

	var detail struct {
		Jobs []struct {
			JobKey      string   `json:"job_key"`
			Status      string   `json:"status"`
			Needs       []string `json:"needs"`
			Environment string   `json:"environment"`
		} `json:"jobs"`
	}
	owner.request("GET", "/repos/routeowner/ciproj/routes/runs/"+runs.Items[0].ID, nil, 200, &detail)
	if len(detail.Jobs) != 2 {
		t.Fatalf("expected 2 resolved jobs (build, deploy), got %d", len(detail.Jobs))
	}
	var build, deploy *struct {
		JobKey      string   `json:"job_key"`
		Status      string   `json:"status"`
		Needs       []string `json:"needs"`
		Environment string   `json:"environment"`
	}
	for i := range detail.Jobs {
		switch detail.Jobs[i].JobKey {
		case "build":
			build = &detail.Jobs[i]
		case "deploy":
			deploy = &detail.Jobs[i]
		}
	}
	if build == nil || build.Status != "ready" {
		t.Fatalf("expected job 'build' to be ready (no needs, no environment), got %+v", build)
	}
	if deploy == nil || deploy.Status != "blocked" {
		t.Fatalf("expected job 'deploy' to be blocked (needs build, has environment), got %+v", deploy)
	}

	// Cancel the run and confirm it and its jobs move to 'cancelled'.
	owner.request("POST", "/repos/routeowner/ciproj/routes/runs/"+runs.Items[0].ID+"/cancel", nil, 200, nil)
	owner.request("GET", "/repos/routeowner/ciproj/routes/runs", nil, 200, &runs)
	if runs.Items[0].Status != "cancelled" {
		t.Fatalf("expected the run to be cancelled, got %q", runs.Items[0].Status)
	}
}

// TestPhase11EnvironmentApprovalGatesJob proves a job naming a protected
// environment stays blocked until a required approver actually approves it,
// and that an unlisted user's approval does not satisfy the gate.
func TestPhase11EnvironmentApprovalGatesJob(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	application, err := New(config.Config{DataDir: storage, Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true}, pool)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jarOwner, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jarOwner}}
	jarApprover, _ := cookiejar.New(nil)
	approver := testClient{t, server.URL, &http.Client{Jar: jarApprover}}
	jarOutsider, _ := cookiejar.New(nil)
	outsider := testClient{t, server.URL, &http.Client{Jar: jarOutsider}}
	owner.request("POST", "/auth/register", map[string]string{"username": "envowner", "email": "envowner@example.test", "password": "env-owner-password"}, 201, nil)
	approver.request("POST", "/auth/register", map[string]string{"username": "envapprover", "email": "envapprover@example.test", "password": "env-approver-password"}, 201, nil)
	outsider.request("POST", "/auth/register", map[string]string{"username": "envoutsider", "email": "envoutsider@example.test", "password": "env-outsider-password"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "gated", "visibility": "public", "readme": true}, 201, nil)
	owner.request("POST", "/repos/envowner/gated/invitations", map[string]string{"username": "envapprover", "role": "write"}, 201, nil)
	var invites []struct {
		ID string `json:"id"`
	}
	approver.request("GET", "/user/invitations", nil, 200, &invites)
	if len(invites) != 1 {
		t.Fatalf("expected 1 pending invitation, got %d", len(invites))
	}
	approver.request("POST", "/user/invitations/"+invites[0].ID+"/accept", nil, 200, nil)

	owner.request("PUT", "/repos/envowner/gated/routes/environments/production", map[string]any{"required_approvers": []string{"envapprover", "envowner"}}, 200, nil)

	var repoID string
	if err := pool.QueryRow(ctx, `SELECT id FROM repositories WHERE name='gated' AND owner_id=(SELECT id FROM users WHERE username='envowner')`).Scan(&repoID); err != nil {
		t.Fatal(err)
	}
	runID := auth.ID()
	jobID := auth.ID()
	if _, err := pool.Exec(ctx, `INSERT INTO route_runs(id,repository_id,workflow_path,workflow_name,ref,trigger_event) VALUES($1,$2,'.gitown/workflows/deploy.yml','Deploy','main','workflow_dispatch')`, runID, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO route_jobs(id,run_id,job_key,environment,status) VALUES($1,$2,'deploy','production','blocked')`, jobID, runID); err != nil {
		t.Fatal(err)
	}

	outsider.request("POST", "/repos/envowner/gated/routes/jobs/"+jobID+"/approve", nil, 403, nil)
	var stillBlocked struct {
		Jobs []struct {
			Status string `json:"status"`
		} `json:"jobs"`
	}
	owner.request("GET", "/repos/envowner/gated/routes/runs/"+runID, nil, 200, &stillBlocked)
	if stillBlocked.Jobs[0].Status != "blocked" {
		t.Fatalf("an outsider's approval must not satisfy the gate, got status %q", stillBlocked.Jobs[0].Status)
	}

	approver.request("POST", "/repos/envowner/gated/routes/jobs/"+jobID+"/approve", nil, 200, nil)
	owner.request("GET", "/repos/envowner/gated/routes/runs/"+runID, nil, 200, &stillBlocked)
	if stillBlocked.Jobs[0].Status != "blocked" {
		t.Fatalf("one of two required approvals must not release the gate, got %q", stillBlocked.Jobs[0].Status)
	}
	owner.request("POST", "/repos/envowner/gated/routes/jobs/"+jobID+"/approve", nil, 200, nil)
	var nowReady struct {
		Jobs []struct {
			Status string `json:"status"`
		} `json:"jobs"`
	}
	owner.request("GET", "/repos/envowner/gated/routes/runs/"+runID, nil, 200, &nowReady)
	if nowReady.Jobs[0].Status != "ready" {
		t.Fatalf("expected the listed approver to satisfy the gate, got status %q", nowReady.Jobs[0].Status)
	}
}
