package app

import (
	"net/http"
	"testing"
)

func TestRequiresStepUpForHighImpactRoutes(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/api/v1/user/tokens", true},
		{http.MethodDelete, "/api/v1/user/tokens/token-id", true},
		{http.MethodDelete, "/api/v1/user/sessions/session-id", true},
		{http.MethodPost, "/api/v1/user/oauth-apps/app-id/secret", true},
		{http.MethodDelete, "/api/v1/repos/owner/repository", true},
		{http.MethodPost, "/api/v1/repos/owner/repository/transfer", true},
		{http.MethodPatch, "/api/v1/repos/owner/repository/members/alice", true},
		{http.MethodPost, "/api/v1/repos/owner/repository/invitations", true},
		{http.MethodPut, "/api/v1/repos/owner/repository/branch-rules", true},
		{http.MethodPost, "/api/v1/repos/owner/repository/webhooks", true},
		{http.MethodPost, "/api/v1/districts/team/secrets", true},
		{http.MethodPost, "/api/v1/user/ssh-keys", true},
		{http.MethodPost, "/api/v1/user/mfa/disable", false}, // already requires password + second factor
		{http.MethodPost, "/api/v1/repos/owner/repository/issues", false},
		{http.MethodPost, "/api/v1/repos/owner/repository/issues/3/comments", false},
		{http.MethodPost, "/api/v1/user/step-up", false},
	}
	for _, test := range tests {
		if got := requiresStepUp(test.method, test.path); got != test.want {
			t.Errorf("requiresStepUp(%q, %q) = %t, want %t", test.method, test.path, got, test.want)
		}
	}
}
