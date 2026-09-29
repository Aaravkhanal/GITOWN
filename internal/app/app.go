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
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/config"
	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	cfg        config.Config
	db         *pgxpool.Pool
	git        *gitstore.Store
	ratesMu    sync.Mutex
	rates      map[string]rateWindow
	passwords  chan struct{}
	transports chan struct{}
	dummyHash  string
}
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
	return &App{cfg: cfg, db: db, git: git, rates: make(map[string]rateWindow), passwords: make(chan struct{}, 2), transports: make(chan struct{}, 4), dummyHash: auth.HashPassword(auth.Secret("dummy_"))}, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := a.db.Ping(ctx); err != nil {
			fail(w, 503, "unavailable", "Database is unavailable.")
			return
		}
		respond(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/v1/auth/register", a.register)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.HandleFunc("GET /api/v1/auth/me", a.me)
	mux.HandleFunc("GET /api/v1/users/{username}/profile", a.profile)
	mux.HandleFunc("PUT /api/v1/user/profile", a.updateProfile)
	mux.HandleFunc("PUT /api/v1/user/showcase", a.updateShowcase)
	mux.HandleFunc("GET /api/v1/user/tokens", a.tokens)
	mux.HandleFunc("POST /api/v1/user/tokens", a.createToken)
	mux.HandleFunc("DELETE /api/v1/user/tokens/{id}", a.deleteToken)
	mux.HandleFunc("GET /api/v1/user/activity", a.activity)
	mux.HandleFunc("GET /api/v1/user/sessions", a.sessions)
	mux.HandleFunc("DELETE /api/v1/user/sessions/{id}", a.deleteSession)
	mux.HandleFunc("PATCH /api/v1/user/password", a.changePassword)
	mux.HandleFunc("GET /api/v1/user/deleted-repositories", a.deletedRepositories)
	mux.HandleFunc("POST /api/v1/user/deleted-repositories/{id}/restore", a.restoreRepository)
	mux.HandleFunc("GET /api/v1/repos", a.repositories)
	mux.HandleFunc("POST /api/v1/repos", a.createRepository)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}", a.repository)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}", a.updateRepository)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}", a.deleteRepository)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/rename", a.renameRepository)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/{action}", a.setRepositoryArchived)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/members", a.members)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/members", a.addMember)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/members/{username}", a.updateMember)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/members/{username}", a.removeMember)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/tree", a.tree)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/raw", a.raw)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/contents", a.updateContent)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/contents", a.deleteContent)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/commits", a.commits)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/branch-rules", a.branchRule)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/branch-rules", a.updateBranchRule)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues", a.issues)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues", a.createIssue)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/issues/{number}", a.updateIssue)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/assignees", a.issueAssignees)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/assignees", a.updateIssueAssignees)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/milestones", a.milestones)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/milestones", a.createMilestone)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/milestones/{id}", a.updateMilestone)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/milestone", a.issueMilestone)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/milestone", a.updateIssueMilestone)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/board", a.board)
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{number}/board", a.updateBoardItem)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/comments", a.issueComments)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{number}/comments", a.createIssueComment)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/labels", a.labels)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/labels", a.createLabel)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/labels/{id}", a.deleteLabel)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{number}/labels", a.issueLabels)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{number}/labels", a.addIssueLabel)
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/issues/{number}/labels/{id}", a.removeIssueLabel)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls", a.pulls)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls", a.createPull)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}", a.pull)
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/pulls/{number}", a.updatePull)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/comments", a.pullComments)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/comments", a.createPullComment)
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{number}/reviews", a.pullReviews)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/reviews", a.createPullReview)
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{number}/merge", a.mergePull)
	mux.HandleFunc("/git/", a.gitHTTP)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", auth.ID())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
				if r.Header.Get("Origin") != a.cfg.Origin {
					fail(w, 403, "origin_rejected", "Request origin is not allowed.")
					return
				}
				if r.Method != "DELETE" && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					fail(w, 415, "json_required", "Use application/json.")
					return
				}
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

func (a *App) user(r *http.Request) *User {
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
func (a *App) requireUser(w http.ResponseWriter, r *http.Request) *User {
	u := a.user(r)
	if u == nil {
		fail(w, 401, "authentication_required", "Sign in to continue.")
	}
	return u
}

func (a *App) authLimit(w http.ResponseWriter, r *http.Request) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.ratesMu.Lock()
	defer a.ratesMu.Unlock()
	now := time.Now()
	for key, v := range a.rates {
		if now.After(v.until) {
			delete(a.rates, key)
		}
	}
	v := a.rates[ip]
	if v.until.IsZero() {
		v.until = now.Add(10 * time.Minute)
	}
	if v.count >= 30 || (len(a.rates) >= 10000 && v.count == 0) {
		w.Header().Set("Retry-After", "600")
		fail(w, 429, "rate_limited", "Too many attempts. Try again later.")
		return false
	}
	v.count++
	a.rates[ip] = v
	select {
	case a.passwords <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "2")
		fail(w, 429, "busy", "Please try again in a moment.")
		return false
	}
}

func (a *App) session(w http.ResponseWriter, r *http.Request, u User) error {
	secret := auth.Secret("ses_")
	expires := time.Now().Add(7 * 24 * time.Hour)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
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
	if _, err = tx.Exec(r.Context(), `INSERT INTO sessions(id,token_hash,user_id,expires_at,ip_address,user_agent) VALUES($1,$2,$3,$4,$5,$6)`, auth.ID(), auth.Digest(secret), u.ID, expires, ip, userAgent); err != nil {
		return err
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
