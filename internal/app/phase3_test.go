package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/config"
	"github.com/Aaravkhanal/GITOWN/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMain lets the test binary act as the SSH program for real Git clients:
// with GITOWN_SSH_HELPER set it runs the forced-command gateway instead of tests.
func TestMain(m *testing.M) {
	if os.Getenv("GITOWN_SSH_HELPER") == "1" {
		os.Exit(runSSHHelper())
	}
	os.Exit(m.Run())
}

func runSSHHelper() int {
	// GIT_SSH_VARIANT=simple passes exactly the host and the remote command.
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "ssh helper: missing command")
		return 2
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = os.Getenv("GITOWN_SSH_HELPER_SCHEMA")
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer pool.Close()
	application, err := New(config.Config{DataDir: os.Getenv("GITOWN_SSH_HELPER_DATA"), Origin: "http://localhost:3000", GitURL: "http://localhost/git"}, pool)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err = application.SSHSession(ctx, os.Getenv("GITOWN_SSH_FINGERPRINT"), os.Args[len(os.Args)-1], os.Stdin, os.Stdout, os.Stderr); err != nil {
		return 1
	}
	return 0
}

func inviteAndAccept(owner, invitee testClient, repository, username, role string) RepositoryMember {
	owner.t.Helper()
	var invitation Invitation
	owner.request("POST", "/repos/"+repository+"/invitations", map[string]string{"username": username, "role": role}, 201, &invitation)
	invitee.request("POST", "/user/invitations/"+invitation.ID+"/accept", map[string]any{}, 200, nil)
	var members []RepositoryMember
	owner.request("GET", "/repos/"+repository+"/members", nil, 200, &members)
	for _, member := range members {
		if member.Username == username {
			return member
		}
	}
	owner.t.Fatalf("%s was not added to %s: %+v", username, repository, members)
	return RepositoryMember{}
}

func statusRequest(t *testing.T, serverURL, token, path string, body any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", serverURL+"/api/v1"+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(payload)
}

func TestPhaseThreeCollaboration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	for _, tool := range []string{"ssh-keygen", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required", tool)
		}
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "gitown_test_" + strings.ReplaceAll(auth.ID(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	a, err := New(config.Config{DataDir: storage, Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true}, pool)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := func() testClient {
		jar, _ := cookiejar.New(nil)
		return testClient{t, server.URL, &http.Client{Jar: jar}}
	}
	owner, other, third := client(), client(), client()
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	other.request("POST", "/auth/register", map[string]string{"username": "other", "email": "other@example.test", "password": "other-long-password", "display_name": "Other"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "forge", "visibility": "public", "readme": true}, 201, nil)

	// Invitations replace silent membership changes.
	owner.request("POST", "/repos/owner/forge/members", map[string]string{"username": "other", "role": "write"}, 404, nil)
	var invite Invitation
	owner.request("POST", "/repos/owner/forge/invitations", map[string]string{"username": "other", "role": "write"}, 201, &invite)
	if invite.AcceptURL != "" {
		t.Fatalf("account invitations must not expose a token link: %+v", invite)
	}
	owner.request("POST", "/repos/owner/forge/invitations", map[string]string{"email": "other@example.test", "role": "read"}, 409, nil)
	owner.request("DELETE", "/repos/owner/forge/invitations/"+invite.ID, nil, 200, nil)
	other.request("POST", "/user/invitations/"+invite.ID+"/accept", map[string]any{}, 409, nil)
	member := inviteAndAccept(owner, other, "owner/forge", "other", "write")
	if member.Role != "write" {
		t.Fatalf("member: %+v", member)
	}
	owner.request("POST", "/repos/owner/forge/invitations", map[string]string{"username": "other", "role": "read"}, 409, nil)
	owner.request("POST", "/repos/owner/forge/invitations", map[string]string{"username": "owner", "role": "read"}, 422, nil)
	var emailInvite Invitation
	owner.request("POST", "/repos/owner/forge/invitations", map[string]string{"email": "third@example.test", "role": "read"}, 201, &emailInvite)
	link, err := url.Parse(emailInvite.AcceptURL)
	if err != nil || link.Path != "/invitations/accept" || !strings.HasPrefix(link.Query().Get("token"), "inv_") {
		t.Fatalf("email invitation link: %q", emailInvite.AcceptURL)
	}
	third.request("POST", "/auth/register", map[string]string{"username": "third", "email": "third@example.test", "password": "third-long-password", "display_name": "Third"}, 201, nil)
	var thirdInvites []Invitation
	third.request("GET", "/user/invitations", nil, 200, &thirdInvites)
	if len(thirdInvites) != 0 {
		t.Fatalf("an unverified email address exposed an invitation: %+v", thirdInvites)
	}
	third.request("POST", "/user/invitations/"+emailInvite.ID+"/accept", map[string]any{}, 403, nil)
	third.request("POST", "/invitations/accept", map[string]string{"token": "inv_wrong"}, 404, nil)
	third.request("POST", "/invitations/accept", map[string]string{"token": link.Query().Get("token")}, 200, nil)
	third.request("POST", "/invitations/accept", map[string]string{"token": link.Query().Get("token")}, 409, nil)

	// The capability matrix: maintainers run the project, managers own access.
	owner.request("PATCH", "/repos/owner/forge/members/third", map[string]string{"role": "maintain"}, 200, nil)
	var permissions RepositoryPermissions
	third.request("GET", "/repos/owner/forge/permissions", nil, 200, &permissions)
	if !permissions.Maintain || permissions.Manage || permissions.Role != "maintain" {
		t.Fatalf("maintainer permissions: %+v", permissions)
	}
	third.request("PUT", "/repos/owner/forge/branch-rules?branch=main", map[string]any{"required_approvals": 0}, 200, nil)
	third.request("PATCH", "/repos/owner/forge", map[string]string{"visibility": "private"}, 403, nil)
	third.request("GET", "/repos/owner/forge/members", nil, 403, nil)
	other.request("PUT", "/repos/owner/forge/branch-rules?branch=main", map[string]any{"required_approvals": 0}, 403, nil)
	owner.request("PATCH", "/repos/owner/forge/members/third", map[string]string{"role": "read"}, 200, nil)

	// Ownership transfer needs confirmation, can be cancelled, and never overwrites.
	owner.request("POST", "/repos", map[string]any{"name": "handoff", "visibility": "public", "readme": true}, 201, nil)
	other.request("POST", "/repos", map[string]any{"name": "forge", "visibility": "public"}, 201, nil)
	owner.request("POST", "/repos/owner/forge/transfer", map[string]string{"username": "other", "confirm": "owner/forge"}, 409, nil)
	owner.request("POST", "/repos/owner/handoff/transfer", map[string]string{"username": "other"}, 422, nil)
	owner.request("POST", "/repos/owner/handoff/transfer", map[string]string{"username": "other", "confirm": "owner/handoff"}, 201, nil)
	var pending struct {
		Pending *struct {
			Username string `json:"username"`
		} `json:"pending"`
	}
	owner.request("GET", "/repos/owner/handoff/transfer", nil, 200, &pending)
	if pending.Pending == nil || pending.Pending.Username != "other" {
		t.Fatalf("pending transfer: %+v", pending)
	}
	owner.request("DELETE", "/repos/owner/handoff/transfer", nil, 200, nil)
	owner.request("GET", "/repos/owner/handoff/transfer", nil, 200, &pending)
	if pending.Pending != nil {
		t.Fatal("cancelled transfer is still pending")
	}
	var created struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos/owner/handoff/transfer", map[string]string{"username": "other", "confirm": "owner/handoff"}, 201, &created)
	if _, err = pool.Exec(ctx, `UPDATE repositories SET owner_id=(SELECT id FROM users WHERE username='third') WHERE name='handoff'`); err != nil {
		t.Fatal(err)
	}
	other.request("POST", "/user/transfers/"+created.ID+"/accept", map[string]any{}, 409, nil)
	if _, err = pool.Exec(ctx, `UPDATE repositories SET owner_id=(SELECT id FROM users WHERE username='owner') WHERE name='handoff'`); err != nil {
		t.Fatal(err)
	}
	owner.request("POST", "/repos/owner/handoff/transfer", map[string]string{"username": "other", "confirm": "owner/handoff"}, 201, &created)
	other.request("POST", "/user/transfers/"+created.ID+"/accept", map[string]any{}, 200, nil)
	other.request("GET", "/repos/other/handoff", nil, 200, nil)

	// Real Git work over HTTPS.
	writeToken := owner.token("repo:write")
	readToken := owner.token("repo:read")
	repoURL := server.URL + "/git/owner/forge.git"
	work := filepath.Join(t.TempDir(), "work")
	gitEnv := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	gitRun := func(dir, token string, ok bool, args ...string) string {
		t.Helper()
		argv := []string{"-c", "credential.helper="}
		if token != "" {
			argv = append(argv, "-c", "http.extraHeader=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("owner:"+token)))
		}
		cmd := exec.Command("git", append(argv, args...)...)
		cmd.Dir = dir
		cmd.Env = gitEnv
		out, gitErr := cmd.CombinedOutput()
		if ok && gitErr != nil {
			t.Fatalf("git %v failed: %s", args, out)
		}
		if !ok && gitErr == nil {
			t.Fatalf("git %v unexpectedly succeeded: %s", args, out)
		}
		return string(out)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var issue Issue
	owner.request("POST", "/repos/owner/forge/issues", map[string]string{"title": "Ship the feature"}, 201, &issue)
	gitRun("", writeToken, true, "clone", repoURL, work)
	gitRun(work, "", true, "config", "user.name", "Owner")
	gitRun(work, "", true, "config", "user.email", "owner@example.test")
	gitRun(work, "", true, "checkout", "-b", "feature")
	write("feature.txt", "one\ntwo\nthree\n")
	gitRun(work, "", true, "add", "feature.txt")
	gitRun(work, "", true, "commit", "-m", "Add the feature", "-m", fmt.Sprintf("Fixes #%d", issue.Number))
	gitRun(work, writeToken, true, "push", "-u", "origin", "feature")
	// Unprotected branches accept force pushes and deletions.
	gitRun(work, writeToken, true, "push", "origin", "HEAD:refs/heads/scratch")
	gitRun(work, writeToken, true, "push", "--force", "origin", "HEAD~1:refs/heads/scratch")
	gitRun(work, writeToken, true, "push", "origin", "--delete", "scratch")

	var pull Pull
	owner.request("POST", "/repos/owner/forge/pulls", map[string]string{"title": "Feature", "base_branch": "main", "head_branch": "feature"}, 201, &pull)
	pullPath := fmt.Sprintf("/repos/owner/forge/pulls/%d", pull.Number)
	var detail struct {
		HeadSHA string `json:"head_sha"`
		BaseSHA string `json:"base_sha"`
	}
	owner.request("GET", pullPath, nil, 200, &detail)

	// Inline comments must land on lines in the diff.
	thread := func(client testClient, line, status int) ReviewThread {
		t.Helper()
		var item ReviewThread
		client.request("POST", pullPath+"/threads", map[string]any{"commit_sha": detail.HeadSHA, "path": "feature.txt", "side": "right", "line": line, "body": "Please check this line."}, status, &item)
		return item
	}
	thread(owner, 99, 422)
	owner.request("POST", pullPath+"/threads", map[string]any{"commit_sha": detail.HeadSHA, "path": "feature.txt", "side": "left", "line": 1, "body": "No old side."}, 422, nil)
	ownerThread := thread(owner, 2, 201)
	thirdThread := thread(third, 1, 201)
	third.request("POST", pullPath+"/threads/"+thirdThread.ID+"/resolve", map[string]bool{"resolved": true}, 200, nil)
	third.request("POST", pullPath+"/threads/"+ownerThread.ID+"/resolve", map[string]bool{"resolved": true}, 403, nil)
	other.request("POST", pullPath+"/threads/"+ownerThread.ID+"/replies", map[string]string{"body": "Looks right to me."}, 201, nil)
	var replies []ThreadReply
	owner.request("GET", pullPath+"/threads/"+ownerThread.ID+"/replies", nil, 200, &replies)
	if len(replies) != 1 || replies[0].Author != "other" || replies[0].Body != "Looks right to me." {
		t.Fatalf("thread replies: %+v", replies)
	}
	owner.request("POST", pullPath+"/comments", map[string]string{"body": "Ready when checks pass."}, 201, nil)

	// CI reports checks with a token, without browser credentials.
	statusPath := "/repos/owner/forge/commits/" + detail.HeadSHA + "/status"
	if code, body := statusRequest(t, server.URL, readToken, statusPath, map[string]string{"context": "ci", "state": "success"}); code != 403 {
		t.Fatalf("read token reported a status: %d %s", code, body)
	}
	if code, body := statusRequest(t, server.URL, writeToken, statusPath, map[string]string{"context": "ci", "state": "success", "target_url": "javascript:alert(1)"}); code != 422 {
		t.Fatalf("unsafe target URL accepted: %d %s", code, body)
	}
	if code, body := statusRequest(t, server.URL, writeToken, statusPath, map[string]string{"context": "ci", "state": "success", "target_url": "https://ci.example.test/run/1"}); code != 200 {
		t.Fatalf("token status report: %d %s", code, body)
	}
	var statuses []map[string]any
	owner.request("GET", "/repos/owner/forge/commits/"+detail.HeadSHA+"/statuses", nil, 200, &statuses)
	if len(statuses) != 1 || statuses[0]["target_url"] != "https://ci.example.test/run/1" || statuses[0]["reporter"] != "owner" {
		t.Fatalf("statuses: %+v", statuses)
	}

	// Branch rules apply to API merges.
	owner.request("PUT", "/repos/owner/forge/branch-rules?branch=main", map[string]any{"required_approvals": 0, "required_reviewers": []string{"nobody"}}, 422, nil)
	owner.request("PUT", "/repos/owner/forge/branch-rules?branch=main", map[string]any{"required_approvals": 0, "require_resolved": true, "restrict_push": true, "required_checks": []string{"ci"}, "required_reviewers": []string{"@other"}}, 200, nil)
	owner.request("PUT", "/repos/owner/forge/branch-rules?branch=feature", map[string]any{"required_approvals": 0}, 200, nil)
	merge := map[string]any{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA, "method": "merge", "delete_branch": true}
	other.request("POST", pullPath+"/merge", merge, 403, nil)
	owner.request("POST", pullPath+"/merge", merge, 409, nil)
	owner.request("POST", pullPath+"/threads/"+ownerThread.ID+"/resolve", map[string]bool{"resolved": true}, 200, nil)
	var blocked struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	owner.request("POST", pullPath+"/merge", merge, 409, &blocked)
	if blocked.Error.Code != "required_reviewer" {
		t.Fatalf("merge without the required reviewer: %+v", blocked)
	}
	other.request("POST", pullPath+"/reviews", map[string]string{"state": "approved", "body": "Ship it.", "head_sha": detail.HeadSHA}, 201, nil)
	var merged Pull
	owner.request("POST", pullPath+"/merge", merge, 200, &merged)
	if merged.State != "merged" || merged.BranchDeleted == nil || *merged.BranchDeleted || merged.BranchDeleteError == "" {
		t.Fatalf("protected source branch handling: %+v", merged)
	}
	var closed Issue
	if err = pool.QueryRow(ctx, `SELECT state FROM issues WHERE number=$1 AND repository_id=(SELECT id FROM repositories WHERE name='forge' AND owner_id=(SELECT id FROM users WHERE username='owner'))`, issue.Number).Scan(&closed.State); err != nil || closed.State != "closed" {
		t.Fatalf("commit message keyword did not close the issue on a merge commit: %q %v", closed.State, err)
	}

	var timeline struct {
		Items []TimelineItem `json:"items"`
	}
	owner.request("GET", pullPath+"/timeline", nil, 200, &timeline)
	kinds := map[string]int{}
	for _, item := range timeline.Items {
		kinds[item.Kind]++
	}
	if kinds["comment"] != 1 || kinds["inline"] != 2 || kinds["review.approved"] != 1 || kinds["reply"] != 1 || kinds["check.success"] != 1 || kinds["merged"] != 1 || kinds["resolved"] != 2 {
		t.Fatalf("timeline kinds: %+v", kinds)
	}

	// Signed-commit rules apply to pushes and browser edits alike.
	gitRun(work, "", true, "checkout", "-b", "signed", "origin/main")
	gitRun(work, writeToken, true, "push", "origin", "signed")
	owner.request("PUT", "/repos/owner/forge/branch-rules?branch=signed", map[string]any{"required_approvals": 0, "require_signed": true}, 200, nil)
	var tree struct {
		SHA string `json:"sha"`
	}
	owner.request("GET", "/repos/owner/forge/tree?ref=signed", nil, 200, &tree)
	owner.request("PUT", "/repos/owner/forge/contents", map[string]string{"branch": "signed", "path": "web.txt", "content": "web\n", "message": "Browser edit", "expected_head": tree.SHA}, 409, nil)
	write("signed.txt", "unsigned\n")
	gitRun(work, "", true, "add", "signed.txt")
	gitRun(work, "", true, "commit", "-m", "Unsigned change")
	if out := gitRun(work, writeToken, false, "push", "origin", "signed"); !strings.Contains(out, "verified signature") {
		t.Fatalf("unsigned push was not refused by the signature rule: %s", out)
	}
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if out, keyErr := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "owner", "-f", keyPath).CombinedOutput(); keyErr != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	owner.request("POST", "/user/signing-keys", map[string]string{"title": "laptop", "public_key": strings.TrimSpace(string(publicKey))}, 201, nil)
	gitRun(work, "", true, "-c", "gpg.format=ssh", "-c", "user.signingkey="+keyPath, "commit", "--amend", "-S", "--no-edit")
	gitRun(work, writeToken, true, "push", "origin", "signed")

	// SSH pushes stream through the forced-command gateway and obey the same rules.
	var sshKey struct {
		Fingerprint string `json:"fingerprint"`
	}
	owner.request("POST", "/user/ssh-keys", map[string]string{"title": "laptop", "public_key": strings.TrimSpace(string(publicKey))}, 201, &sshKey)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sshEnv := append(gitEnv, "GITOWN_SSH_HELPER=1", "GITOWN_SSH_HELPER_SCHEMA="+schema, "GITOWN_SSH_HELPER_DATA="+storage, "GITOWN_SSH_FINGERPRINT="+sshKey.Fingerprint, "GIT_SSH_COMMAND="+executable, "GIT_SSH_VARIANT=simple")
	sshGit := func(dir string, ok bool, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = sshEnv
		out, gitErr := cmd.CombinedOutput()
		if ok && gitErr != nil {
			t.Fatalf("ssh git %v failed: %s", args, out)
		}
		if !ok && gitErr == nil {
			t.Fatalf("ssh git %v unexpectedly succeeded: %s", args, out)
		}
		return string(out)
	}
	sshWork := filepath.Join(t.TempDir(), "ssh")
	sshGit("", true, "clone", "ssh://git@gitown.test/owner/forge.git", sshWork)
	sshGit(sshWork, true, "checkout", "-b", "via-ssh")
	if err = os.WriteFile(filepath.Join(sshWork, "ssh.txt"), []byte("pushed over ssh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sshGit(sshWork, true, "add", "ssh.txt")
	sshGit(sshWork, true, "-c", "user.name=Owner", "-c", "user.email=owner@example.test", "commit", "-m", "Push over SSH")
	sshGit(sshWork, true, "push", "origin", "via-ssh")
	owner.request("GET", "/repos/owner/forge/tree?ref=via-ssh&path=ssh.txt", nil, 200, nil)
	owner.request("PUT", "/repos/owner/forge/branch-rules?branch=via-ssh", map[string]any{"required_approvals": 0, "require_unite": true}, 200, nil)
	sshGit(sshWork, true, "-c", "user.name=Owner", "-c", "user.email=owner@example.test", "commit", "--allow-empty", "-m", "Direct change")
	if out := sshGit(sshWork, false, "push", "origin", "via-ssh"); !strings.Contains(out, "Unite request") {
		t.Fatalf("ssh push ignored require_unite: %s", out)
	}
	var refs []map[string]any
	owner.request("GET", "/repos/owner/forge/refs", nil, 200, &refs)
	sawSSH := false
	for _, ref := range refs {
		sawSSH = sawSSH || ref["via"] == "ssh"
	}
	if !sawSSH {
		t.Fatalf("ssh ref event missing: %+v", refs)
	}
}

func TestDiffLineNumbers(t *testing.T) {
	patch := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,3 +1,4 @@\n one\n-two\n+deux\n+zwei\n three\n@@ -10 +11 @@\n-ten\n+eleven\n"
	left, right := diffLineNumbers(patch)
	for _, line := range []int{1, 2, 3, 10} {
		if !left[line] {
			t.Fatalf("left line %d missing: %v", line, left)
		}
	}
	for _, line := range []int{1, 2, 3, 4, 11} {
		if !right[line] {
			t.Fatalf("right line %d missing: %v", line, right)
		}
	}
	if left[4] || right[5] || right[10] {
		t.Fatalf("unexpected lines: %v %v", left, right)
	}
}
