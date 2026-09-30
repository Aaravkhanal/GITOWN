package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type PublicRepository struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Archived    bool      `json:"archived"`
	CreatedAt   time.Time `json:"created_at"`
	Homepage    string    `json:"homepage"`
	Stack       string    `json:"stack"`
	Sparks      int       `json:"sparks"`
	LatestDrop  string    `json:"latest_drop"`
	Downloads   int       `json:"downloads"`
}

// Contributions count public work that helped something ship. Sparks and
// raw commit counts are deliberately left out.
type Contributions struct {
	MergedUnites       int `json:"merged_unites"`
	Approvals          int `json:"approvals"`
	Reviews            int `json:"reviews"`
	HelpedShip         int `json:"helped_ship"`
	ClosedIssues       int `json:"closed_issues"`
	ResolvedIssues     int `json:"resolved_issues"`
	Pushes             int `json:"pushes"`
	DocsUnites         int `json:"docs_unites"`
	ExternalMerges     int `json:"external_merges"`
	PublicRepositories int `json:"public_repositories"`
}

type Badge struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

type ProfileLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Profile struct {
	Username            string             `json:"username"`
	DisplayName         string             `json:"display_name"`
	Bio                 string             `json:"bio"`
	Website             string             `json:"website"`
	Location            string             `json:"location"`
	Skills              string             `json:"skills"`
	SkillTags           []string           `json:"skill_tags"`
	Availability        string             `json:"availability"`
	OpenToCollaborators bool               `json:"open_to_collaborators"`
	Links               []ProfileLink      `json:"links"`
	CreatedAt           time.Time          `json:"created_at"`
	Followers           int                `json:"followers"`
	Following           int                `json:"following"`
	Followed            bool               `json:"followed"`
	Contributions       Contributions      `json:"contributions"`
	Badges              []Badge            `json:"badges"`
	Showcase            []PublicRepository `json:"showcase"`
	Repositories        []PublicRepository `json:"repositories"`
}

// skillTags splits the free-form skills field into distinct tags.
func skillTags(skills string) []string {
	tags := []string{}
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(skills, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		tag := strings.TrimSpace(part)
		key := strings.ToLower(tag)
		if tag == "" || seen[key] || len(tags) == 20 {
			continue
		}
		seen[key] = true
		tags = append(tags, tag)
	}
	return tags
}

// badgeTiers holds the thresholds for bronze, silver, and gold.
var badgeTiers = []struct {
	id, label, unit string
	thresholds      [3]int
	count           func(Contributions) int
}{
	{"merged", "Merged contributor", "merged Unite request", [3]int{1, 10, 50}, func(c Contributions) int { return c.MergedUnites }},
	{"reviewer", "Trusted reviewer", "approved Unite request that shipped", [3]int{5, 25, 100}, func(c Contributions) int { return c.HelpedShip }},
	{"resolver", "Issue resolver", "resolved issue as assignee", [3]int{1, 10, 50}, func(c Contributions) int { return c.ResolvedIssues }},
	{"documentarian", "Documentation improver", "merged documentation change", [3]int{1, 5, 20}, func(c Contributions) int { return c.DocsUnites }},
	{"maintainer", "Welcoming maintainer", "merged contribution from someone else", [3]int{1, 10, 50}, func(c Contributions) int { return c.ExternalMerges }},
}

func contributionBadges(c Contributions) []Badge {
	badges := []Badge{}
	tiers := [3]string{"bronze", "silver", "gold"}
	for _, rule := range badgeTiers {
		count := rule.count(c)
		level := -1
		for index, threshold := range rule.thresholds {
			if count >= threshold {
				level = index
			}
		}
		if level < 0 {
			continue
		}
		unit := rule.unit
		if count != 1 {
			unit += "s"
		}
		badges = append(badges, Badge{ID: rule.id, Label: rule.label, Tier: tiers[level], Reason: fmt.Sprintf("%d %s", count, unit)})
	}
	return badges
}

// documentationUnites counts recent merged public Unite requests by the
// user that touched Markdown files or a docs directory.
func (a *App) documentationUnites(ctx context.Context, userID string) (int, error) {
	rows, err := a.db.Query(ctx, `SELECT p.repository_id,p.expected_base_sha,p.merge_sha FROM pull_requests p JOIN repositories r ON r.id=p.repository_id
		WHERE p.author_id=$1 AND p.state='merged' AND p.merge_sha IS NOT NULL AND p.expected_base_sha IS NOT NULL AND r.visibility='public' AND r.deleted_at IS NULL
		ORDER BY p.merged_at DESC NULLS LAST LIMIT 20`, userID)
	if err != nil {
		return 0, err
	}
	type merged struct{ repo, base, head string }
	var items []merged
	for rows.Next() {
		var item merged
		if err = rows.Scan(&item.repo, &item.base, &item.head); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		out, diffErr := a.git.Run(ctx, item.repo, nil, "diff", "--name-only", item.base, item.head)
		if diffErr != nil {
			continue
		}
		for _, path := range strings.Split(string(out), "\n") {
			lower := strings.ToLower(path)
			if strings.HasSuffix(lower, ".md") || strings.HasPrefix(lower, "docs/") || strings.Contains(lower, "/docs/") {
				count++
				break
			}
		}
	}
	return count, nil
}

func (a *App) profile(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if !slug.MatchString(username) {
		fail(w, 404, "not_found", "Profile not found.")
		return
	}
	var userID string
	p := Profile{Showcase: []PublicRepository{}, Repositories: []PublicRepository{}, Badges: []Badge{}, Links: []ProfileLink{}}
	err := a.db.QueryRow(r.Context(), `SELECT id,username,display_name,bio,website,location,skills,availability,open_to_collaborators,created_at FROM users WHERE username=$1`, username).
		Scan(&userID, &p.Username, &p.DisplayName, &p.Bio, &p.Website, &p.Location, &p.Skills, &p.Availability, &p.OpenToCollaborators, &p.CreatedAt)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Profile not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	p.SkillTags = skillTags(p.Skills)
	viewerID := ""
	if viewer := a.user(r); viewer != nil {
		viewerID = viewer.ID
	}
	if err = a.db.QueryRow(r.Context(), `SELECT
		(SELECT count(*)::int FROM user_follows WHERE followed_id=$1),
		(SELECT count(*)::int FROM user_follows WHERE follower_id=$1),
		EXISTS(SELECT 1 FROM user_follows WHERE followed_id=$1 AND follower_id::text=$2)`, userID, viewerID).
		Scan(&p.Followers, &p.Following, &p.Followed); err != nil {
		serverError(w, err)
		return
	}
	links, err := a.db.Query(r.Context(), `SELECT label,url FROM user_links WHERE user_id=$1 ORDER BY position`, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	for links.Next() {
		var link ProfileLink
		if err = links.Scan(&link.Label, &link.URL); err != nil {
			links.Close()
			serverError(w, err)
			return
		}
		p.Links = append(p.Links, link)
	}
	links.Close()
	c := &p.Contributions
	if err = a.db.QueryRow(r.Context(), `SELECT
		(SELECT count(*)::int FROM pull_requests p JOIN repositories r ON r.id=p.repository_id WHERE p.author_id=$1 AND p.state='merged' AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM pull_reviews rv JOIN pull_requests p ON p.id=rv.pull_request_id JOIN repositories r ON r.id=p.repository_id WHERE rv.reviewer_id=$1 AND rv.state='approved' AND rv.dismissed_at IS NULL AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM pull_reviews rv JOIN pull_requests p ON p.id=rv.pull_request_id JOIN repositories r ON r.id=p.repository_id WHERE rv.reviewer_id=$1 AND p.author_id<>$1 AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(DISTINCT p.id)::int FROM pull_reviews rv JOIN pull_requests p ON p.id=rv.pull_request_id JOIN repositories r ON r.id=p.repository_id WHERE rv.reviewer_id=$1 AND rv.state='approved' AND rv.dismissed_at IS NULL AND p.state='merged' AND p.author_id<>$1 AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM issues i JOIN repositories r ON r.id=i.repository_id WHERE i.author_id=$1 AND i.state='closed' AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(DISTINCT i.id)::int FROM issue_assignees ia JOIN issues i ON i.id=ia.issue_id JOIN repositories r ON r.id=i.repository_id WHERE ia.user_id=$1 AND i.state='closed' AND i.duplicate_of IS NULL AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM ref_events e JOIN repositories r ON r.id=e.repository_id WHERE e.actor_id=$1 AND e.new_sha<>repeat('0',40) AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM pull_requests p JOIN repositories r ON r.id=p.repository_id WHERE r.owner_id=$1 AND p.author_id<>$1 AND p.state='merged' AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM repositories r WHERE r.owner_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL)`, userID).
		Scan(&c.MergedUnites, &c.Approvals, &c.Reviews, &c.HelpedShip, &c.ClosedIssues, &c.ResolvedIssues, &c.Pushes, &c.ExternalMerges, &c.PublicRepositories); err != nil {
		serverError(w, err)
		return
	}
	if c.MergedUnites > 0 {
		if c.DocsUnites, err = a.documentationUnites(r.Context(), userID); err != nil {
			serverError(w, err)
			return
		}
	}
	p.Badges = contributionBadges(*c)
	showcase, err := a.publicProfileRepositories(r, `FROM profile_repositories pr JOIN repositories r ON r.id=pr.repository_id
		WHERE pr.user_id=$1 AND r.owner_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL ORDER BY pr.position`, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	p.Showcase = showcase
	repos, err := a.publicProfileRepositories(r, `FROM repositories r WHERE r.owner_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL ORDER BY r.created_at DESC LIMIT 100`, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	p.Repositories = repos
	respond(w, 200, p)
}

type FollowEntry struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
}

func (a *App) followers(w http.ResponseWriter, r *http.Request) {
	a.followList(w, r, `SELECT u.username,u.display_name,u.bio FROM user_follows f JOIN users u ON u.id=f.follower_id WHERE f.followed_id=$1 ORDER BY f.created_at DESC,u.username LIMIT 31 OFFSET $2`)
}

func (a *App) following(w http.ResponseWriter, r *http.Request) {
	a.followList(w, r, `SELECT u.username,u.display_name,u.bio FROM user_follows f JOIN users u ON u.id=f.followed_id WHERE f.follower_id=$1 ORDER BY f.created_at DESC,u.username LIMIT 31 OFFSET $2`)
}

func (a *App) followList(w http.ResponseWriter, r *http.Request, query string) {
	username := r.PathValue("username")
	if !slug.MatchString(username) {
		fail(w, 404, "not_found", "Profile not found.")
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 10000 {
			fail(w, 422, "validation_failed", "Offset must be between zero and 10000.")
			return
		}
		offset = value
	}
	var userID string
	if err := a.db.QueryRow(r.Context(), `SELECT id FROM users WHERE username=$1`, username).Scan(&userID); err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Profile not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), query, userID, offset)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []FollowEntry{}
	hasMore := false
	for rows.Next() {
		var item FollowEntry
		if err = rows.Scan(&item.Username, &item.DisplayName, &item.Bio); err != nil {
			serverError(w, err)
			return
		}
		if len(items) == 30 {
			hasMore = true
			break
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "has_more": hasMore})
}

func (a *App) updateFollow(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	username := r.PathValue("username")
	if !slug.MatchString(username) {
		fail(w, 404, "not_found", "Profile not found.")
		return
	}
	var in struct {
		Followed *bool `json:"followed"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Followed == nil {
		fail(w, 422, "validation_failed", "Choose whether to follow this builder.")
		return
	}
	var followedID string
	if err := a.db.QueryRow(r.Context(), `SELECT id FROM users WHERE username=$1`, username).Scan(&followedID); err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Profile not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	if u.ID == followedID {
		fail(w, 422, "validation_failed", "You cannot follow yourself.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if *in.Followed {
		result, execErr := tx.Exec(r.Context(), `INSERT INTO user_follows(follower_id,followed_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, u.ID, followedID)
		if err = execErr; err == nil && result.RowsAffected() == 1 {
			err = notifyFollow(r.Context(), tx, followedID, u.ID)
		}
	} else {
		_, err = tx.Exec(r.Context(), `DELETE FROM user_follows WHERE follower_id=$1 AND followed_id=$2`, u.ID, followedID)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	action := "profile.unfollowed"
	if *in.Followed {
		action = "profile.followed"
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, action, username); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.profile(w, r)
}

// publicProfileRepositories loads repository cards; from is the query body
// after SELECT, aliasing repositories as r and taking the user id as $1.
func (a *App) publicProfileRepositories(r *http.Request, from, userID string) ([]PublicRepository, error) {
	rows, err := a.db.Query(r.Context(), `SELECT r.id,r.name,r.description,(r.archived_at IS NOT NULL),r.created_at,r.homepage,r.stack,
		(SELECT count(*)::int FROM repository_sparks s WHERE s.repository_id=r.id),
		COALESCE((SELECT d.tag FROM drops d WHERE d.repository_id=r.id AND NOT d.draft ORDER BY d.created_at DESC LIMIT 1),''),
		COALESCE((SELECT sum(da.download_count)::int FROM drops d JOIN drop_assets da ON da.drop_id=d.id WHERE d.repository_id=r.id AND NOT d.draft),0)
		`+from, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	repos := []PublicRepository{}
	for rows.Next() {
		var repo PublicRepository
		if err = rows.Scan(&repo.ID, &repo.Name, &repo.Description, &repo.Archived, &repo.CreatedAt, &repo.Homepage, &repo.Stack, &repo.Sparks, &repo.LatestDrop, &repo.Downloads); err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func validProfileWebsite(value string) bool {
	if value == "" {
		return true
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func (a *App) updateProfile(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		DisplayName         string         `json:"display_name"`
		Bio                 string         `json:"bio"`
		Website             string         `json:"website"`
		Location            string         `json:"location"`
		Skills              *string        `json:"skills"`
		Availability        *string        `json:"availability"`
		OpenToCollaborators *bool          `json:"open_to_collaborators"`
		Links               *[]ProfileLink `json:"links"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Bio = strings.TrimSpace(in.Bio)
	in.Website = strings.TrimSpace(in.Website)
	in.Location = strings.TrimSpace(in.Location)
	if in.Skills != nil {
		trimmed := strings.TrimSpace(*in.Skills)
		in.Skills = &trimmed
	}
	if in.Availability != nil {
		trimmed := strings.TrimSpace(*in.Availability)
		in.Availability = &trimmed
	}
	if in.DisplayName == "" || len(in.DisplayName) > 80 || len(in.Bio) > 500 || len(in.Website) > 300 || len(in.Location) > 100 || !validProfileWebsite(in.Website) || (in.Skills != nil && len(*in.Skills) > 200) || (in.Availability != nil && len(*in.Availability) > 200) {
		fail(w, 422, "validation_failed", "Use a display name up to 80 characters, bio up to 500, location up to 100, skills and availability up to 200, and an optional HTTPS website.")
		return
	}
	if in.Links != nil {
		if len(*in.Links) > 5 {
			fail(w, 422, "validation_failed", "Add up to five links.")
			return
		}
		for index, link := range *in.Links {
			link.Label = strings.TrimSpace(link.Label)
			link.URL = strings.TrimSpace(link.URL)
			if link.Label == "" || len(link.Label) > 40 || link.URL == "" || len(link.URL) > 300 || !validProfileWebsite(link.URL) {
				fail(w, 422, "validation_failed", "Each link needs a label up to 40 characters and an HTTPS address.")
				return
			}
			(*in.Links)[index] = link
		}
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `UPDATE users SET display_name=$1,bio=$2,website=$3,location=$4,skills=COALESCE($5,skills),availability=COALESCE($6,availability),open_to_collaborators=COALESCE($7,open_to_collaborators) WHERE id=$8`, in.DisplayName, in.Bio, in.Website, in.Location, in.Skills, in.Availability, in.OpenToCollaborators, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if in.Links != nil {
		if _, err = tx.Exec(r.Context(), `DELETE FROM user_links WHERE user_id=$1`, u.ID); err != nil {
			serverError(w, err)
			return
		}
		for index, link := range *in.Links {
			if _, err = tx.Exec(r.Context(), `INSERT INTO user_links(user_id,position,label,url) VALUES($1,$2,$3,$4)`, u.ID, index+1, link.Label, link.URL); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'profile.updated',$2)`, u.ID, u.Username); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.profileForUser(w, r, u.Username)
}

func (a *App) profileForUser(w http.ResponseWriter, r *http.Request, username string) {
	r.SetPathValue("username", username)
	a.profile(w, r)
}

func (a *App) updateShowcase(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		RepositoryIDs []string `json:"repository_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.RepositoryIDs) > 6 {
		fail(w, 422, "validation_failed", "Showcase up to six public repositories.")
		return
	}
	seen := map[string]bool{}
	for _, id := range in.RepositoryIDs {
		if !milestoneIDPattern.MatchString(id) || seen[id] {
			fail(w, 422, "validation_failed", "Choose distinct public repositories you own.")
			return
		}
		seen[id] = true
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM users WHERE id=$1 FOR UPDATE`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	for _, id := range in.RepositoryIDs {
		var exists bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM repositories WHERE id=$1 AND owner_id=$2 AND visibility='public' AND deleted_at IS NULL)`, id, u.ID).Scan(&exists); err != nil {
			serverError(w, err)
			return
		}
		if !exists {
			fail(w, 422, "validation_failed", "Choose distinct public repositories you own.")
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM profile_repositories WHERE user_id=$1`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	for index, id := range in.RepositoryIDs {
		if _, err = tx.Exec(r.Context(), `INSERT INTO profile_repositories(user_id,repository_id,position) VALUES($1,$2,$3)`, u.ID, id, index+1); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'profile.showcase_updated',$2)`, u.ID, fmt.Sprintf("%s [%d repositories]", u.Username, len(in.RepositoryIDs))); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.profileForUser(w, r, u.Username)
}
