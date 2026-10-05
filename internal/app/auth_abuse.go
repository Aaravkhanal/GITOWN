package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

// authLimitFor applies shared database-backed ten-minute windows to the
// caller's IP and optional account/challenge identifier. Only digests are
// persisted, and concurrent API processes share the same decision.
func (a *App) authLimitFor(w http.ResponseWriter, r *http.Request, principal string) bool {
	ip := trustedClientIP(r, a.cfg.TrustedProxies)
	ipHash := abuseDigest("ip:" + ip)
	buckets := []string{abuseDigest("ip:" + ip)}
	if principal != "" {
		buckets = append(buckets, abuseDigest("principal:"+strings.ToLower(strings.TrimSpace(principal))))
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return false
	}
	defer tx.Rollback(r.Context())
	blocked := false
	newlyBlocked := false
	var blockedScope string
	for _, bucket := range buckets {
		var denied bool
		var firstBlock bool
		err = tx.QueryRow(r.Context(), `INSERT INTO auth_rate_limit_buckets(bucket_hash,attempts,window_started_at)
			VALUES($1,1,now()) ON CONFLICT(bucket_hash) DO UPDATE SET
			attempts=CASE WHEN auth_rate_limit_buckets.window_started_at<now()-interval '10 minutes' OR
				(auth_rate_limit_buckets.blocked_until IS NOT NULL AND auth_rate_limit_buckets.blocked_until<=now()) THEN 1
				ELSE auth_rate_limit_buckets.attempts+1 END,
			window_started_at=CASE WHEN auth_rate_limit_buckets.window_started_at<now()-interval '10 minutes' OR
				(auth_rate_limit_buckets.blocked_until IS NOT NULL AND auth_rate_limit_buckets.blocked_until<=now()) THEN now()
				ELSE auth_rate_limit_buckets.window_started_at END,
			blocked_until=CASE WHEN auth_rate_limit_buckets.window_started_at<now()-interval '10 minutes' OR
				(auth_rate_limit_buckets.blocked_until IS NOT NULL AND auth_rate_limit_buckets.blocked_until<=now()) THEN NULL
				WHEN auth_rate_limit_buckets.blocked_until>now() THEN auth_rate_limit_buckets.blocked_until
				WHEN auth_rate_limit_buckets.attempts+1>30 THEN now()+interval '10 minutes'
				ELSE NULL END, updated_at=now()
			RETURNING COALESCE(blocked_until>now(),false),attempts=31 AND blocked_until>now()`, bucket).Scan(&denied, &firstBlock)
		if err != nil {
			serverError(w, err)
			return false
		}
		if denied {
			blocked = true
			blockedScope = bucket
			newlyBlocked = newlyBlocked || firstBlock
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return false
	}
	if blocked {
		if newlyBlocked {
			_, _ = a.db.Exec(r.Context(), `INSERT INTO auth_abuse_events(kind,scope_hash,ip_hash,route) VALUES('auth_rate_limited',$1,$2,$3)`, blockedScope, ipHash, abuseRoute(r.URL.Path))
		}
		w.Header().Set("Retry-After", "600")
		fail(w, 429, "rate_limited", "Too many attempts. Try again later.")
		return false
	}
	select {
	case a.passwords <- struct{}{}:
		return true
	default:
		_, _ = a.db.Exec(r.Context(), `INSERT INTO auth_abuse_events(kind,scope_hash,ip_hash,route) VALUES('auth_capacity_limited',$1,$2,$3)`, buckets[0], ipHash, abuseRoute(r.URL.Path))
		w.Header().Set("Retry-After", "2")
		fail(w, 429, "busy", "Please try again in a moment.")
		return false
	}
}

func abuseDigest(value string) string {
	key := os.Getenv("GITOWN_SECRET_KEY")
	if key == "" {
		return auth.Digest(value)
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte("gitown-auth-abuse-v1:" + value))
	return hex.EncodeToString(mac.Sum(nil))
}

func abuseRoute(path string) string {
	if strings.HasPrefix(path, "/api/v1/auth/") {
		return path
	}
	if strings.HasPrefix(path, "/api/v1/user/mfa/") {
		return "/api/v1/user/mfa/*"
	}
	if strings.HasPrefix(path, "/api/v1/user/email-verification") {
		return "/api/v1/user/email-verification/*"
	}
	return "other"
}

func (a *App) recordFailedLogin(r *http.Request, principal string) {
	a.recordAuthAbuse(r, "login_failed", principal, "/api/v1/auth/login")
}

func (a *App) recordAuthAbuse(r *http.Request, kind, principal, route string) {
	ip := trustedClientIP(r, a.cfg.TrustedProxies)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO auth_abuse_events(kind,scope_hash,ip_hash,route)
		VALUES($1,$2,$3,$4)`, kind, abuseDigest("principal:"+strings.ToLower(strings.TrimSpace(principal))), abuseDigest("ip:"+ip), abuseRoute(route))
}

func trustedClientIP(r *http.Request, trusted []net.IPNet) string {
	peerHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peerHost = r.RemoteAddr
	}
	peer := net.ParseIP(peerHost)
	if peer == nil {
		return peerHost
	}
	if !isTrustedProxy(peer, trusted) {
		return peer.String()
	}
	chain := make([]net.IP, 0, 1+strings.Count(r.Header.Get("X-Forwarded-For"), ","))
	for _, raw := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return peer.String()
		}
		chain = append(chain, ip)
	}
	chain = append(chain, peer)
	for i := len(chain) - 1; i >= 0; i-- {
		if !isTrustedProxy(chain[i], trusted) {
			return chain[i].String()
		}
	}
	return chain[0].String()
}

func isTrustedProxy(ip net.IP, trusted []net.IPNet) bool {
	for _, network := range trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (a *App) abuseOverview(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !isOperator(u) {
		fail(w, 403, "forbidden", "This endpoint is restricted to configured GITOWN operators.")
		return
	}
	type event struct {
		ID        int64  `json:"id"`
		Kind      string `json:"kind"`
		Scope     string `json:"scope_fingerprint"`
		IP        string `json:"network_fingerprint"`
		Route     string `json:"route"`
		CreatedAt string `json:"created_at"`
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,kind,left(scope_hash,12),left(ip_hash,12),route,created_at::text
		FROM auth_abuse_events WHERE created_at>now()-interval '7 days' ORDER BY id DESC LIMIT 200`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := make([]event, 0)
	for rows.Next() {
		var item event
		if err = rows.Scan(&item.ID, &item.Kind, &item.Scope, &item.IP, &item.Route, &item.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	rows.Close()
	blockedRows, err := a.db.Query(r.Context(), `SELECT left(bucket_hash,12),attempts,blocked_until::text,updated_at::text
		FROM auth_rate_limit_buckets WHERE blocked_until>now() ORDER BY blocked_until DESC LIMIT 200`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer blockedRows.Close()
	type blockedBucket struct {
		Fingerprint string `json:"fingerprint"`
		Attempts    int    `json:"attempts_in_window"`
		BlockedTo   string `json:"blocked_until"`
		UpdatedAt   string `json:"updated_at"`
	}
	blocked := make([]blockedBucket, 0)
	for blockedRows.Next() {
		var item blockedBucket
		if err = blockedRows.Scan(&item.Fingerprint, &item.Attempts, &item.BlockedTo, &item.UpdatedAt); err != nil {
			serverError(w, err)
			return
		}
		blocked = append(blocked, item)
	}
	if err = blockedRows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"window_days": 7, "events": items, "currently_blocked": blocked})
}
