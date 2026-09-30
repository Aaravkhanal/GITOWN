package app

import (
	"fmt"
	"strings"
	"testing"
)

// TestDiscoveryDecorateAllBatchedMatchesPerRow proves the batched decorateAll
// path (used by search, topics, recommendations, and collections) produces
// the same role and capability fields as the old per-row decorate for a
// mixed page: the viewer's own repository, a repository where the viewer is
// a direct collaborator, a repository reached through district admin
// status, a repository reached through a district's base permission, and an
// unrelated repository.
func TestDiscoveryDecorateAllBatchedMatchesPerRow(t *testing.T) {
	server := phase4Server(t)
	viewer := phase4Client(t, server)
	owner2 := phase4Client(t, server)
	viewer.request("POST", "/auth/register", map[string]string{"username": "viewer", "email": "viewer@example.test", "password": "viewer-long-password", "display_name": "Viewer"}, 201, nil)
	owner2.request("POST", "/auth/register", map[string]string{"username": "owner2", "email": "owner2@example.test", "password": "owner2-long-password", "display_name": "Owner Two"}, 201, nil)

	viewer.request("POST", "/repos", map[string]any{"name": "mine", "visibility": "public"}, 201, nil)

	owner2.request("POST", "/repos", map[string]any{"name": "member", "visibility": "public"}, 201, nil)
	inviteAndAccept(owner2, viewer, "owner2/member", "viewer", "triage")

	owner2.request("POST", "/districts", map[string]string{"slug": "acme", "name": "Acme", "visibility": "public"}, 201, nil)
	owner2.request("POST", "/districts/acme/members", map[string]string{"username": "viewer", "role": "admin"}, 200, nil)
	owner2.request("POST", "/repos", map[string]any{"name": "districtowner", "visibility": "public", "district": "acme"}, 201, nil)

	owner2.request("POST", "/districts", map[string]string{"slug": "beta", "name": "Beta", "visibility": "public"}, 201, nil)
	owner2.request("POST", "/districts/beta/members", map[string]string{"username": "viewer", "role": "member"}, 200, nil)
	owner2.request("PATCH", "/districts/beta", map[string]any{"allow_public": true, "allow_outside_collaborators": true, "repo_creation": "admin", "base_permission": "write"}, 200, nil)
	owner2.request("POST", "/repos", map[string]any{"name": "districtmember", "visibility": "public", "district": "beta"}, 201, nil)

	owner2.request("POST", "/repos", map[string]any{"name": "unrelated", "visibility": "public"}, 201, nil)

	for _, repo := range []string{"viewer/mine", "owner2/member", "owner2/districtowner", "owner2/districtmember", "owner2/unrelated"} {
		client := owner2
		if strings.HasPrefix(repo, "viewer/") {
			client = viewer
		}
		client.request("PUT", "/repos/"+repo+"/topics", map[string]any{"topics": []string{"decoratecheck"}}, 200, nil)
	}

	var result RepositorySearch
	viewer.request("GET", "/search/repositories?topic=decoratecheck", nil, 200, &result)
	if len(result.Items) != 5 {
		t.Fatalf("expected 5 repositories, got %d", len(result.Items))
	}
	byName := map[string]Repository{}
	for _, item := range result.Items {
		byName[item.Owner+"/"+item.Name] = item
	}
	check := func(key, role string, write, triage, manage, maintain bool) {
		t.Helper()
		repo, ok := byName[key]
		if !ok {
			t.Fatalf("missing %s in results", key)
		}
		if repo.Role != role || repo.CanWrite != write || repo.CanTriage != triage || repo.CanManage != manage || repo.CanMaintain != maintain {
			t.Fatalf("%s: got role=%q write=%v triage=%v manage=%v maintain=%v, want role=%q write=%v triage=%v manage=%v maintain=%v", key, repo.Role, repo.CanWrite, repo.CanTriage, repo.CanManage, repo.CanMaintain, role, write, triage, manage, maintain)
		}
	}
	check("viewer/mine", "owner", true, true, true, true)
	check("owner2/member", "triage", false, true, false, false)
	check("owner2/districtowner", "maintain", true, true, true, true)
	check("owner2/districtmember", "write", true, true, false, false)
	check("owner2/unrelated", "", false, false, false, false)
}

func TestTopicCatalogPagination(t *testing.T) {
	server := phase4Server(t)
	owner := phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)

	allTopics := map[string]bool{}
	for repoIndex := 0; repoIndex < 6; repoIndex++ {
		name := fmt.Sprintf("catalog%d", repoIndex)
		owner.request("POST", "/repos", map[string]any{"name": name, "visibility": "public"}, 201, nil)
		var topics []string
		for topicIndex := 0; topicIndex < 9; topicIndex++ {
			topic := fmt.Sprintf("cat-%d-%d", repoIndex, topicIndex)
			topics = append(topics, topic)
			allTopics[topic] = true
		}
		owner.request("PUT", "/repos/owner/"+name+"/topics", map[string]any{"topics": topics}, 200, nil)
	}
	if len(allTopics) != 54 {
		t.Fatalf("test setup produced %d distinct topics, want 54", len(allTopics))
	}

	var page1 struct {
		Items []struct {
			Topic string `json:"topic"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	owner.request("GET", "/topics?offset=0", nil, 200, &page1)
	if len(page1.Items) != 50 || !page1.HasMore {
		t.Fatalf("page 1: got %d items, has_more=%v, want 50 items and has_more=true", len(page1.Items), page1.HasMore)
	}
	var page2 struct {
		Items []struct {
			Topic string `json:"topic"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	owner.request("GET", "/topics?offset=50", nil, 200, &page2)
	if len(page2.Items) != 4 || page2.HasMore {
		t.Fatalf("page 2: got %d items, has_more=%v, want 4 items and has_more=false", len(page2.Items), page2.HasMore)
	}
	seen := map[string]bool{}
	for _, item := range append(page1.Items, page2.Items...) {
		if seen[item.Topic] {
			t.Fatalf("topic %s appeared on both pages", item.Topic)
		}
		seen[item.Topic] = true
	}
	if len(seen) != 54 {
		t.Fatalf("pages together covered %d topics, want 54", len(seen))
	}
}

func TestTopicPagePagination(t *testing.T) {
	server := phase4Server(t)
	owner := phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)

	for i := 0; i < 32; i++ {
		name := fmt.Sprintf("shared%d", i)
		owner.request("POST", "/repos", map[string]any{"name": name, "visibility": "public"}, 201, nil)
		owner.request("PUT", "/repos/owner/"+name+"/topics", map[string]any{"topics": []string{"sharedtopic"}}, 200, nil)
	}

	var page1 struct {
		Items   []Repository `json:"items"`
		HasMore bool         `json:"has_more"`
	}
	owner.request("GET", "/topics/sharedtopic?offset=0", nil, 200, &page1)
	if len(page1.Items) != 30 || !page1.HasMore {
		t.Fatalf("page 1: got %d items, has_more=%v, want 30 items and has_more=true", len(page1.Items), page1.HasMore)
	}
	var page2 struct {
		Items   []Repository `json:"items"`
		HasMore bool         `json:"has_more"`
	}
	owner.request("GET", "/topics/sharedtopic?offset=30", nil, 200, &page2)
	if len(page2.Items) != 2 || page2.HasMore {
		t.Fatalf("page 2: got %d items, has_more=%v, want 2 items and has_more=false", len(page2.Items), page2.HasMore)
	}
	seen := map[string]bool{}
	for _, item := range append(page1.Items, page2.Items...) {
		if seen[item.Name] {
			t.Fatalf("repository %s appeared on both pages", item.Name)
		}
		seen[item.Name] = true
		if item.Role != "owner" || !item.CanManage {
			t.Fatalf("%s: expected the owner's own decorated role, got role=%q can_manage=%v", item.Name, item.Role, item.CanManage)
		}
	}
	if len(seen) != 32 {
		t.Fatalf("pages together covered %d repositories, want 32", len(seen))
	}
}

func TestCollectionsPagination(t *testing.T) {
	server := phase4Server(t)
	owner := phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)

	for i := 0; i < 21; i++ {
		slug := fmt.Sprintf("collection-%d", i)
		owner.request("POST", "/collections", map[string]string{"slug": slug, "title": slug, "description": ""}, 201, nil)
	}

	var page1 struct {
		Items []struct {
			Slug string `json:"slug"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	owner.request("GET", "/collections?offset=0", nil, 200, &page1)
	if len(page1.Items) != 20 || !page1.HasMore {
		t.Fatalf("page 1: got %d items, has_more=%v, want 20 items and has_more=true", len(page1.Items), page1.HasMore)
	}
	var page2 struct {
		Items []struct {
			Slug string `json:"slug"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	owner.request("GET", "/collections?offset=20", nil, 200, &page2)
	if len(page2.Items) != 1 || page2.HasMore {
		t.Fatalf("page 2: got %d items, has_more=%v, want 1 item and has_more=false", len(page2.Items), page2.HasMore)
	}
	seen := map[string]bool{}
	for _, item := range append(page1.Items, page2.Items...) {
		if seen[item.Slug] {
			t.Fatalf("collection %s appeared on both pages", item.Slug)
		}
		seen[item.Slug] = true
	}
	if len(seen) != 21 {
		t.Fatalf("pages together covered %d collections, want 21", len(seen))
	}
}

func TestSearchTasksPaginationAndKinds(t *testing.T) {
	server := phase4Server(t)
	owner := phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "tasks", "visibility": "public"}, 201, nil)
	var helpLabel, firstLabel struct{ ID string }
	owner.request("POST", "/repos/owner/tasks/labels", map[string]string{"name": "help wanted", "color": "0e8a16"}, 201, &helpLabel)
	owner.request("POST", "/repos/owner/tasks/labels", map[string]string{"name": "good first task", "color": "7057ff"}, 201, &firstLabel)

	for i := 0; i < 26; i++ {
		var issue struct{ Number int }
		owner.request("POST", "/repos/owner/tasks/issues", map[string]string{"title": fmt.Sprintf("Help %d", i)}, 201, &issue)
		owner.request("POST", fmt.Sprintf("/repos/owner/tasks/issues/%d/labels", issue.Number), map[string]string{"label_id": helpLabel.ID}, 200, nil)
	}
	for i := 0; i < 3; i++ {
		var issue struct{ Number int }
		owner.request("POST", "/repos/owner/tasks/issues", map[string]string{"title": fmt.Sprintf("First %d", i)}, 201, &issue)
		owner.request("POST", fmt.Sprintf("/repos/owner/tasks/issues/%d/labels", issue.Number), map[string]string{"label_id": firstLabel.ID}, 200, nil)
	}

	var help1, help2 struct {
		Items   []map[string]any `json:"items"`
		HasMore bool             `json:"has_more"`
	}
	owner.request("GET", "/search/tasks?kind=help&offset=0", nil, 200, &help1)
	if len(help1.Items) != 25 || !help1.HasMore {
		t.Fatalf("help page 1: got %d items, has_more=%v, want 25 items and has_more=true", len(help1.Items), help1.HasMore)
	}
	owner.request("GET", "/search/tasks?kind=help&offset=25", nil, 200, &help2)
	if len(help2.Items) != 1 || help2.HasMore {
		t.Fatalf("help page 2: got %d items, has_more=%v, want 1 item and has_more=false", len(help2.Items), help2.HasMore)
	}

	var first struct {
		Items   []map[string]any `json:"items"`
		HasMore bool             `json:"has_more"`
	}
	owner.request("GET", "/search/tasks?kind=first", nil, 200, &first)
	if len(first.Items) != 3 || first.HasMore {
		t.Fatalf("first page: got %d items, has_more=%v, want 3 items and has_more=false", len(first.Items), first.HasMore)
	}
	for _, item := range first.Items {
		title, _ := item["title"].(string)
		if !strings.HasPrefix(title, "First ") {
			t.Fatalf("kind=first leaked a help-wanted issue: %v", item)
		}
	}
}

func TestRecommendationsIncludeFollowedOwners(t *testing.T) {
	server := phase4Server(t)
	viewer := phase4Client(t, server)
	other := phase4Client(t, server)
	stranger := phase4Client(t, server)
	viewer.request("POST", "/auth/register", map[string]string{"username": "viewer", "email": "viewer@example.test", "password": "viewer-long-password", "display_name": "Viewer"}, 201, nil)
	other.request("POST", "/auth/register", map[string]string{"username": "followed", "email": "followed@example.test", "password": "followed-long-password", "display_name": "Followed"}, 201, nil)
	stranger.request("POST", "/auth/register", map[string]string{"username": "stranger", "email": "stranger@example.test", "password": "stranger-long-password", "display_name": "Stranger"}, 201, nil)

	// The viewer needs an existing topic-affinity match so an empty result
	// set doesn't fall back to plain trending, which would make every
	// public repository appear "recommended" regardless of the follow
	// signal under test.
	viewer.request("POST", "/repos", map[string]any{"name": "anchor", "visibility": "public"}, 201, nil)
	viewer.request("PUT", "/repos/viewer/anchor/topics", map[string]any{"topics": []string{"anchor-topic"}}, 200, nil)
	stranger.request("POST", "/repos", map[string]any{"name": "shared-topic-work", "visibility": "public"}, 201, nil)
	stranger.request("PUT", "/repos/stranger/shared-topic-work/topics", map[string]any{"topics": []string{"anchor-topic"}}, 200, nil)

	// A repository sharing no topic with anything the viewer owns or
	// Sparked, so the only signal that can surface it is the follow.
	other.request("POST", "/repos", map[string]any{"name": "unrelated-work", "visibility": "public"}, 201, nil)

	var before struct {
		Items        []Repository `json:"items"`
		Personalized bool         `json:"personalized"`
	}
	viewer.request("GET", "/search/recommendations", nil, 200, &before)
	if !before.Personalized {
		t.Fatal("expected personalized recommendations from the topic-affinity match")
	}
	foundStranger := false
	for _, item := range before.Items {
		if item.Owner == "followed" && item.Name == "unrelated-work" {
			t.Fatal("followed owner's repository appeared before the follow was created")
		}
		if item.Owner == "stranger" && item.Name == "shared-topic-work" {
			foundStranger = true
		}
	}
	if !foundStranger {
		t.Fatal("expected the topic-affinity match to appear before the follow")
	}

	viewer.request("PUT", "/users/followed/follow", map[string]bool{"followed": true}, 200, nil)

	var after struct {
		Items        []Repository `json:"items"`
		Personalized bool         `json:"personalized"`
	}
	viewer.request("GET", "/search/recommendations", nil, 200, &after)
	if !after.Personalized {
		t.Fatal("expected personalized recommendations once the viewer follows someone")
	}
	found := false
	for _, item := range after.Items {
		if item.Owner == "followed" && item.Name == "unrelated-work" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the followed owner's repository in recommendations")
	}
}
