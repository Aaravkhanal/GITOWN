package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/config"
	"github.com/Aaravkhanal/GITOWN/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBuildMessageHeaders(t *testing.T) {
	message, err := buildMessage("GITOWN <noreply@example.test>", outgoingMail{
		To:          "person@example.test",
		Subject:     "Café update\r\nBcc: attacker@example.test",
		Body:        "Hello\nsecond line with a very long run of text that needs soft wrapping " + strings.Repeat("x", 120),
		MessageID:   "abc",
		OneClickURL: "https://gitown.example/api/v1/email/unsubscribe?token=unsub_x",
	}, "gitown.example", time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	head, body, _ := strings.Cut(string(message), "\r\n\r\n")
	if strings.Contains(head, "\r\nBcc:") {
		t.Fatalf("header injection was not neutralized:\n%s", head)
	}
	for _, want := range []string{"Subject: =?utf-8?q?Caf=C3=A9_update_Bcc:_attacker@example.test?=", "Message-ID: <abc@gitown.example>", "MIME-Version: 1.0", "Content-Transfer-Encoding: quoted-printable", "List-Unsubscribe: <https://gitown.example/api/v1/email/unsubscribe?token=unsub_x>", "List-Unsubscribe-Post: List-Unsubscribe=One-Click", "Date: Wed, 30 Sep 2026 12:00:00 +0000"} {
		if !strings.Contains(head, want) {
			t.Fatalf("missing header %q in:\n%s", want, head)
		}
	}
	for _, line := range strings.Split(body, "\r\n") {
		if len(line) > 76 {
			t.Fatalf("body line exceeds quoted-printable limit: %q", line)
		}
	}
}

func TestReadmeSectionsAndBadges(t *testing.T) {
	intro, setup := readmeSections("# Atlas\n\nMaps for builders.\n\n## Getting started\n\nnpm install\nnpm run dev\n\n## License\n\nMIT\n")
	if !strings.Contains(intro, "Maps for builders.") || strings.Contains(intro, "npm install") {
		t.Fatalf("unexpected intro: %q", intro)
	}
	if setup != "npm install\nnpm run dev" {
		t.Fatalf("unexpected setup: %q", setup)
	}
	badges := contributionBadges(Contributions{MergedUnites: 12, HelpedShip: 4, ResolvedIssues: 1, ExternalMerges: 60, PublicRepositories: 3})
	got := map[string]string{}
	for _, badge := range badges {
		got[badge.ID] = badge.Tier
	}
	if got["merged"] != "silver" || got["resolver"] != "bronze" || got["maintainer"] != "gold" || got["reviewer"] != "" || len(got) != 3 {
		t.Fatalf("unexpected badges: %+v", badges)
	}
	if tags := skillTags("Go, postgres;GO,\nReact "); strings.Join(tags, "|") != "Go|postgres|React" {
		t.Fatalf("unexpected skill tags: %v", tags)
	}
}

// fakeSMTP accepts one message over an in-memory pipe and records the transcript.
func fakeSMTP(t *testing.T) (func(context.Context, string) (net.Conn, error), <-chan string) {
	t.Helper()
	done := make(chan string, 1)
	serve := func(conn net.Conn) {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		reader := bufio.NewReader(conn)
		var transcript strings.Builder
		fmt.Fprint(conn, "220 fake ESMTP\r\n")
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			transcript.WriteString(line)
			if inData {
				if line == ".\r\n" {
					inData = false
					fmt.Fprint(conn, "250 queued\r\n")
				}
				continue
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"):
				fmt.Fprint(conn, "250-fake\r\n250 8BITMIME\r\n")
			case strings.HasPrefix(command, "DATA"):
				inData = true
				fmt.Fprint(conn, "354 go ahead\r\n")
			case strings.HasPrefix(command, "QUIT"):
				fmt.Fprint(conn, "221 bye\r\n")
				done <- transcript.String()
				return
			default:
				fmt.Fprint(conn, "250 ok\r\n")
			}
		}
		done <- transcript.String()
	}
	dial := func(context.Context, string) (net.Conn, error) {
		client, server := net.Pipe()
		go serve(server)
		return client, nil
	}
	return dial, done
}

func TestSMTPTransport(t *testing.T) {
	dial, done := fakeSMTP(t)
	a := &App{cfg: config.Config{Origin: "https://gitown.example", SMTPAddr: "smtp.example.test:587", SMTPFrom: "GITOWN <noreply@gitown.example>"}, smtpDial: dial}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.sendSMTP(ctx, outgoingMail{To: "person@example.test", Subject: "Hello", Body: "Body text", MessageID: "m1"}); err != nil {
		t.Fatal(err)
	}
	transcript := <-done
	for _, want := range []string{"MAIL FROM:<noreply@gitown.example>", "RCPT TO:<person@example.test>", "Subject: Hello", "Body text"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("SMTP transcript missing %q:\n%s", want, transcript)
		}
	}
}

type capturedMail struct {
	mu   sync.Mutex
	mail []outgoingMail
	fail error
}

func (c *capturedMail) send(_ context.Context, m outgoingMail) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.mail = append(c.mail, m)
	return nil
}

func (c *capturedMail) to(address string) []outgoingMail {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []outgoingMail
	for _, m := range c.mail {
		if m.To == address {
			out = append(out, m)
		}
	}
	return out
}

func TestPhaseFiveNotificationsAndProfiles(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
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
	a, err := New(config.Config{DataDir: t.TempDir(), Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true, DigestInterval: time.Hour}, pool)
	if err != nil {
		t.Fatal(err)
	}
	outbox := &capturedMail{}
	a.mailer = outbox.send
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := func() testClient {
		jar, _ := cookiejar.New(nil)
		return testClient{t, server.URL, &http.Client{Jar: jar}}
	}
	owner, other, third := client(), client(), client()
	anon := client()
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	other.request("POST", "/auth/register", map[string]string{"username": "other", "email": "other@example.test", "password": "other-long-password", "display_name": "Other"}, 201, nil)
	third.request("POST", "/auth/register", map[string]string{"username": "third", "email": "third@example.test", "password": "third-long-password", "display_name": "Third"}, 201, nil)
	deliver := func() {
		t.Helper()
		for {
			sent, err := a.deliverMail(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if sent < mailBatchSize {
				return
			}
		}
	}
	kinds := func(c testClient, query string) []Notification {
		t.Helper()
		var notes []Notification
		c.request("GET", "/user/notifications"+query, nil, 200, &notes)
		return notes
	}
	count := func(notes []Notification, kind string) int {
		n := 0
		for _, note := range notes {
			if note.Kind == kind {
				n++
			}
		}
		return n
	}

	// Owners watch their repositories, so new issues reach them, and a
	// mention in the issue body reaches the mentioned reader.
	owner.request("POST", "/repos", map[string]any{"name": "notify", "description": "Notify", "visibility": "public", "readme": true}, 201, nil)
	var invitation struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos/owner/notify/invitations", map[string]string{"username": "other", "role": "write"}, 201, &invitation)
	other.request("POST", "/user/invitations/"+invitation.ID+"/accept", map[string]any{}, 200, nil)
	other.request("POST", "/repos/owner/notify/issues", map[string]string{"title": "Crash on start", "body": "cc @third please look"}, 201, nil)
	if notes := kinds(owner, ""); count(notes, "issue_opened") != 1 || notes[0].Excerpt == "" {
		t.Fatalf("owner did not hear about the new issue: %+v", notes)
	}
	if notes := kinds(third, ""); count(notes, "mention") != 1 {
		t.Fatalf("issue body mention was not delivered: %+v", notes)
	}
	deliver()
	ownerMail := outbox.to("owner@example.test")
	if len(ownerMail) != 1 || !strings.Contains(ownerMail[0].Subject, "[owner/notify] Crash on start (#1)") || !strings.Contains(ownerMail[0].Body, "http://localhost:3000/repos/owner/notify/issues/1") || !strings.Contains(ownerMail[0].Body, "/unsubscribe?token=unsub_") || ownerMail[0].OneClickURL == "" {
		t.Fatalf("unexpected owner email: %+v", ownerMail)
	}
	var leaked int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM email_messages WHERE body LIKE '%unsub_%' OR (status='sent' AND user_id IS NOT NULL AND unsubscribe_token IS NULL)`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("unsubscribe tokens must be hashed and never stored in bodies: %d %v", leaked, err)
	}

	// Every comment produces its own notification and its own email.
	other.request("POST", "/repos/owner/notify/issues/1/comments", map[string]string{"body": "First detail"}, 201, nil)
	other.request("POST", "/repos/owner/notify/issues/1/comments", map[string]string{"body": "Second detail"}, 201, nil)
	if notes := kinds(owner, ""); count(notes, "issue_comment") != 2 {
		t.Fatalf("each comment should notify: %+v", notes)
	}
	deliver()
	if mails := outbox.to("owner@example.test"); len(mails) != 3 || !strings.Contains(mails[2].Body, "Second detail") {
		t.Fatalf("each comment should send an email: %+v", mails)
	}

	// GET only previews; the one-click POST from a mail client unsubscribes.
	token := regexp.MustCompile(`token=(unsub_[A-Za-z0-9_-]+)`).FindStringSubmatch(ownerMail[0].Body)[1]
	var preview map[string]string
	anon.request("GET", "/email/unsubscribe?token="+token, nil, 200, &preview)
	if preview["email_notifications"] != "immediate" || !strings.HasPrefix(preview["email"], "o") || strings.Contains(preview["email"], "owner@") {
		t.Fatalf("unexpected unsubscribe preview: %+v", preview)
	}
	anon.request("GET", "/email/unsubscribe?token=unsub_wrong", nil, 404, nil)
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/email/unsubscribe?token="+token, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("one-click unsubscribe failed: %v %v", res, err)
	}
	res.Body.Close()
	var preference map[string]any
	owner.request("GET", "/user/email-notifications", nil, 200, &preference)
	if preference["email_notifications"] != "off" || preference["smtp_configured"] != true {
		t.Fatalf("unsubscribe did not turn email off: %+v", preference)
	}
	req, _ = http.NewRequest("POST", server.URL+"/api/v1/email/unsubscribe", strings.NewReader(`{"token":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 403 {
		t.Fatalf("JSON unsubscribe without the app origin must be rejected: %v %v", res, err)
	}
	res.Body.Close()
	owner.request("PUT", "/user/email-notifications", map[string]string{"email_notifications": "immediate"}, 200, nil)

	// Repository ignoring silences mentions; watching adds new issues.
	third.request("PUT", "/repos/owner/notify/subscription", map[string]string{"mode": "ignoring"}, 200, nil)
	before := len(kinds(third, ""))
	owner.request("POST", "/repos/owner/notify/issues/1/comments", map[string]string{"body": "@third are you there?"}, 201, nil)
	if after := len(kinds(third, "")); after != before {
		t.Fatalf("ignoring a repository should silence it: %d -> %d", before, after)
	}
	third.request("PUT", "/repos/owner/notify/subscription", map[string]string{"mode": "bogus"}, 422, nil)
	third.request("PUT", "/repos/owner/notify/subscription", map[string]string{"mode": "watching"}, 200, nil)
	var subscription map[string]any
	third.request("GET", "/repos/owner/notify/subscription", nil, 200, &subscription)
	if subscription["mode"] != "watching" || subscription["implicit"] != false {
		t.Fatalf("unexpected repository subscription: %+v", subscription)
	}
	owner.request("GET", "/repos/owner/notify/subscription", nil, 200, &subscription)
	if subscription["mode"] != "watching" || subscription["implicit"] != true {
		t.Fatalf("owners should watch by default: %+v", subscription)
	}
	other.request("POST", "/repos/owner/notify/issues", map[string]string{"title": "Second report", "body": "More"}, 201, nil)
	if notes := kinds(third, ""); count(notes, "issue_opened") != 1 {
		t.Fatalf("repository watchers should hear about new issues: %+v", notes)
	}

	// Inbox paging, filters, and unread count.
	all := kinds(owner, "")
	page := kinds(owner, "?limit=1")
	if len(page) != 1 || page[0].ID != all[0].ID {
		t.Fatalf("limit did not page: %+v", page)
	}
	next := kinds(owner, fmt.Sprintf("?limit=1&before=%d", page[0].ID))
	if len(next) != 1 || next[0].ID != all[1].ID {
		t.Fatalf("cursor did not page: %+v", next)
	}
	if onlyComments := kinds(owner, "?kind=issue_comment"); len(onlyComments) != 2 {
		t.Fatalf("kind filter failed: %+v", onlyComments)
	}
	owner.request("GET", "/user/notifications?kind=nope", nil, 422, nil)
	var unread map[string]int
	owner.request("GET", "/user/notifications/unread-count", nil, 200, &unread)
	if unread["unread"] != len(all) {
		t.Fatalf("unread count %d, expected %d", unread["unread"], len(all))
	}
	owner.request("PUT", fmt.Sprintf("/user/notifications/%d/read", all[0].ID), map[string]any{}, 200, nil)
	if unreadOnly := kinds(owner, "?filter=unread"); len(unreadOnly) != len(all)-1 {
		t.Fatalf("unread filter failed: %+v", unreadOnly)
	}

	// Digest readers get one combined email after the interval.
	deliver()
	immediate := len(outbox.to("other@example.test"))
	other.request("PUT", "/user/email-notifications", map[string]string{"email_notifications": "digest"}, 200, nil)
	owner.request("POST", "/repos/owner/notify/issues/1/comments", map[string]string{"body": "Digest item"}, 201, nil)
	deliver()
	if mails := outbox.to("other@example.test"); len(mails) != immediate {
		t.Fatalf("digest readers should not get immediate mail: %+v", mails)
	}
	if err = a.bundleDigests(ctx); err != nil {
		t.Fatal(err)
	}
	var pending int
	_ = pool.QueryRow(ctx, `SELECT count(*)::int FROM email_messages WHERE recipient_email='other@example.test' AND status='pending' AND digest`).Scan(&pending)
	if pending == 0 {
		t.Fatal("digest items should wait for the interval")
	}
	if _, err = pool.Exec(ctx, `UPDATE email_messages SET created_at=now()-interval '2 hours' WHERE recipient_email='other@example.test' AND digest`); err != nil {
		t.Fatal(err)
	}
	if err = a.bundleDigests(ctx); err != nil {
		t.Fatal(err)
	}
	deliver()
	if mails := outbox.to("other@example.test"); len(mails) != immediate+1 || !strings.HasPrefix(mails[immediate].Subject, "GITOWN digest: 1 update") || !strings.Contains(mails[immediate].Body, "Crash on start") {
		t.Fatalf("expected one digest email: %+v", mails)
	}

	// Without SMTP, mail is suppressed honestly; failures retry with backoff.
	a.mailer = nil
	owner.request("POST", "/repos/owner/notify/issues/2/comments", map[string]string{"body": "No transport"}, 201, nil)
	deliver()
	var status, lastError string
	if err = pool.QueryRow(ctx, `SELECT status,last_error FROM email_messages WHERE recipient_email='third@example.test' ORDER BY created_at DESC LIMIT 1`).Scan(&status, &lastError); err != nil || status != "suppressed" || lastError != errSMTPDisabled.Error() {
		t.Fatalf("expected suppressed delivery, got %q %q %v", status, lastError, err)
	}
	a.mailer = outbox.send
	outbox.fail = errors.New("421 try later")
	owner.request("POST", "/repos/owner/notify/issues/2/comments", map[string]string{"body": "Flaky transport"}, 201, nil)
	deliver()
	var attempts int
	var retryLater bool
	if err = pool.QueryRow(ctx, `SELECT status,attempts,next_attempt_at>now() FROM email_messages WHERE recipient_email='third@example.test' ORDER BY created_at DESC LIMIT 1`).Scan(&status, &attempts, &retryLater); err != nil || status != "pending" || attempts != 1 || !retryLater {
		t.Fatalf("failed delivery should be retried later: %q %d %v %v", status, attempts, retryLater, err)
	}
	var deadLetterID string
	if err = pool.QueryRow(ctx, `UPDATE email_messages SET status='sending',attempts=$1,claimed_at=now()-interval '11 minutes' WHERE id=(SELECT id FROM email_messages WHERE recipient_email='third@example.test' ORDER BY created_at DESC LIMIT 1) RETURNING id::text`, mailMaxAttempts-1).Scan(&deadLetterID); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverStaleMailClaims(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status,attempts FROM email_messages WHERE id=$1`, deadLetterID).Scan(&status, &attempts); err != nil || status != "failed" || attempts != mailMaxAttempts {
		t.Fatalf("expired mail claim did not enter dead-letter state: %q %d %v", status, attempts, err)
	}
	t.Setenv("GITOWN_OPERATORS", "owner")
	var queues struct {
		DeadLetters map[string][]map[string]any `json:"dead_letters"`
	}
	owner.request("GET", "/operator/queues", nil, 200, &queues)
	if len(queues.DeadLetters["email"]) == 0 || queues.DeadLetters["email"][0]["id"] != deadLetterID {
		t.Fatalf("operator queue view omitted the email dead letter: %+v", queues.DeadLetters)
	}
	outbox.fail = nil
	owner.request("POST", "/operator/queues/email/"+deadLetterID+"/retry", nil, 200, nil)
	deliver()
	if err = pool.QueryRow(ctx, `SELECT status FROM email_messages WHERE id=$1`, deadLetterID).Scan(&status); err != nil || status != "sent" {
		t.Fatalf("operator retry did not recover the email: %q %v", status, err)
	}
	// Following notifies once per follower and is listed both ways.
	other.request("PUT", "/users/owner/follow", map[string]bool{"followed": true}, 200, nil)
	other.request("PUT", "/users/owner/follow", map[string]bool{"followed": false}, 200, nil)
	other.request("PUT", "/users/owner/follow", map[string]bool{"followed": true}, 200, nil)
	if notes := kinds(owner, "?kind=follow"); len(notes) != 1 || notes[0].Actor != "other" || notes[0].Owner != "" {
		t.Fatalf("follow should notify once: %+v", notes)
	}
	var follows struct {
		Items   []FollowEntry `json:"items"`
		HasMore bool          `json:"has_more"`
	}
	anon.request("GET", "/users/owner/followers", nil, 200, &follows)
	if len(follows.Items) != 1 || follows.Items[0].Username != "other" {
		t.Fatalf("followers list: %+v", follows)
	}
	anon.request("GET", "/users/other/following", nil, 200, &follows)
	if len(follows.Items) != 1 || follows.Items[0].Username != "owner" {
		t.Fatalf("following list: %+v", follows)
	}
	anon.request("GET", "/users/nobody/followers", nil, 404, nil)

	// Profiles carry labelled links and skill tags that search understands.
	owner.request("PUT", "/user/profile", map[string]any{"display_name": "Owner", "bio": "Builds", "website": "", "location": "Kathmandu", "skills": "Go, Postgres", "availability": "Weekends", "open_to_collaborators": true, "links": []ProfileLink{{Label: "Blog", URL: "https://blog.example.test"}, {Label: "Mastodon", URL: "https://social.example.test/@owner"}}}, 200, nil)
	owner.request("PUT", "/user/profile", map[string]any{"display_name": "Owner", "bio": "", "website": "", "location": "", "links": []ProfileLink{{Label: "Plain", URL: "http://insecure.example.test"}}}, 422, nil)
	var profile Profile
	anon.request("GET", "/users/owner/profile", nil, 200, &profile)
	if len(profile.Links) != 2 || profile.Links[1].Label != "Mastodon" || strings.Join(profile.SkillTags, ",") != "Go,Postgres" {
		t.Fatalf("profile links or skills missing: %+v", profile)
	}
	var builders BuilderSearch
	anon.request("GET", "/search/builders?skill=postgres", nil, 200, &builders)
	if len(builders.Items) != 1 || builders.Items[0].Username != "owner" || builders.Items[0].Skills != "Go, Postgres" {
		t.Fatalf("skill filter failed: %+v", builders.Items)
	}
	anon.request("GET", "/search/builders?q=weekends&location=kathmandu&available=1", nil, 200, &builders)
	if len(builders.Items) != 1 {
		t.Fatalf("availability and location search failed: %+v", builders.Items)
	}

	// Reassigning someone notifies them again.
	owner.request("PUT", "/repos/owner/notify/issues/1/assignees", map[string][]string{"usernames": {"other"}}, 200, nil)
	owner.request("PUT", "/repos/owner/notify/issues/1/assignees", map[string][]string{"usernames": {}}, 200, nil)
	owner.request("PUT", "/repos/owner/notify/issues/1/assignees", map[string][]string{"usernames": {"other"}}, 200, nil)
	if notes := kinds(other, "?kind=assignment"); len(notes) != 2 {
		t.Fatalf("reassignment should notify again: %+v", notes)
	}

	// Pending invitations to private repositories reach the invitee.
	owner.request("POST", "/repos", map[string]any{"name": "secret", "visibility": "private", "readme": true}, 201, nil)
	owner.request("POST", "/repos/owner/secret/invitations", map[string]string{"username": "third", "role": "read"}, 201, nil)
	if notes := kinds(third, "?kind=invitation"); len(notes) != 1 || notes[0].Repository != "secret" {
		t.Fatalf("private invitation was hidden: %+v", notes)
	}

	// Check results: authors hear about failures even from their own
	// automation, thread watchers hear everything, participants do not.
	writeToken := owner.token("repo:write")
	workDir := filepath.Join(t.TempDir(), "work")
	gitRun := func(dir string, args ...string) string {
		t.Helper()
		argv := []string{"-c", "credential.helper=", "-c", "http.extraHeader=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("owner:"+writeToken))}
		cmd := exec.Command("git", append(argv, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, gitErr := cmd.CombinedOutput()
		if gitErr != nil {
			t.Fatalf("git %s failed: %s", args[0], out)
		}
		return strings.TrimSpace(string(out))
	}
	gitRun("", "clone", server.URL+"/git/owner/notify.git", workDir)
	gitRun(workDir, "config", "user.name", "Owner")
	gitRun(workDir, "config", "user.email", "owner@example.test")
	gitRun(workDir, "checkout", "-b", "feature")
	if err = os.MkdirAll(filepath.Join(workDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workDir, "docs", "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(workDir, "add", "docs/guide.md")
	gitRun(workDir, "commit", "-m", "Add guide")
	gitRun(workDir, "push", "-u", "origin", "feature")
	head := gitRun(workDir, "rev-parse", "HEAD")
	owner.request("POST", "/repos/owner/notify/pulls", map[string]any{"title": "Add guide", "body": "Docs for @third", "base_branch": "main", "head_branch": "feature"}, 201, nil)
	third.request("PUT", "/repos/owner/notify/pulls/1/subscription", map[string]any{"subscribed": true, "mode": "watch"}, 200, nil)
	other.request("POST", "/repos/owner/notify/pulls/1/comments", map[string]string{"body": "Looks good"}, 201, nil)
	owner.request("POST", "/repos/owner/notify/commits/"+head+"/status", map[string]string{"context": "ci/test", "state": "failure", "description": "2 tests failed"}, 200, nil)
	if notes := kinds(owner, "?kind=check_failure"); len(notes) != 1 || !strings.Contains(notes[0].Excerpt, "2 tests failed") {
		t.Fatalf("author should hear about failing checks: %+v", notes)
	}
	if notes := kinds(third, "?kind=check_failure"); len(notes) != 1 {
		t.Fatalf("thread watcher should hear about checks: %+v", notes)
	}
	if notes := kinds(other, "?kind=check_failure"); len(notes) != 0 {
		t.Fatalf("participants should not hear about checks: %+v", notes)
	}
	owner.request("POST", "/repos/owner/notify/commits/"+head+"/status", map[string]string{"context": "ci/test", "state": "failure", "description": "2 tests failed"}, 200, nil)
	owner.request("POST", "/repos/owner/notify/commits/"+head+"/status", map[string]string{"context": "ci/test", "state": "success"}, 200, nil)
	if notes := kinds(owner, "?kind=check_failure"); len(notes) != 1 {
		t.Fatalf("repeated status should not notify twice: %+v", notes)
	}
	if notes := kinds(owner, "?kind=check_success"); len(notes) != 0 {
		t.Fatalf("authors do not need their own success notices: %+v", notes)
	}
	if notes := kinds(third, "?kind=check_success"); len(notes) != 1 {
		t.Fatalf("watchers should hear about passing checks: %+v", notes)
	}
	if notes := kinds(owner, "?kind=pull_comment"); len(notes) != 1 {
		t.Fatalf("pull author should hear about comments: %+v", notes)
	}
	var activity struct {
		Total int `json:"total"`
	}
	anon.request("GET", "/users/owner/contributions", nil, 200, &activity)
	anon.request("GET", "/users/owner/profile", nil, 200, &profile)
	if profile.Contributions.Pushes < 1 || activity.Total < 3 {
		t.Fatalf("pushes should count as contributions: %+v %+v", profile.Contributions, activity)
	}

	// Showcase pages assemble presentation, people, and README sections.
	owner.request("PUT", "/repos/owner/notify/presentation", map[string]any{"homepage": "https://notify.example.test", "stack": "Go, Postgres", "screenshots": []Screenshot{{URL: "https://img.example.test/a.png", Caption: "Inbox"}}}, 200, nil)
	owner.request("PUT", "/repos/owner/notify/presentation", map[string]any{"homepage": "", "stack": "", "screenshots": []Screenshot{{URL: "http://img.example.test/a.png"}}}, 422, nil)
	var show Showcase
	anon.request("GET", "/repos/owner/notify/showcase", nil, 200, &show)
	if len(show.Screenshots) != 1 || show.Screenshots[0].Caption != "Inbox" || strings.Join(show.Stack, "|") != "Go|Postgres" || show.Readme == "" || len(show.Contributors) != 1 || show.Contributors[0].Role != "owner" || show.Repository.Homepage != "https://notify.example.test" {
		t.Fatalf("unexpected showcase: %+v", show)
	}
	anon.request("GET", "/repos/owner/secret/showcase", nil, 404, nil)
	owner.request("PUT", "/user/showcase", map[string]any{"repository_ids": []string{show.Repository.ID}}, 200, &profile)
	if len(profile.Showcase) != 1 || profile.Showcase[0].Homepage != "https://notify.example.test" || profile.Showcase[0].Stack != "Go, Postgres" {
		t.Fatalf("showcase cards should carry presentation: %+v", profile.Showcase)
	}
}
