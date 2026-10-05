package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
)

func TestPhase11ResourceCapsRejectOpenNetwork(t *testing.T) {
	if msg := routeResourceCaps("open", 1000, 512, 1024); msg == "" {
		t.Fatal("open network must stay rejected until sandbox review")
	}
	if msg := routeResourceCaps("restricted", 1000, 512, 1024); msg != "" {
		t.Fatal(msg)
	}
	if msg := routeResourceCaps("none", 50, 512, 1024); msg == "" {
		t.Fatal("cpu below the floor must be rejected")
	}
}

func TestPhase11PlatformControls(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	t.Setenv("GITOWN_SECRET_KEY", "phase11b-test-secret-key-not-random")
	server, _, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "routeops", "email": "routeops@example.test", "password": "route-ops-password"}, 201, nil)
	var repo struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos", map[string]any{"name": "pipes", "visibility": "public", "readme": true}, 201, &repo)
	head := tip(owner, "/repos/routeops/pipes/commits?ref=main")
	workflow := "name: Check\non:\n  workflow_dispatch: {}\njobs:\n  build:\n    steps:\n      - name: stored only\n        run: echo not-executed\n"
	putFile(owner, "routeops/pipes", "main", ".gitown/workflows/check.yml", workflow, "Add a planned workflow", head)

	owner.request("PUT", "/repos/routeops/pipes/routes/environments/production", map[string]any{"required_approvers": []string{}, "network": "open", "cpu_millis": 1000, "memory_mb": 512, "disk_mb": 1024}, 422, nil)
	var env struct {
		Network   string `json:"network"`
		Isolation string `json:"isolation"`
	}
	owner.request("PUT", "/repos/routeops/pipes/routes/environments/production", map[string]any{"required_approvers": []string{}, "network": "restricted", "cpu_millis": 1000, "memory_mb": 512, "disk_mb": 1024}, 200, &env)
	if env.Network != "restricted" || env.Isolation != "untrusted_execution_disabled" {
		t.Fatalf("environment caps: %+v", env)
	}
	owner.request("PUT", "/repos/routeops/pipes/routes/secrets/BUILD_TOKEN", map[string]string{"value": "super-secret-value"}, 200, nil)
	var secrets json.RawMessage
	owner.request("GET", "/repos/routeops/pipes/routes/secrets", nil, 200, &secrets)
	if strings.Contains(string(secrets), "super-secret-value") || !strings.Contains(string(secrets), `"name":"BUILD_TOKEN"`) {
		t.Fatalf("secret value leaked or was not stored: %s", secrets)
	}

	var queued struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos/routeops/pipes/routes/dispatch", map[string]string{"path": ".gitown/workflows/check.yml", "ref": "main"}, 201, nil)
	var runs struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	owner.request("GET", "/repos/routeops/pipes/routes/runs", nil, 200, &runs)
	if len(runs.Items) != 1 || runs.Items[0].Status != "queued" {
		t.Fatalf("manual run was not queued: %+v", runs.Items)
	}
	queued.ID = runs.Items[0].ID
	var detail struct {
		Execution bool   `json:"execution_enabled"`
		Timeout   string `json:"queue_timeout"`
	}
	owner.request("GET", "/repos/routeops/pipes/routes/runs/"+queued.ID, nil, 200, &detail)
	if detail.Execution || detail.Timeout != "24h" {
		t.Fatalf("run detail must keep execution off: %+v", detail)
	}
	payload := base64.StdEncoding.EncodeToString([]byte("planned-log"))
	var artifact struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos/routeops/pipes/routes/runs/"+queued.ID+"/artifacts", map[string]string{"name": "planned-log", "content_base64": payload}, 201, &artifact)
	owner.request("PUT", "/repos/routeops/pipes/routes/caches/go-mod", map[string]string{"content_base64": payload}, 200, nil)
	owner.request("POST", "/repos/routeops/pipes/routes/runs/"+queued.ID+"/logs", map[string]string{"message": "planning note only"}, 201, nil)

	body := rawRequest(t, server.URL, "GET", "/api/v1/repos/routeops/pipes/routes/artifacts/"+artifact.ID, owner.client, "")
	if string(body) != "planned-log" {
		t.Fatalf("artifact bytes: %q", body)
	}

	var runner struct {
		Token     string `json:"token"`
		Execution bool   `json:"execution_enabled"`
	}
	owner.request("POST", "/repos/routeops/pipes/routes/runners", map[string]any{"name": "bench", "labels": []string{"linux"}, "cpu_millis": 1000, "memory_mb": 512, "disk_mb": 1024, "network": "restricted"}, 201, &runner)
	if runner.Execution || !strings.HasPrefix(runner.Token, "rnr_") {
		t.Fatalf("runner registration: %+v", runner)
	}
	heartbeat := rawRequest(t, server.URL, "POST", "/api/v1/routes/runners/heartbeat", &http.Client{}, "Bearer "+runner.Token)
	if strings.Contains(string(heartbeat), `"jobs":[]`) == false || strings.Contains(string(heartbeat), `"execution_enabled":false`) == false {
		t.Fatalf("heartbeat dispatched work: %s", heartbeat)
	}
	var listed struct {
		Execution bool `json:"execution_enabled"`
		Items     []struct {
			Name      string `json:"name"`
			Execution bool   `json:"execution_enabled"`
		} `json:"items"`
	}
	owner.request("GET", "/repos/routeops/pipes/routes/runners", nil, 200, &listed)
	if listed.Execution {
		t.Fatal("runner list enabled execution")
	}
	hosted := false
	for _, item := range listed.Items {
		if item.Name == "gitown-hosted" {
			hosted = true
			if item.Execution {
				t.Fatal("hosted runner must stay offline")
			}
		}
	}
	if !hosted {
		t.Fatalf("hosted runner missing: %+v", listed.Items)
	}
}

func tip(c testClient, path string) string {
	c.t.Helper()
	var commits []gitstore.Commit
	c.request("GET", path, nil, 200, &commits)
	if len(commits) == 0 || len(commits[0].SHA) != 40 {
		c.t.Fatalf("missing commit at %s: %+v", path, commits)
	}
	return commits[0].SHA
}

func putFile(c testClient, repo, branch, path, content, message, expected string) string {
	c.t.Helper()
	var saved struct {
		SHA string `json:"sha"`
	}
	c.request("PUT", "/repos/"+repo+"/contents", map[string]string{
		"branch": branch, "path": path, "content": content, "message": message, "expected_head": expected,
	}, 201, &saved)
	if len(saved.SHA) != 40 {
		c.t.Fatalf("commit did not return a sha: %+v", saved)
	}
	return saved.SHA
}

func pointBranch(t *testing.T, application *App, repoID, name, sha string) {
	t.Helper()
	out, code, err := application.git.Command(context.Background(), 20*time.Second, repoID, "update-ref", "refs/heads/"+name, sha)
	if err != nil || code != 0 {
		t.Fatalf("update-ref %s: %v %s", name, err, out)
	}
}

func rawRequest(t *testing.T, base, method, path string, client *http.Client, authorization string) []byte {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 && res.StatusCode != 201 {
		t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, body)
	}
	return body
}
