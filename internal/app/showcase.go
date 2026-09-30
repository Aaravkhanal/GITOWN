package app

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Screenshot struct {
	URL     string `json:"url"`
	Caption string `json:"caption"`
}

type ShowcaseContributor struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Merged      int    `json:"merged"`
	Role        string `json:"role"`
}

type ShowcaseDrop struct {
	Tag       string    `json:"tag"`
	Title     string    `json:"title"`
	Downloads int       `json:"downloads"`
	CreatedAt time.Time `json:"created_at"`
}

type ShowcaseMilestone struct {
	Title        string  `json:"title"`
	DueDate      *string `json:"due_date"`
	OpenIssues   int     `json:"open_issues"`
	ClosedIssues int     `json:"closed_issues"`
}

type ShowcaseTask struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Labels []string `json:"labels"`
}

type Showcase struct {
	Repository   Repository            `json:"repository"`
	Readme       string                `json:"readme"`
	Setup        string                `json:"setup"`
	Screenshots  []Screenshot          `json:"screenshots"`
	Stack        []string              `json:"stack"`
	Sparks       int                   `json:"sparks"`
	Contributors []ShowcaseContributor `json:"contributors"`
	LatestDrop   *ShowcaseDrop         `json:"latest_drop"`
	Roadmap      []ShowcaseMilestone   `json:"roadmap"`
	Tasks        []ShowcaseTask        `json:"tasks"`
}

// collaborationLabels are the label names that mark work newcomers can pick up.
var collaborationLabels = []string{"good first task", "good first issue", "help wanted", "beginner friendly"}

var setupHeading = regexp.MustCompile(`(?i)^(#{1,6})\s*(setup|getting started|installation|install|quick ?start|development|usage)\b`)

// readmeSections returns a short introduction and the setup section of a
// Markdown README.
func readmeSections(readme string) (string, string) {
	lines := strings.Split(strings.ReplaceAll(readme, "\r\n", "\n"), "\n")
	var intro, setup []string
	setupLevel := 0
	for _, line := range lines {
		if match := setupHeading.FindStringSubmatch(line); match != nil && setupLevel == 0 && setup == nil {
			setupLevel = len(match[1])
			setup = []string{}
			continue
		}
		if setupLevel > 0 {
			if trimmed := strings.TrimLeft(line, "#"); len(line)-len(trimmed) > 0 && len(line)-len(trimmed) <= setupLevel && strings.HasPrefix(trimmed, " ") {
				setupLevel = -1
				continue
			}
			setup = append(setup, line)
			continue
		}
		if setupLevel == 0 {
			intro = append(intro, line)
		}
	}
	clip := func(lines []string, limit int) string {
		text := strings.TrimSpace(strings.Join(lines, "\n"))
		if len(text) > limit {
			cut := limit
			for cut > 0 && !isRuneStart(text[cut]) {
				cut--
			}
			text = strings.TrimSpace(text[:cut]) + "…"
		}
		return text
	}
	return clip(intro, 1500), clip(setup, 2000)
}

func (a *App) repositoryScreenshots(ctx context.Context, db queryer, repoID string) ([]Screenshot, error) {
	rows, err := db.Query(ctx, `SELECT url,caption FROM repository_screenshots WHERE repository_id=$1 ORDER BY position`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shots := []Screenshot{}
	for rows.Next() {
		var shot Screenshot
		if err = rows.Scan(&shot.URL, &shot.Caption); err != nil {
			return nil, err
		}
		shots = append(shots, shot)
	}
	return shots, rows.Err()
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// repositoryShowcase assembles a public project page from what the
// repository already knows: README, screenshots, stack, people, Drops,
// milestones, and open collaboration tasks.
func (a *App) repositoryShowcase(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	ctx := r.Context()
	show := Showcase{Repository: *repo, Stack: []string{}, Contributors: []ShowcaseContributor{}, Roadmap: []ShowcaseMilestone{}, Tasks: []ShowcaseTask{}}
	for _, name := range []string{"README.md", "readme.md", "Readme.md", "README"} {
		blob, err := a.git.Blob(ctx, repo.ID, repo.DefaultBranch, name)
		if err == nil {
			show.Readme, show.Setup = readmeSections(string(blob))
			break
		}
	}
	for _, part := range strings.Split(repo.Stack, ",") {
		if part = strings.TrimSpace(part); part != "" {
			show.Stack = append(show.Stack, part)
		}
	}
	var err error
	if show.Screenshots, err = a.repositoryScreenshots(ctx, a.db, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = a.db.QueryRow(ctx, `SELECT count(*)::int FROM repository_sparks WHERE repository_id=$1`, repo.ID).Scan(&show.Sparks); err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(ctx, `SELECT u.username,u.display_name,count(p.id)::int,CASE WHEN u.id=$2 THEN 'owner' ELSE 'contributor' END
		FROM users u LEFT JOIN pull_requests p ON p.author_id=u.id AND p.repository_id=$1 AND p.state='merged'
		WHERE u.id=$2 OR EXISTS (SELECT 1 FROM pull_requests x WHERE x.repository_id=$1 AND x.author_id=u.id AND x.state='merged')
		GROUP BY u.id,u.username,u.display_name ORDER BY (u.id=$2) DESC,count(p.id) DESC,u.username LIMIT 24`, repo.ID, repo.OwnerID)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var person ShowcaseContributor
		if err = rows.Scan(&person.Username, &person.DisplayName, &person.Merged, &person.Role); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		show.Contributors = append(show.Contributors, person)
	}
	rows.Close()
	var drop ShowcaseDrop
	err = a.db.QueryRow(ctx, `SELECT d.tag,d.title,d.created_at,COALESCE((SELECT sum(download_count)::int FROM drop_assets WHERE drop_id=d.id),0)
		FROM drops d WHERE d.repository_id=$1 AND NOT d.draft ORDER BY d.created_at DESC LIMIT 1`, repo.ID).Scan(&drop.Tag, &drop.Title, &drop.CreatedAt, &drop.Downloads)
	if err == nil {
		show.LatestDrop = &drop
	} else if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	rows, err = a.db.Query(ctx, `SELECT m.title,to_char(m.due_date,'YYYY-MM-DD'),
		(SELECT count(*)::int FROM issues i WHERE i.milestone_id=m.id AND i.state='open'),
		(SELECT count(*)::int FROM issues i WHERE i.milestone_id=m.id AND i.state='closed')
		FROM milestones m WHERE m.repository_id=$1 AND m.state='open' ORDER BY m.due_date NULLS LAST,m.created_at LIMIT 10`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var item ShowcaseMilestone
		if err = rows.Scan(&item.Title, &item.DueDate, &item.OpenIssues, &item.ClosedIssues); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		show.Roadmap = append(show.Roadmap, item)
	}
	rows.Close()
	rows, err = a.db.Query(ctx, `SELECT i.number,i.title,array_agg(l.name ORDER BY l.name) FROM issues i
		JOIN issue_labels il ON il.issue_id=i.id JOIN labels l ON l.id=il.label_id
		WHERE i.repository_id=$1 AND i.state='open' AND lower(l.name)=ANY($2)
		GROUP BY i.id,i.number,i.title ORDER BY i.number LIMIT 10`, repo.ID, collaborationLabels)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var task ShowcaseTask
		if err = rows.Scan(&task.Number, &task.Title, &task.Labels); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		show.Tasks = append(show.Tasks, task)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, show)
}
