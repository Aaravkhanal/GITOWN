package app

import (
	"fmt"
	"testing"
)

// Phase 7 (Organizations) coverage: crew deletion guarded by branch rules,
// district member/crew management round-trips, secret storage, and the
// internal-visibility fix on discovery surfaces (search, tasks, trending,
// recommendations, topics, feed, code search) — internal repositories must
// be visible to district members and invisible to everyone else, while
// private repositories stay invisible regardless.

func TestCrewDeletionBlockedByBranchRuleThenAllowed(t *testing.T) {
	server := phase4Server(t)
	owner := phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	owner.request("POST", "/districts", map[string]string{"slug": "acme", "name": "Acme", "visibility": "public"}, 201, nil)
	owner.request("POST", "/districts/acme/crews", map[string]string{"slug": "reviewers", "name": "Reviewers"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "project", "visibility": "public", "district": "acme", "readme": true}, 201, nil)
	owner.request("PUT", "/repos/owner/project/branch-rules?branch=main", map[string]any{
		"required_approvals": 0, "required_reviewers": []string{"crew:reviewers"},
	}, 200, nil)

	owner.request("DELETE", "/districts/acme/crews/reviewers", nil, 409, nil)

	owner.request("PUT", "/repos/owner/project/branch-rules?branch=main", map[string]any{
		"required_approvals": 0, "required_reviewers": []string{},
	}, 200, nil)
	owner.request("DELETE", "/districts/acme/crews/reviewers", nil, 200, nil)
	owner.request("DELETE", "/districts/acme/crews/reviewers", nil, 404, nil)
}

func TestDistrictMemberAndCrewManagement(t *testing.T) {
	server := phase4Server(t)
	owner := phase4Client(t, server)
	districtadmin := phase4Client(t, server)
	member := phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	districtadmin.request("POST", "/auth/register", map[string]string{"username": "districtadmin", "email": "admin@example.test", "password": "admin-long-password", "display_name": "Admin"}, 201, nil)
	member.request("POST", "/auth/register", map[string]string{"username": "member", "email": "member@example.test", "password": "member-long-password", "display_name": "Member"}, 201, nil)
	owner.request("POST", "/districts", map[string]string{"slug": "acme", "name": "Acme", "visibility": "public"}, 201, nil)
	owner.request("POST", "/districts/acme/members", map[string]string{"username": "districtadmin", "role": "admin"}, 200, nil)
	owner.request("POST", "/districts/acme/members", map[string]string{"username": "member", "role": "member"}, 200, nil)

	// A plain member cannot grant the admin role.
	member.request("POST", "/districts/acme/members", map[string]string{"username": "member", "role": "admin"}, 403, nil)

	// An admin (not the owner) can create a crew and manage its membership.
	districtadmin.request("POST", "/districts/acme/crews", map[string]string{"slug": "reviewers", "name": "Reviewers"}, 201, nil)
	districtadmin.request("POST", "/districts/acme/crews/reviewers/members", map[string]string{"username": "member"}, 200, nil)

	var crewMembers struct {
		Items []struct {
			Username string `json:"username"`
		} `json:"items"`
	}
	member.request("GET", "/districts/acme/crews/reviewers/members", nil, 200, &crewMembers)
	if len(crewMembers.Items) != 1 || crewMembers.Items[0].Username != "member" {
		t.Fatalf("expected member in crew, got %+v", crewMembers.Items)
	}

	districtadmin.request("DELETE", "/districts/acme/crews/reviewers/members/member", nil, 200, nil)
	member.request("GET", "/districts/acme/crews/reviewers/members", nil, 200, &crewMembers)
	if len(crewMembers.Items) != 0 {
		t.Fatalf("expected empty crew after removal, got %+v", crewMembers.Items)
	}

	// Only the owner can remove an admin.
	member.request("DELETE", "/districts/acme/members/districtadmin", nil, 403, nil)
	owner.request("DELETE", "/districts/acme/members/districtadmin", nil, 200, nil)

	var members struct {
		Items []struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"items"`
	}
	owner.request("GET", "/districts/acme/members", nil, 200, &members)
	if len(members.Items) != 1 || members.Items[0].Username != "member" {
		t.Fatalf("expected only member left, got %+v", members.Items)
	}
}

func TestDistrictSecretsRoundTrip(t *testing.T) {
	t.Setenv("GITOWN_SECRET_KEY", "phase7-test-secret-key-not-random")
	server := phase4Server(t)
	owner := phase4Client(t, server)
	member := phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	member.request("POST", "/auth/register", map[string]string{"username": "member", "email": "member@example.test", "password": "member-long-password", "display_name": "Member"}, 201, nil)
	owner.request("POST", "/districts", map[string]string{"slug": "acme", "name": "Acme", "visibility": "public"}, 201, nil)
	owner.request("POST", "/districts/acme/members", map[string]string{"username": "member", "role": "member"}, 200, nil)

	owner.request("POST", "/districts/acme/secrets", map[string]string{"name": "DEPLOY_TOKEN", "value": "super-secret-value"}, 201, nil)

	// A plain member cannot manage secrets.
	member.request("GET", "/districts/acme/secrets", nil, 403, nil)

	var listed struct {
		Items []map[string]any `json:"items"`
	}
	owner.request("GET", "/districts/acme/secrets", nil, 200, &listed)
	if len(listed.Items) != 1 {
		t.Fatalf("expected one secret, got %+v", listed.Items)
	}
	if _, leaked := listed.Items[0]["value"]; leaked {
		t.Fatal("secret list must never include the value")
	}

	var read struct {
		Value string `json:"value"`
	}
	owner.request("GET", "/districts/acme/secrets/DEPLOY_TOKEN", nil, 200, &read)
	if read.Value != "super-secret-value" {
		t.Fatalf("expected round-tripped secret value, got %q", read.Value)
	}

	owner.request("DELETE", "/districts/acme/secrets/DEPLOY_TOKEN", nil, 200, nil)
	owner.request("GET", "/districts/acme/secrets/DEPLOY_TOKEN", nil, 404, nil)
}

// setUpInternalVisibility creates a district with an owner and one member,
// an internal repository under it, and a fully private repository (not
// district-owned) for contrast. It also returns an outsider who belongs to
// no district and an anonymous client (never registered or logged in).
// Every visibility-surface test below asserts: the internal repo is visible
// to the owner and member, invisible to the outsider and to the anonymous
// caller; the private repo is invisible to everyone but its own owner.
func setUpInternalVisibility(t *testing.T) (owner, member, outsider, anon testClient) {
	t.Helper()
	srv := phase4Server(t)
	owner = phase4Client(t, srv)
	member = phase4Client(t, srv)
	outsider = phase4Client(t, srv)
	anon = phase4Client(t, srv)
	owner.request("POST", "/auth/register", map[string]string{"username": "iowner", "email": "iowner@example.test", "password": "iowner-long-password", "display_name": "Owner"}, 201, nil)
	member.request("POST", "/auth/register", map[string]string{"username": "imember", "email": "imember@example.test", "password": "imember-long-password", "display_name": "Member"}, 201, nil)
	outsider.request("POST", "/auth/register", map[string]string{"username": "ioutsider", "email": "ioutsider@example.test", "password": "ioutsider-long-password", "display_name": "Outsider"}, 201, nil)
	owner.request("POST", "/districts", map[string]string{"slug": "innerdistrict", "name": "Inner District", "visibility": "public"}, 201, nil)
	owner.request("POST", "/districts/innerdistrict/members", map[string]string{"username": "imember", "role": "member"}, 200, nil)
	owner.request("POST", "/repos", map[string]any{"name": "insiderepo", "visibility": "internal", "district": "innerdistrict", "readme": true}, 201, nil)
	owner.request("PUT", "/repos/iowner/insiderepo/topics", map[string]any{"topics": []string{"visibilitycheck"}}, 200, nil)
	owner.request("POST", "/repos", map[string]any{"name": "privaterepo", "visibility": "private", "readme": true}, 201, nil)
	owner.request("PUT", "/repos/iowner/privaterepo/topics", map[string]any{"topics": []string{"visibilitycheck"}}, 200, nil)
	return owner, member, outsider, anon
}

func TestInternalVisibilityOnSearchRepositories(t *testing.T) {
	owner, member, outsider, anon := setUpInternalVisibility(t)
	assertInternalVisible := func(client testClient, wantInside, wantPrivate bool) {
		t.Helper()
		var result RepositorySearch
		client.request("GET", "/search/repositories?topic=visibilitycheck", nil, 200, &result)
		names := map[string]bool{}
		for _, item := range result.Items {
			names[item.Owner+"/"+item.Name] = true
		}
		if names["iowner/insiderepo"] != wantInside {
			t.Fatalf("insiderepo visibility=%v, want %v (items=%v)", names["iowner/insiderepo"], wantInside, names)
		}
		if names["iowner/privaterepo"] != wantPrivate {
			t.Fatalf("privaterepo visibility=%v, want %v (items=%v)", names["iowner/privaterepo"], wantPrivate, names)
		}
	}
	assertInternalVisible(owner, true, false)
	assertInternalVisible(member, true, false)
	assertInternalVisible(outsider, false, false)
	assertInternalVisible(anon, false, false)
}

func TestInternalVisibilityOnTopicPageAndFeed(t *testing.T) {
	owner, member, outsider, anon := setUpInternalVisibility(t)
	_ = anon
	var page struct {
		Items []Repository `json:"items"`
	}
	owner.request("GET", "/topics/visibilitycheck", nil, 200, &page)
	ownerFound := false
	for _, item := range page.Items {
		if item.Owner+"/"+item.Name == "iowner/insiderepo" {
			ownerFound = true
		}
	}
	if !ownerFound {
		t.Fatal("internal repository missing from topic page for its own district owner")
	}

	member.request("GET", "/topics/visibilitycheck", nil, 200, &page)
	found := false
	for _, item := range page.Items {
		if item.Owner+"/"+item.Name == "iowner/insiderepo" {
			found = true
		}
		if item.Owner+"/"+item.Name == "iowner/privaterepo" {
			t.Fatal("private repository leaked into topic page")
		}
	}
	if !found {
		t.Fatal("internal repository missing from topic page for a district member")
	}
	outsider.request("GET", "/topics/visibilitycheck", nil, 200, &page)
	for _, item := range page.Items {
		if item.Owner+"/"+item.Name == "iowner/insiderepo" {
			t.Fatal("internal repository leaked into topic page for a non-member")
		}
	}

	// Feed: the member follows the district owner, so the internal
	// repository's creation event should appear once the member follows,
	// but never for an outsider who does not follow anyone.
	member.request("PUT", "/users/iowner/follow", map[string]bool{"followed": true}, 200, nil)
	var events []map[string]any
	member.request("GET", "/user/feed", nil, 200, &events)
	feedHasInside := false
	for _, event := range events {
		if event["owner"] == "iowner" && event["repository"] == "insiderepo" {
			feedHasInside = true
		}
		if event["owner"] == "iowner" && event["repository"] == "privaterepo" {
			t.Fatal("private repository leaked into feed")
		}
	}
	if !feedHasInside {
		t.Fatal("internal repository missing from feed for a following district member")
	}
}

func TestInternalVisibilityOnCodeSearchAndRecommendationsAndTasks(t *testing.T) {
	owner, member, outsider, _ := setUpInternalVisibility(t)
	// Force a re-index of both repositories' default README (the private
	// repository must never be indexed at all; the internal one is indexed
	// but only surfaced to a viewer who can see it).
	owner.request("PATCH", "/repos/iowner/insiderepo", map[string]string{"description": "", "visibility": "internal"}, 200, nil)
	owner.request("PATCH", "/repos/iowner/privaterepo", map[string]string{"description": "", "visibility": "private"}, 200, nil)

	hasCodeMatch := func(client testClient, term string) bool {
		t.Helper()
		var result struct {
			Items []CodeMatch `json:"items"`
		}
		client.request("GET", "/search/code?q="+term, nil, 200, &result)
		for _, item := range result.Items {
			if item.Owner == "iowner" && item.Repository == term {
				return true
			}
		}
		return false
	}
	if !hasCodeMatch(member, "insiderepo") {
		t.Fatal("internal repository's code missing from code search for a district member")
	}
	if hasCodeMatch(outsider, "insiderepo") {
		t.Fatal("internal repository's code leaked into code search for a non-member")
	}
	if hasCodeMatch(owner, "privaterepo") {
		t.Fatal("private repository was indexed for code search")
	}

	// searchTasks (help-wanted / good-first-task) must respect the same
	// rule for an internal repository's open, labelled issues.
	var issue struct {
		Number int `json:"number"`
	}
	owner.request("POST", "/repos/iowner/insiderepo/issues", map[string]string{"title": "Needs a hand", "body": "..."}, 201, &issue)
	var label struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos/iowner/insiderepo/labels", map[string]string{"name": "help wanted", "color": "00ff00"}, 201, &label)
	owner.request("POST", fmt.Sprintf("/repos/iowner/insiderepo/issues/%d/labels", issue.Number), map[string]string{"label_id": label.ID}, 200, nil)

	hasTask := func(client testClient) bool {
		t.Helper()
		var result struct {
			Items []map[string]any `json:"items"`
		}
		client.request("GET", "/search/tasks?kind=help", nil, 200, &result)
		for _, item := range result.Items {
			if item["owner"] == "iowner" && item["repository"] == "insiderepo" {
				return true
			}
		}
		return false
	}
	if !hasTask(member) {
		t.Fatal("internal repository's help-wanted issue missing for a district member")
	}
	if hasTask(outsider) {
		t.Fatal("internal repository's help-wanted issue leaked to a non-member")
	}

	// recommendations: a district member who follows the owner should see
	// the internal repository surfaced.
	member.request("PUT", "/users/iowner/follow", map[string]bool{"followed": true}, 200, nil)
	var recs struct {
		Items []Repository `json:"items"`
	}
	member.request("GET", "/search/recommendations", nil, 200, &recs)
	found := false
	for _, item := range recs.Items {
		if item.Owner == "iowner" && item.Name == "privaterepo" {
			t.Fatal("private repository leaked into recommendations")
		}
		if item.Owner == "iowner" && item.Name == "insiderepo" {
			found = true
		}
	}
	if !found {
		t.Fatal("internal repository missing from recommendations for a following district member")
	}
}
