package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/config"
	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
	"github.com/Aaravkhanal/GITOWN/internal/version"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	cfg        config.Config
	db         *pgxpool.Pool
	git        *gitstore.Store
	apiRatesMu sync.Mutex
	apiRates   map[string]rateWindow
	passwords  chan struct{}
	transports chan struct{}
	dummyHash  string
	// mailer delivers rendered email; nil means SMTP is not configured and
	// queued mail is recorded as suppressed rather than sent.
	mailer func(context.Context, outgoingMail) error
	// smtpDial is injectable so the SMTP protocol test can use net.Pipe and
	// run under sandboxes that prohibit loopback listeners.
	smtpDial func(context.Context, string) (net.Conn, error)
	// webhookClient sends webhook deliveries. New() always sets this to a
	// client whose dialer refuses private/loopback addresses; tests swap it
	// for one pointed at a local receiver rather than weakening that dialer.
	webhookClient *http.Client
	metrics       httpMetrics
	workers       workerMetrics
}

type httpMetrics struct {
	requests        atomic.Uint64
	duration        atomic.Uint64
	durationBuckets [10]atomic.Uint64
	inFlight        atomic.Int64
	status          [6]atomic.Uint64
}

var httpDurationBuckets = [10]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type metricResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (m *httpMetrics) record(status int, elapsed time.Duration) {
	m.requests.Add(1)
	if status == 0 {
		status = http.StatusOK
	}
	class := status / 100
	if class >= 1 && class <= 5 {
		m.status[class].Add(1)
	}
	if elapsed > 0 {
		m.duration.Add(uint64(elapsed))
	}
	for index, bound := range httpDurationBuckets {
		if elapsed.Seconds() <= bound {
			m.durationBuckets[index].Add(1)
		}
	}
}

// apiRateLimitPerMinute bounds the general /api/ surface per authenticated
// identity (session or access token) or, for an anonymous caller, per IP.
// Many pages here fire a dozen or more parallel useData requests on load
// (commits, issues, milestones, labels, members, drops, ...), so a single
// active session can legitimately generate hundreds of requests a minute;
// 300 turned out to be tight enough to trip the e2e suite itself doing
// completely normal interactive use. This stays well below what a scripted
// scrape or abuse loop would want while covering that real usage. This is
// separate from authLimit, which is a much tighter, login-specific limit.
const apiRateLimitPerMinute = 1200

type rateWindow struct {
	count int
	until time.Time
}
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

var slug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$`)
var repoSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,98}[a-z0-9])?$`)

func New(cfg config.Config, db *pgxpool.Pool) (*App, error) {
	git, err := gitstore.New(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, db: db, git: git, apiRates: make(map[string]rateWindow), passwords: make(chan struct{}, 2), transports: make(chan struct{}, 4), dummyHash: auth.HashPassword(auth.Secret("dummy_")), webhookClient: newWebhookHTTPClient()}
	if cfg.SMTPAddr != "" {
		a.mailer = a.sendSMTP
	}
	return a, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	ready := func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := a.db.Ping(ctx); err != nil {
			fail(w, 503, "unavailable", "Database is unavailable.")
			return
		}
		respond(w, 200, map[string]string{"status": "ok", "version": version.Version})
	}
	mux.HandleFunc("GET /healthz", ready)
	mux.HandleFunc("GET /readyz", ready)
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /metrics", a.metricsHandler)
	mux.HandleFunc("POST /api/v1/auth/register", a.register)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/password/forgot", a.requestPasswordReset)
	mux.HandleFunc("POST /api/v1/auth/password/reset", a.resetPassword)
	mux.HandleFunc("POST /api/v1/auth/email/verify", a.verifyEmail)
	mux.HandleFunc("POST /api/v1/auth/email/verification-request", a.requestEmailVerification)
	mux.HandleFunc("POST /api/v1/auth/mfa/verify", a.verifyMFAChallenge)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.HandleFunc("GET /api/v1/auth/me", a.me)
	mux.HandleFunc("GET /api/v1/users/{username}/profile", a.profile)
	mux.HandleFunc("GET /api/v1/users/{username}/followers", a.followers)
	mux.HandleFunc("GET /api/v1/users/{username}/following", a.following)
	mux.HandleFunc("PUT /api/v1/users/{username}/follow", a.updateFollow)
	mux.HandleFunc("PUT /api/v1/user/profile", a.updateProfile)
	mux.HandleFunc("PUT /api/v1/user/showcase", a.updateShowcase)
	mux.HandleFunc("GET /api/v1/user/notifications", a.notifications)
	mux.HandleFunc("GET /api/v1/user/notifications/unread-count", a.unreadNotifications)
	mux.HandleFunc("GET /api/v1/user/feed", a.feed)
	mux.HandleFunc("PUT /api/v1/user/notifications/read", a.readAllNotifications)
	mux.HandleFunc("PUT /api/v1/user/notifications/{id}/read", a.readNotification)
	mux.HandleFunc("GET /api/v1/user/email-notifications", a.emailPreference)
	mux.HandleFunc("PUT /api/v1/user/email-notifications", a.updateEmailPreference)
	mux.HandleFunc("GET /api/v1/email/unsubscribe", a.unsubscribeEmail)
	mux.HandleFunc("POST /api/v1/email/unsubscribe", a.confirmUnsubscribe)
	mux.HandleFunc("GET /api/v1/user/invitations", a.userInvitations)
	mux.HandleFunc("POST /api/v1/user/invitations/{id}/accept", a.respondInvitation)
	mux.HandleFunc("POST /api/v1/user/invitations/{id}/decline", a.respondInvitation)
	mux.HandleFunc("POST /api/v1/invitations/accept", a.acceptInvitationToken)
	mux.HandleFunc("GET /api/v1/user/transfers", a.userTransfers)
	mux.HandleFunc("POST /api/v1/user/transfers/{id}/accept", a.respondTransfer)
	mux.HandleFunc("POST /api/v1/user/transfers/{id}/decline", a.respondTransfer)
	mux.HandleFunc("GET /api/v1/user/saved-searches", a.savedSearches)
	mux.HandleFunc("POST /api/v1/user/saved-searches", a.createSavedSearch)
	mux.HandleFunc("DELETE /api/v1/user/saved-searches/{id}", a.deleteSavedSearch)
	mux.HandleFunc("GET /api/v1/user/tokens", a.tokens)
	mux.HandleFunc("POST /api/v1/user/tokens", a.createToken)
	mux.HandleFunc("DELETE /api/v1/user/tokens/{id}", a.deleteToken)
	mux.HandleFunc("GET /api/v1/user/oauth-apps", a.oauthApps)
	mux.HandleFunc("POST /api/v1/user/oauth-apps", a.createOAuthApp)
	mux.HandleFunc("POST /api/v1/user/oauth-apps/{id}/secret", a.regenerateOAuthAppSecret)
	mux.HandleFunc("DELETE /api/v1/user/oauth-apps/{id}", a.deleteOAuthApp)
	mux.HandleFunc("GET /api/v1/oauth/authorize", a.oauthAuthorizeInfo)
	mux.HandleFunc("POST /api/v1/oauth/authorize", a.oauthAuthorizeDecide)
	mux.HandleFunc("POST /api/v1/oauth/token", a.oauthToken)
	mux.HandleFunc("GET /api/v1/user/authorized-apps", a.userOAuthAuthorizations)
	mux.HandleFunc("DELETE /api/v1/user/authorized-apps/{id}", a.revokeOAuthAuthorization)
	mux.HandleFunc("GET /api/v1/user/gitown-apps", a.gitownApps)
	mux.HandleFunc("POST /api/v1/user/gitown-apps", a.createGitownApp)
	mux.HandleFunc("DELETE /api/v1/user/gitown-apps/{id}", a.deleteGitownApp)
	mux.HandleFunc("GET /api/v1/user/activity", a.activity)
	mux.HandleFunc("GET /api/v1/user/sessions", a.sessions)
	mux.HandleFunc("DELETE /api/v1/user/sessions/{id}", a.deleteSession)
	mux.HandleFunc("PATCH /api/v1/user/password", a.changePassword)
	mux.HandleFunc("GET /api/v1/user/email-verification", a.emailVerificationStatus)
	mux.HandleFunc("POST /api/v1/user/email-verification/resend", a.resendEmailVerification)
	mux.HandleFunc("GET /api/v1/user/mfa", a.mfaSettings)
	mux.HandleFunc("POST /api/v1/user/mfa/setup", a.setupMFA)
	mux.HandleFunc("POST /api/v1/user/mfa/confirm", a.confirmMFA)
	mux.HandleFunc("POST /api/v1/user/mfa/recovery-codes/regenerate", a.regenerateMFARecoveryCodes)
	mux.HandleFunc("POST /api/v1/user/mfa/disable", a.disableMFA)
	mux.HandleFunc("POST /api/v1/user/step-up", a.stepUpAuthentication)
	mux.HandleFunc("GET /api/v1/operator/security/abuse", a.abuseOverview)
	mux.HandleFunc("GET /api/v1/operator/queues", a.operatorQueues)
	mux.HandleFunc("POST /api/v1/operator/queues/{queue}/{id}/retry", a.retryDeadLetter)
	mux.HandleFunc("GET /api/v1/user/deleted-repositories", a.deletedRepositories)
	mux.HandleFunc("POST /api/v1/user/deleted-repositories/{id}/restore", a.restoreRepository)
	mux.HandleFunc("GET /api/v1/repos", a.repositories)
	mux.HandleFunc("GET /api/v1/search/repositories", a.searchRepositories)
	mux.HandleFunc("GET /api/v1/search/builders", a.searchBuilders)
	mux.HandleFunc("GET /api/v1/search/work", a.searchWork)
	mux.HandleFunc("GET /api/v1/search/code", a.searchCode)
	mux.HandleFunc("GET /api/v1/topics", a.topicCatalog)
	mux.HandleFunc("GET /api/v1/topics/{topic}", a.topicPage)
	mux.HandleFunc("GET /api/v1/users/{username}/contributions", a.contributions)
	mux.HandleFunc("GET /api/v1/search/tasks", a.searchTasks)
	mux.HandleFunc("GET /api/v1/search/recommendations", a.recommendations)
	mux.HandleFunc("GET /api/v1/collections", a.collections)
	mux.HandleFunc("POST /api/v1/collections", a.createCollection)
	mux.HandleFunc("GET /api/v1/collections/{owner}/{slug}", a.collection)
	mux.HandleFunc("POST /api/v1/collections/{owner}/{slug}/items", a.addCollectionItem)
	mux.HandleFunc("DELETE /api/v1/collections/{owner}/{slug}/items/{repoOwner}/{repoName}", a.removeCollectionItem)
	mux.HandleFunc("PATCH /api/v1/collections/{owner}/{slug}", a.featureCollection)
	mux.HandleFunc("GET /api/v1/districts", a.districts)
	mux.HandleFunc("POST /api/v1/districts", a.createDistrict)
	mux.HandleFunc("GET /api/v1/districts/{slug}", a.district)
	mux.HandleFunc("PATCH /api/v1/districts/{slug}", a.updateDistrict)
	mux.HandleFunc("GET /api/v1/districts/{slug}/members", a.districtMembers)
	mux.HandleFunc("POST /api/v1/districts/{slug}/members", a.addDistrictMember)
	mux.HandleFunc("DELETE /api/v1/districts/{slug}/members/{username}", a.removeDistrictMember)
	mux.HandleFunc("GET /api/v1/districts/{slug}/crews", a.districtCrews)
	mux.HandleFunc("POST /api/v1/districts/{slug}/crews", a.createCrew)
	mux.HandleFunc("GET /api/v1/districts/{slug}/crews/{crew}/members", a.crewMembers)
	mux.HandleFunc("POST /api/v1/districts/{slug}/crews/{crew}/members", a.addCrewMember)
	mux.HandleFunc("DELETE /api/v1/districts/{slug}/crews/{crew}/members/{username}", a.removeCrewMember)
	mux.HandleFunc("DELETE /api/v1/districts/{slug}/crews/{crew}", a.deleteCrew)
	mux.HandleFunc("GET /api/v1/districts/{slug}/secrets", a.districtSecrets)
	mux.HandleFunc("POST /api/v1/districts/{slug}/secrets", a.putDistrictSecret)
	mux.HandleFunc("GET /api/v1/districts/{slug}/secrets/{name}", a.districtSecret)
	mux.HandleFunc("DELETE /api/v1/districts/{slug}/secrets/{name}", a.deleteDistrictSecret)
	mux.HandleFunc("GET /api/v1/districts/{slug}/audit", a.districtAudit)
	mux.HandleFunc("GET /api/v1/districts/{slug}/usage", a.districtUsage)
	mux.HandleFunc("GET /api/v1/districts/{slug}/invoices", a.districtInvoices)
	mux.HandleFunc("POST /api/v1/districts/{slug}/invoices", a.createDistrictInvoice)
	mux.HandleFunc("POST /api/v1/districts/{slug}/invoices/{id}/pay", a.payDistrictInvoice)
	mux.HandleFunc("GET /api/v1/districts/{slug}/repos", a.districtRepos)
	mux.HandleFunc("GET /api/v1/districts/{slug}/board", a.districtBoard)
	mux.HandleFunc("GET /api/v1/districts/{slug}/webhooks", a.districtWebhooks)
	mux.HandleFunc("POST /api/v1/districts/{slug}/webhooks", a.createDistrictWebhook)
	mux.HandleFunc("PATCH /api/v1/districts/{slug}/webhooks/{id}", a.updateDistrictWebhook)
	mux.HandleFunc("DELETE /api/v1/districts/{slug}/webhooks/{id}", a.deleteDistrictWebhook)
	mux.HandleFunc("GET /api/v1/districts/{slug}/webhooks/{id}/deliveries", a.districtWebhookDeliveries)
	mux.HandleFunc("POST /api/v1/districts/{slug}/webhooks/{id}/deliveries/{deliveryId}/replay", a.replayDistrictWebhookDelivery)
	mux.HandleFunc("GET /api/v1/user/ssh-keys", a.sshKeys)
	mux.HandleFunc("POST /api/v1/user/ssh-keys", a.createSSHKey)
	mux.HandleFunc("DELETE /api/v1/user/ssh-keys/{id}", a.deleteSSHKey)
	mux.HandleFunc("GET /api/v1/user/signing-keys", a.signingKeys)
	mux.HandleFunc("POST /api/v1/user/signing-keys", a.createSigningKey)
	mux.HandleFunc("DELETE /api/v1/user/signing-keys/{id}", a.deleteSigningKey)
	mux.HandleFunc("GET /api/v1/crates", a.crates)
	mux.HandleFunc("POST /api/v1/crates", a.createCrate)
	mux.HandleFunc("GET /api/v1/crates/{name}", a.crate)
	mux.HandleFunc("PATCH /api/v1/crates/{name}", a.updateCrate)
	mux.HandleFunc("DELETE /api/v1/crates/{name}", a.deleteCrate)
	mux.HandleFunc("POST /api/v1/crates/{name}/versions", a.publishCrateVersion)
	mux.HandleFunc("DELETE /api/v1/crates/{name}/versions/{version}", a.deleteCrateVersion)
	mux.HandleFunc("GET /api/v1/crates/{name}/versions/{version}/download", a.downloadCrateVersion)
	mux.HandleFunc("POST /api/v1/repos", a.createRepository)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}", a.repository)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/spark", a.spark)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/spark", a.updateSpark)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/topics", a.topics)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/topics", a.updateTopics)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}", a.updateRepository)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}", a.deleteRepository)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/rename", a.renameRepository)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/{action}", a.setRepositoryArchived)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/members", a.members)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/permissions", a.repositoryPermissions)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/members/{username}", a.updateMember)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/members/{username}", a.removeMember)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/tree", a.tree)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/raw", a.raw)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/blame", a.blame)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/compare", a.compareRefs)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/contents", a.updateContent)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/contents", a.deleteContent)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/commits", a.commits)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/commits/{sha}/statuses", a.commitStatuses)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/commits/{sha}/status", a.updateCommitStatus)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/invitations", a.repositoryInvitations)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/invitations", a.createInvitation)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/invitations/{id}", a.revokeInvitation)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/transfer", a.repositoryTransfer)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/transfer", a.createTransfer)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/transfer", a.cancelTransfer)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/presentation", a.updatePresentation)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/showcase", a.repositoryShowcase)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/subscription", a.repositorySubscription)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/subscription", a.updateRepositorySubscription)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/branch-rules", a.branchRule)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/branch-rules", a.updateBranchRule)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues", a.issues)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issue-templates", a.issueTemplates)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issue-templates", a.updateIssueTemplates)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues", a.createIssue)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}", a.issue)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/issues/{number}", a.updateIssue)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/sub-issues", a.subIssues)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/parent", a.updateIssueParent)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/dependencies", a.issueDependencies)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/dependencies", a.updateIssueDependencies)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/assignees", a.issueAssignees)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/assignees", a.updateIssueAssignees)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/subscription", a.issueSubscription)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/subscription", a.updateIssueSubscription)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/milestones", a.milestones)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/milestones", a.createMilestone)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/milestones/{id}", a.updateMilestone)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/milestone", a.issueMilestone)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/milestone", a.updateIssueMilestone)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/board", a.board)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/board/settings", a.boardSettings)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/board/settings", a.updateBoardSettings)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/board/fields", a.createBoardField)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/board/fields/{id}", a.updateBoardField)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/board/fields/{id}", a.deleteBoardField)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/board/values", a.updateBoardValue)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/pulls/{number}/board", a.updatePullBoard)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/pulls/{number}/board", a.removePullBoard)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/board", a.updateBoardItem)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/comments", a.issueComments)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{number}/comments", a.createIssueComment)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/issues/{number}/comments/{id}", a.editIssueComment)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/comments/{id}/history", a.issueCommentHistory)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/references", a.issueReferences)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/planning", a.updateIssuePlanning)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{number}/transfer", a.transferIssue)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/labels", a.labels)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/labels", a.createLabel)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/labels/{id}", a.deleteLabel)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/labels", a.issueLabels)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{number}/labels", a.addIssueLabel)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/issues/{number}/labels/{id}", a.removeIssueLabel)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls", a.pulls)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls", a.createPull)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}", a.pull)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/subscription", a.pullSubscription)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/pulls/{number}/subscription", a.updatePullSubscription)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/pulls/{number}", a.updatePull)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/comments", a.pullComments)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/comments", a.createPullComment)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/reviews", a.pullReviews)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/reviews", a.createPullReview)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/reviews/{id}/dismiss", a.dismissPullReview)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/threads", a.pullThreads)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/threads", a.createPullThread)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/threads/{id}/resolve", a.resolvePullThread)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/threads/{id}/replies", a.threadReplies)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/threads/{id}/replies", a.createThreadReply)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/reviewers", a.pullReviewers)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/pulls/{number}/reviewers", a.updatePullReviewers)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/assignees", a.pullAssignees)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/pulls/{number}/assignees", a.updatePullAssignees)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/labels", a.pullLabels)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/pulls/{number}/labels", a.updatePullLabels)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/links", a.pullLinks)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/pulls/{number}/links", a.updatePullLinks)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/timeline", a.pullTimeline)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/pulls/{number}/comments/{id}", a.editPullComment)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/comments/{id}/history", a.pullCommentHistory)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/merge", a.mergePull)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/crews", a.pullCrews)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/pulls/{number}/crews", a.updatePullCrews)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/deploy-keys", a.deployKeys)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/deploy-keys", a.createDeployKey)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/deploy-keys/{id}", a.deleteDeployKey)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/gitown-apps", a.repoGitownApps)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/gitown-apps", a.installGitownApp)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/gitown-apps/{id}", a.uninstallGitownApp)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/webhooks", a.repoWebhooks)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/webhooks", a.createRepoWebhook)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/webhooks/{id}", a.updateRepoWebhook)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/webhooks/{id}", a.deleteRepoWebhook)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/webhooks/{id}/deliveries", a.repoWebhookDeliveries)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/webhooks/{id}/deliveries/{deliveryId}/replay", a.replayRepoWebhookDelivery)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/routes/workflows", a.routesWorkflows)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/wiki", a.wikiPages)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/wiki-search", a.wikiSearch)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/wiki/{slug}/attachments", a.wikiAttachmentList)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/wiki/{slug}/attachments", a.wikiAttachments)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/wiki/{slug}/attachments/{name}", a.wikiAttachments)
	mux.HandleFunc("HEAD /api/v1/repos/{owner}/{repo}/wiki/{slug}/attachments/{name}", a.wikiAttachments)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/wiki/{slug}", a.wikiPage)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/wiki/{slug}", a.saveWikiPage)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/remixes", a.remixes)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/remix", a.createRemix)
	mux.HandleFunc("POST /api/v1/imports", a.importRepository)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/discussions", a.discussions)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/discussions", a.discussions)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/discussions/{number}", a.discussion)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/discussions/{number}", a.discussion)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/discussions/{number}/comments", a.discussionComment)
	mux.HandleFunc("GET /api/v1/snippets", a.snippets)
	mux.HandleFunc("POST /api/v1/snippets", a.snippets)
	mux.HandleFunc("GET /api/v1/snippets/{id}", a.snippet)
	mux.HandleFunc("DELETE /api/v1/snippets/{id}", a.snippet)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/dev-environments", a.devEnvironments)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/dev-environments", a.devEnvironments)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/dev-environments/{id}/cancel", a.cancelDevEnvironment)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/codeowners", a.codeOwners)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/dependency-graph", a.dependencyGraph)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/supply-chain/scan", a.scanSupplyChain)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/secret-findings", a.secretFindings)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/secret-findings/{id}/dismiss", a.dismissSecretFinding)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/advisories", a.advisories)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/advisories", a.advisories)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/vulnerability-alerts", a.vulnerabilityAlerts)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/vulnerability-alerts/{id}/dismiss", a.dismissVulnerability)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/merge-queue", a.mergeQueue)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/merge-queue", a.enqueueMerge)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/merge-queue/{number}/dequeue", a.dequeueMerge)
	mux.HandleFunc("GET /api/v1/users/{username}/sponsorships", a.sponsorships)
	mux.HandleFunc("POST /api/v1/users/{username}/sponsorships", a.sponsorships)
	mux.HandleFunc("POST /api/v1/user/devices", a.registerDevice)
	mux.HandleFunc("GET /api/v1/mobile/feed", a.mobileFeed)
	mux.HandleFunc("GET /sites/{owner}/{repo}", a.serveShowcase)
	mux.HandleFunc("GET /sites/{owner}/{repo}/{path...}", a.serveShowcase)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/routes/runs", a.routesRuns)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/routes/runs/{id}", a.routesRunDetail)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/routes/dispatch", a.dispatchRoute)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/routes/runs/{id}/cancel", a.cancelRouteRun)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/routes/jobs/{id}/approve", a.approveRouteJob)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/routes/environments", a.routeEnvironments)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/routes/environments/{name}", a.updateRouteEnvironment)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/routes/secrets", a.routeSecrets)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/routes/secrets/{name}", a.putRouteSecret)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/routes/secrets/{name}", a.deleteRouteSecret)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/routes/runs/{id}/artifacts", a.uploadRouteArtifact)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/routes/artifacts/{artifactId}", a.downloadRouteArtifact)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/routes/caches", a.routeCaches)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/routes/caches/{key}", a.putRouteCache)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/routes/runners", a.routeRunners)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/routes/runners", a.createRouteRunner)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/routes/runners/{runnerId}", a.deleteRouteRunner)
	mux.HandleFunc("POST /api/v1/routes/runners/heartbeat", a.routeRunnerHeartbeat)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/routes/runs/{id}/logs", a.appendRouteLog)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/maintenance", a.maintainRepository)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/district", a.updateRepositoryDistrict)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/refs", a.refEvents)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/tags", a.repositoryTags)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/tags", a.createRepositoryTag)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/tags/{tag}", a.deleteRepositoryTag)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/export", a.exportRepository)
	mux.HandleFunc("POST /api/v1/operator/storage/reconcile", a.reconcileOrphanStorage)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/drops", a.drops)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/drops", a.createDrop)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/drops/{tag}", a.drop)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/drops/{tag}", a.updateDrop)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/drops/{tag}", a.deleteDrop)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/drops/{tag}/assets", a.uploadDropAsset)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/drops/{tag}/assets/{name}", a.downloadDropAsset)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/drops/{tag}/assets/{name}", a.deleteDropAsset)
	mux.HandleFunc("PUT /npm/{name}", a.npmPublish)
	mux.HandleFunc("GET /npm/{name}/-/{file}", a.npmTarball)
	mux.HandleFunc("GET /npm/{name}", a.npmPackument)
	mux.HandleFunc("GET /v2/", a.ociVersion)
	mux.HandleFunc("POST /v2/{name}/blobs/uploads/", a.ociStartBlob)
	mux.HandleFunc("PUT /v2/{name}/blobs/uploads/{uuid}", a.ociFinishBlob)
	mux.HandleFunc("PUT /v2/{name}/manifests/{reference}", a.ociPutManifest)
	mux.HandleFunc("GET /v2/{name}/manifests/{reference}", a.ociGetManifest)
	mux.HandleFunc("GET /v2/{name}/blobs/{digest}", a.ociGetBlob)
	mux.HandleFunc("/git/", a.gitHTTP)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := auth.ID()
		trace := newTraceContext(r.Header.Get("traceparent"))
		r = r.WithContext(context.WithValue(r.Context(), traceContextKey{}, trace))
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("traceparent", traceParent(trace))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writer := &metricResponseWriter{ResponseWriter: w}
			w = writer
			started := time.Now()
			a.metrics.inFlight.Add(1)
			defer func() {
				a.metrics.inFlight.Add(-1)
				elapsed := time.Since(started)
				a.metrics.record(writer.status, elapsed)
				if trace.flags&1 == 1 {
					slog.Info("http server span", "trace_id", trace.traceID, "span_id", trace.spanID, "request_id", requestID, "method", r.Method, "status", writer.status, "duration_ms", elapsed.Milliseconds())
				}
			}()
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// GITOWN-API-Version is a real, checkable signal for whenever a
			// future breaking version ships; /api/v1/ is the only version
			// today, so there is nothing to route yet, just a value clients
			// can already start comparing against.
			w.Header().Set("GITOWN-API-Version", "v1")
			if !a.apiRateLimit(w, r) {
				return
			}
			limit := int64(1 << 20)
			asset := r.Method == "POST" && strings.Contains(r.URL.Path, "/drops/") && strings.HasSuffix(r.URL.Path, "/assets")
			wikiAsset := r.Method == "POST" && strings.Contains(r.URL.Path, "/wiki/") && strings.HasSuffix(r.URL.Path, "/attachments")
			if asset {
				limit = 8 << 20
			}
			if wikiAsset {
				limit = 700 << 10
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			// RFC 8058 one-click unsubscribe comes from mail clients: no
			// Origin header, form-encoded body, secret token in the URL.
			oneClick := r.Method == "POST" && r.URL.Path == "/api/v1/email/unsubscribe" && r.URL.Query().Get("token") != ""
			// The OAuth token endpoint is called directly by third-party
			// clients per the OAuth spec — form-encoded, no browser Origin —
			// unlike /oauth/authorize, which our own frontend calls normally.
			oauthTokenExchange := r.Method == "POST" && r.URL.Path == "/api/v1/oauth/token"
			runnerHeartbeat := r.Method == "POST" && r.URL.Path == "/api/v1/routes/runners/heartbeat"
			bearerRepoWrite := false
			if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !oneClick && !oauthTokenExchange && !runnerHeartbeat {
				if allowed, done := a.allowBearerWrite(w, r); done {
					return
				} else {
					bearerRepoWrite = allowed
				}
			}
			if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !oneClick && !oauthTokenExchange && !runnerHeartbeat {
				if !bearerRepoWrite && r.Header.Get("Origin") != a.cfg.Origin && !isTokenStatusRequest(r) {
					fail(w, 403, "origin_rejected", "Request origin is not allowed.")
					return
				}
				jsonRequired := r.Method != "DELETE"
				if (asset || wikiAsset) && strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream") {
					jsonRequired = false
				}
				if jsonRequired && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					fail(w, 415, "json_required", "Use application/json.")
					return
				}
			}
		}
		if requiresStepUp(r.Method, r.URL.Path) {
			if !a.hasRecentStepUp(w, r) {
				return
			}
		}
		defer func() {
			if err := recover(); err != nil {
				slog.Error("request panic", "request_id", w.Header().Get("X-Request-ID"))
				fail(w, 500, "internal_error", "An unexpected error occurred.")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		fail(w, 400, "invalid_input", "Invalid JSON request.")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, 400, "invalid_input", "Expected a single JSON value.")
		return false
	}
	return true
}
func serverError(w http.ResponseWriter, err error) {
	slog.Error("operation failed", "error", err)
	fail(w, 500, "internal_error", "The operation could not be completed.")
}
func conflict(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// user resolves the caller's identity from a session cookie first, then a
// Bearer access token. A browser session is not limited by token scope.
// A token is: allowBearerWrite rejects every write unless the token's
// scope is repo:write, and account-security routes stay session-only.
// package:read and package:write do not authorize general API writes.
func (a *App) user(r *http.Request) *User {
	if u := a.sessionUser(r); u != nil {
		return u
	}
	return a.bearerUser(r)
}

// allowBearerWrite reports whether this write authenticated with a
// repo:write bearer token. done is true when the response was already sent.
func (a *App) allowBearerWrite(w http.ResponseWriter, r *http.Request) (allowed bool, done bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" || strings.HasPrefix(raw, "rnr_") || strings.HasPrefix(raw, "mob_") {
		return false, false
	}
	if sessionOnlyWrite(r.URL.Path) {
		fail(w, 403, "session_required", "Account security changes require a browser session.")
		return false, true
	}
	var scope string
	err := a.db.QueryRow(r.Context(), `SELECT scope FROM access_tokens WHERE token_hash=$1 AND expires_at>now()`, auth.Digest(raw)).Scan(&scope)
	if err != nil {
		fail(w, 401, "authentication_required", "Use a personal access token with repo:write scope.")
		return false, true
	}
	if scope != "repo:write" {
		fail(w, 403, "token_scope", "This token is read-only. A repo:write token is required for this change.")
		return false, true
	}
	return true, false
}

func sessionOnlyWrite(path string) bool {
	switch {
	case strings.HasPrefix(path, "/api/v1/auth/"):
		return true
	case path == "/api/v1/user/password" || strings.HasPrefix(path, "/api/v1/user/tokens"):
		return true
	case strings.HasPrefix(path, "/api/v1/user/sessions"):
		return true
	case strings.HasPrefix(path, "/api/v1/user/email-verification"):
		return true
	case strings.HasPrefix(path, "/api/v1/user/mfa"):
		return true
	case strings.HasPrefix(path, "/api/v1/user/oauth-apps"):
		return true
	case path == "/api/v1/oauth/authorize" || strings.HasPrefix(path, "/api/v1/oauth/authorize"):
		return true
	case strings.HasPrefix(path, "/api/v1/user/gitown-apps"):
		return true
	default:
		return false
	}
}

func (a *App) sessionUser(r *http.Request) *User {
	c, err := r.Cookie("gitown_session")
	if err != nil {
		return nil
	}
	var u User
	digest := auth.Digest(c.Value)
	err = a.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.display_name FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now()`, digest).Scan(&u.ID, &u.Username, &u.DisplayName)
	if err != nil {
		return nil
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE sessions SET last_seen_at=now() WHERE token_hash=$1 AND last_seen_at<now()-interval '5 minutes'`, digest)
	return &u
}

func (a *App) bearerUser(r *http.Request) *User {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" || len(raw) > 200 {
		return nil
	}
	var u User
	err := a.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.display_name FROM access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1 AND t.expires_at>now()`, auth.Digest(raw)).Scan(&u.ID, &u.Username, &u.DisplayName)
	if err != nil {
		return nil
	}
	return &u
}
func (a *App) requireUser(w http.ResponseWriter, r *http.Request) *User {
	u := a.user(r)
	if u == nil {
		fail(w, 401, "authentication_required", "Sign in to continue.")
	}
	return u
}

// apiRateLimit bounds every /api/ request by identity: a signed-in session
// or access token gets its own bucket (so one busy user never starves
// another), and an anonymous caller falls back to a per-IP bucket. Tokens
// are hashed before use as a map key so a raw secret is never held in
// memory outside the request that presented it.
func (a *App) apiRateLimit(w http.ResponseWriter, r *http.Request) bool {
	return a.apiRateLimitAt(w, r, apiRateLimitPerMinute, time.Now())
}

// apiRateLimitAt isolates the decision from wall-clock time and the production
// threshold so abuse and concurrency tests can exercise it deterministically.
func (a *App) apiRateLimitAt(w http.ResponseWriter, r *http.Request, limit int, now time.Time) bool {
	ip := trustedClientIP(r, a.cfg.TrustedProxies)
	key := "ip:" + ip
	if c, err := r.Cookie("gitown_session"); err == nil && c.Value != "" {
		key = "s:" + auth.Digest(c.Value)
	} else if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && raw != "" {
		key = "t:" + auth.Digest(raw)
	}
	a.apiRatesMu.Lock()
	defer a.apiRatesMu.Unlock()
	for k, v := range a.apiRates {
		if now.After(v.until) {
			delete(a.apiRates, k)
		}
	}
	v := a.apiRates[key]
	if v.until.IsZero() {
		v.until = now.Add(time.Minute)
	}
	if v.count >= limit || (len(a.apiRates) >= 50000 && v.count == 0) {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "rate_limited", "Too many requests. Try again in a moment.")
		return false
	}
	v.count++
	a.apiRates[key] = v
	return true
}

func (a *App) session(w http.ResponseWriter, r *http.Request, u User) error {
	secret := auth.Secret("ses_")
	expires := time.Now().Add(7 * 24 * time.Hour)
	ip := trustedClientIP(r, a.cfg.TrustedProxies)
	userAgent := r.UserAgent()
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM users WHERE id=$1 FOR UPDATE`, u.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM sessions WHERE user_id=$1 AND (expires_at<=now() OR token_hash IN (SELECT token_hash FROM sessions WHERE user_id=$1 ORDER BY created_at DESC OFFSET 19))`, u.ID); err != nil {
		return err
	}
	var newDevice bool
	if ip != "" && userAgent != "" {
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sessions WHERE user_id=$1) AND NOT EXISTS(SELECT 1 FROM sessions WHERE user_id=$1 AND ip_address=$2 AND user_agent=$3)`, u.ID, ip, userAgent).Scan(&newDevice); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO sessions(id,token_hash,user_id,expires_at,ip_address,user_agent,step_up_at) VALUES($1,$2,$3,$4,$5,$6,now())`, auth.ID(), auth.Digest(secret), u.ID, expires, ip, userAgent); err != nil {
		return err
	}
	if newDevice {
		if err = a.queueSecurityNotice(r, tx, u.ID, "New sign-in to your GITOWN account", "A sign-in to your GITOWN account was completed from a device or network not seen before. If this was not you, change your password and revoke unfamiliar sessions."); err != nil {
			return err
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.new_device_sign_in','self')`, u.ID); err != nil {
			return err
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "gitown_session", Value: secret, Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: 7 * 24 * 3600})
	return nil
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, map[string]any{"user": a.user(r), "signup_enabled": a.cfg.Signup})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("gitown_session"); err == nil {
		if _, err := a.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, auth.Digest(c.Value)); err != nil {
			serverError(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "gitown_session", Value: "", Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	respond(w, 200, map[string]bool{"ok": true})
}
