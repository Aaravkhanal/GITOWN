package app

import (
	"fmt"
	"net/http"
	"net/url"
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
}

type Contributions struct {
	MergedUnites       int `json:"merged_unites"`
	Approvals          int `json:"approvals"`
	ClosedIssues       int `json:"closed_issues"`
	PublicRepositories int `json:"public_repositories"`
}

type Profile struct {
	Username            string             `json:"username"`
	DisplayName         string             `json:"display_name"`
	Bio                 string             `json:"bio"`
	Website             string             `json:"website"`
	Location            string             `json:"location"`
	Skills              string             `json:"skills"`
	Availability        string             `json:"availability"`
	OpenToCollaborators bool               `json:"open_to_collaborators"`
	CreatedAt           time.Time          `json:"created_at"`
	Followers           int                `json:"followers"`
	Following           int                `json:"following"`
	Followed            bool               `json:"followed"`
	Contributions       Contributions      `json:"contributions"`
	Badges              []string           `json:"badges"`
	Showcase            []PublicRepository `json:"showcase"`
	Repositories        []PublicRepository `json:"repositories"`
}

func (a *App) profile(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if !slug.MatchString(username) {
		fail(w, 404, "not_found", "Profile not found.")
		return
	}
	var userID string
	p := Profile{Showcase: []PublicRepository{}, Repositories: []PublicRepository{}, Badges: []string{}}
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
	if err = a.db.QueryRow(r.Context(), `SELECT
		(SELECT count(*)::int FROM pull_requests p JOIN repositories r ON r.id=p.repository_id WHERE p.author_id=$1 AND p.state='merged' AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM pull_reviews rv JOIN pull_requests p ON p.id=rv.pull_request_id JOIN repositories r ON r.id=p.repository_id WHERE rv.reviewer_id=$1 AND rv.state='approved' AND rv.dismissed_at IS NULL AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM issues i JOIN repositories r ON r.id=i.repository_id WHERE i.author_id=$1 AND i.state='closed' AND r.visibility='public' AND r.deleted_at IS NULL),
		(SELECT count(*)::int FROM repositories r WHERE r.owner_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL)`, userID).
		Scan(&p.Contributions.MergedUnites, &p.Contributions.Approvals, &p.Contributions.ClosedIssues, &p.Contributions.PublicRepositories); err != nil {
		serverError(w, err)
		return
	}
	if p.Contributions.MergedUnites > 0 {
		p.Badges = append(p.Badges, "Merged a Unite request")
	}
	if p.Contributions.Approvals > 0 {
		p.Badges = append(p.Badges, "Reviewed a Unite request")
	}
	if p.Contributions.ClosedIssues > 0 {
		p.Badges = append(p.Badges, "Closed an issue")
	}
	if p.Contributions.PublicRepositories > 0 {
		p.Badges = append(p.Badges, "Maintains a public repository")
	}
	showcase, err := a.publicProfileRepositories(r, `SELECT r.id,r.name,r.description,(r.archived_at IS NOT NULL),r.created_at
		FROM profile_repositories pr JOIN repositories r ON r.id=pr.repository_id
		WHERE pr.user_id=$1 AND r.owner_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL ORDER BY pr.position`, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	p.Showcase = showcase
	repos, err := a.publicProfileRepositories(r, `SELECT r.id,r.name,r.description,(r.archived_at IS NOT NULL),r.created_at
		FROM repositories r WHERE r.owner_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL ORDER BY r.created_at DESC LIMIT 100`, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	p.Repositories = repos
	respond(w, 200, p)
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
		_, err = tx.Exec(r.Context(), `INSERT INTO user_follows(follower_id,followed_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, u.ID, followedID)
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

func (a *App) publicProfileRepositories(r *http.Request, query, userID string) ([]PublicRepository, error) {
	rows, err := a.db.Query(r.Context(), query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	repos := []PublicRepository{}
	for rows.Next() {
		var repo PublicRepository
		if err = rows.Scan(&repo.ID, &repo.Name, &repo.Description, &repo.Archived, &repo.CreatedAt); err != nil {
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
		DisplayName         string  `json:"display_name"`
		Bio                 string  `json:"bio"`
		Website             string  `json:"website"`
		Location            string  `json:"location"`
		Skills              *string `json:"skills"`
		Availability        *string `json:"availability"`
		OpenToCollaborators *bool   `json:"open_to_collaborators"`
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
