package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPIRateLimitIsolationExpiryAndRedaction(t *testing.T) {
	application := &App{apiRates: make(map[string]rateWindow)}
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	request := func(remote, token string) (*httptest.ResponseRecorder, bool) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		req.RemoteAddr = remote
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		return w, application.apiRateLimitAt(w, req, 2, now)
	}
	if _, ok := request("192.0.2.1:1234", "secret-token-value"); !ok {
		t.Fatal("first request denied")
	}
	if _, ok := request("192.0.2.1:1234", "secret-token-value"); !ok {
		t.Fatal("second request denied")
	}
	w, ok := request("192.0.2.1:1234", "secret-token-value")
	if ok || w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("expected bounded 429 with retry header, got ok=%v status=%d", ok, w.Code)
	}
	if _, ok := request("192.0.2.2:1234", ""); !ok {
		t.Fatal("rate limit leaked across client IPs")
	}
	for key := range application.apiRates {
		if strings.Contains(key, "secret-token-value") {
			t.Fatal("raw bearer credential retained in rate-limit state")
		}
	}
	now = now.Add(61 * time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("Authorization", "Bearer secret-token-value")
	w = httptest.NewRecorder()
	if !application.apiRateLimitAt(w, req, 2, now) {
		t.Fatal("expired bucket did not reset")
	}
	if len(application.apiRates) > 2 {
		t.Fatalf("expired buckets were not pruned: %d", len(application.apiRates))
	}
}

func TestAPIRateLimitConcurrentBurstStaysWithinBudget(t *testing.T) {
	const limit, requests = 50, 400
	application := &App{apiRates: make(map[string]rateWindow)}
	now := time.Now()
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/load", nil)
			req.RemoteAddr = "198.51.100.42:54321"
			w := httptest.NewRecorder()
			if application.apiRateLimitAt(w, req, limit, now) {
				accepted.Add(1)
			} else if w.Code != http.StatusTooManyRequests {
				t.Errorf("request %d unexpected status %d", i, w.Code)
			}
		}(i)
	}
	wg.Wait()
	if got := accepted.Load(); got != limit {
		t.Fatalf("accepted %d requests in concurrent burst; want exactly %d", got, limit)
	}
	if got := application.apiRates["ip:198.51.100.42"].count; got != limit {
		t.Fatalf("bucket count = %d", got)
	}
}
