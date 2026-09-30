package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

type IssueLabelBrief struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type SubIssueProgress struct {
	Total  int `json:"total"`
	Closed int `json:"closed"`
}

type Issue struct {
	ID          string            `json:"id"`
	Number      int               `json:"number"`
	Title       string            `json:"title"`
	Body        string            `json:"body"`
	State       string            `json:"state"`
	StateReason string            `json:"state_reason"`
	Author      string            `json:"author"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Pinned      bool              `json:"pinned"`
	Priority    string            `json:"priority"`
	Iteration   string            `json:"iteration"`
	Estimate    *int              `json:"estimate"`
	DueDate     *time.Time        `json:"due_date"`
	DuplicateOf *int              `json:"duplicate_of"`
	Parent      *int              `json:"parent"`
	Milestone   *string           `json:"milestone"`
	Labels      []IssueLabelBrief `json:"labels"`
	Assignees   []string          `json:"assignees"`
	Comments    int               `json:"comments"`
	SubIssues   SubIssueProgress  `json:"sub_issues"`
}

type IssueComment struct {
	ID        string     `json:"id"`
	Body      string     `json:"body"`
	Author    string     `json:"author"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
	Edited    bool       `json:"edited"`
	Editable  bool       `json:"editable"`
}

const issueColumns = `i.id,i.number,i.title,i.body,i.state,i.state_reason,u.username,i.created_at,i.updated_at,i.pinned,i.priority,i.iteration,i.estimate,i.due_date,
	(SELECT d.number FROM issues d WHERE d.id=i.duplicate_of),
	(SELECT p.number FROM issues p WHERE p.id=i.parent_id),
	(SELECT m.title FROM milestones m WHERE m.id=i.milestone_id),
	COALESCE((SELECT json_agg(json_build_object('name',l.name,'color',l.color) ORDER BY lower(l.name)) FROM issue_labels il JOIN labels l ON l.id=il.label_id WHERE il.issue_id=i.id),'[]'),
	COALESCE((SELECT json_agg(au.username ORDER BY au.username) FROM issue_assignees ia JOIN users au ON au.id=ia.user_id WHERE ia.issue_id=i.id),'[]'),
	(SELECT count(*)::int FROM issue_comments c WHERE c.issue_id=i.id),
	(SELECT count(*)::int FROM issues s WHERE s.parent_id=i.id),
	(SELECT count(*)::int FROM issues s WHERE s.parent_id=i.id AND s.state='closed')`

func scanIssue(row scanner) (Issue, error) {
	var i Issue
	err := row.Scan(&i.ID, &i.Number, &i.Title, &i.Body, &i.State, &i.StateReason, &i.Author, &i.CreatedAt, &i.UpdatedAt, &i.Pinned, &i.Priority, &i.Iteration, &i.Estimate, &i.DueDate,
		&i.DuplicateOf, &i.Parent, &i.Milestone, &i.Labels, &i.Assignees, &i.Comments, &i.SubIssues.Total, &i.SubIssues.Closed)
	return i, err
}

// pageParams reads page/per_page query parameters with a bounded page size.
func pageParams(w http.ResponseWriter, r *http.Request, defaultSize, maxSize int) (int, int, bool) {
	page, perPage := 1, defaultSize
	if raw := r.URL.Query().Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 10000 {
			fail(w, 422, "validation_failed", "Page must be a positive number.")
			return 0, 0, false
		}
		page = n
	}
	if raw := r.URL.Query().Get("per_page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxSize {
			fail(w, 422, "validation_failed", fmt.Sprintf("Choose between 1 and %d results per page.", maxSize))
			return 0, 0, false
		}
		perPage = n
	}
	return page, perPage, true
}

func (a *App) issues(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	query := r.URL.Query()
	state := query.Get("state")
	q := strings.TrimSpace(query.Get("q"))
	assignee := strings.ToLower(strings.TrimSpace(query.Get("assignee")))
	author := strings.ToLower(strings.TrimSpace(query.Get("author")))
	milestone := strings.TrimSpace(query.Get("milestone"))
	sort := query.Get("sort")
	labels := []string{}
	seen := map[string]bool{}
	for _, raw := range query["label"] {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name != "" && !seen[name] {
			seen[name] = true
			labels = append(labels, name)
		}
	}
	if (state != "" && state != "open" && state != "closed" && state != "all") || len(q) > 100 || len(labels) > 5 || len(milestone) > 200 ||
		(assignee != "" && assignee != "none" && !slug.MatchString(assignee)) || (author != "" && !slug.MatchString(author)) ||
		(sort != "" && sort != "newest" && sort != "oldest" && sort != "updated") {
		fail(w, 422, "validation_failed", "Filter by open, closed, or all issues, up to five labels, an assignee, an author, a milestone, and a query up to 100 characters.")
		return
	}
	for _, name := range labels {
		if len(name) > 50 {
			fail(w, 422, "validation_failed", "Label names are up to 50 characters.")
			return
		}
	}
	page, perPage, ok := pageParams(w, r, 30, 100)
	if !ok {
		return
	}
	conditions := []string{"i.repository_id=$1"}
	args := []any{repo.ID}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, strings.ReplaceAll(condition, "$?", "$"+strconv.Itoa(len(args))))
	}
	if q != "" {
		add(`strpos(lower(i.title||' '||i.body), lower($?))>0`, q)
	}
	if len(labels) > 0 {
		add(`(SELECT count(DISTINCT lower(l.name)) FROM issue_labels il JOIN labels l ON l.id=il.label_id WHERE il.issue_id=i.id AND lower(l.name)=ANY($?::text[]))=`+strconv.Itoa(len(labels)), labels)
	}
	if assignee == "none" {
		conditions = append(conditions, `NOT EXISTS(SELECT 1 FROM issue_assignees ia WHERE ia.issue_id=i.id)`)
	} else if assignee != "" {
		add(`EXISTS(SELECT 1 FROM issue_assignees ia JOIN users au ON au.id=ia.user_id WHERE ia.issue_id=i.id AND au.username=$?)`, assignee)
	}
	if author != "" {
		add(`u.username=$?`, author)
	}
	if milestone == "none" {
		conditions = append(conditions, `i.milestone_id IS NULL`)
	} else if milestone != "" {
		add(`EXISTS(SELECT 1 FROM milestones m WHERE m.id=i.milestone_id AND lower(m.title)=lower($?))`, milestone)
	}
	where := strings.Join(conditions, " AND ")
	var open, closed int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FILTER (WHERE i.state='open')::int, count(*) FILTER (WHERE i.state='closed')::int FROM issues i JOIN users u ON u.id=i.author_id WHERE `+where, args...).Scan(&open, &closed); err != nil {
		serverError(w, err)
		return
	}
	total := open + closed
	if state == "open" || state == "closed" {
		add(`i.state=$?`, state)
		total = open
		if state == "closed" {
			total = closed
		}
	}
	order := "i.pinned DESC, i.number DESC"
	switch sort {
	case "oldest":
		order = "i.pinned DESC, i.number ASC"
	case "updated":
		order = "i.pinned DESC, i.updated_at DESC, i.number DESC"
	}
	args = append(args, perPage, (page-1)*perPage)
	rows, err := a.db.Query(r.Context(), `SELECT `+issueColumns+` FROM issues i JOIN users u ON u.id=i.author_id WHERE `+strings.Join(conditions, " AND ")+
		` ORDER BY `+order+fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Issue{}
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	w.Header().Set("X-Open-Count", strconv.Itoa(open))
	w.Header().Set("X-Closed-Count", strconv.Itoa(closed))
	w.Header().Set("X-Page", strconv.Itoa(page))
	w.Header().Set("X-Per-Page", strconv.Itoa(perPage))
	respond(w, 200, items)
}

func (a *App) issue(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	item, err := scanIssue(a.db.QueryRow(r.Context(), `SELECT `+issueColumns+` FROM issues i JOIN users u ON u.id=i.author_id WHERE i.repository_id=$1 AND i.number=$2`, repo.ID, number))
	if errors.Is(err, pgx.ErrNoRows) {
		a.issueMoved(w, r, repo, number)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, item)
}

// issueMoved explains where a transferred issue went when the viewer can see
// the destination repository, and otherwise reports a plain 404.
func (a *App) issueMoved(w http.ResponseWriter, r *http.Request, repo *Repository, number int) {
	var owner, name, visibility, targetRepoID string
	var newNumber int
	err := a.db.QueryRow(r.Context(), `SELECT o.username,tr.name,tr.visibility,tr.id,i.number FROM issue_transfers t JOIN issues i ON i.id=t.issue_id
		JOIN repositories tr ON tr.id=i.repository_id JOIN users o ON o.id=tr.owner_id
		WHERE t.source_repository_id=$1 AND t.source_number=$2 AND tr.deleted_at IS NULL`, repo.ID, number).Scan(&owner, &name, &visibility, &targetRepoID, &newNumber)
	if err == nil && (visibility == "public" || a.canReadRepository(r, targetRepoID)) {
		fail(w, 404, "issue_transferred", fmt.Sprintf("This issue moved to %s/%s#%d.", owner, name, newNumber))
		return
	}
	fail(w, 404, "not_found", "Issue not found.")
}

func (a *App) canReadRepository(r *http.Request, repositoryID string) bool {
	u := a.user(r)
	if u == nil {
		return false
	}
	target, err := scanRepo(a.db.QueryRow(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.id=$1 AND r.deleted_at IS NULL`, repositoryID))
	if err != nil || a.decorate(r.Context(), &target, u) != nil {
		return false
	}
	return target.Visibility == "public" || target.Role != ""
}

type issueFormField struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Options  []string `json:"options"`
}

// composeIssueForm validates submitted form values against a template's
// fields and renders them as Markdown sections.
func composeIssueForm(fields []issueFormField, values map[string]string, extra string) (string, error) {
	known := map[string]bool{}
	var body strings.Builder
	for _, field := range fields {
		known[field.ID] = true
		value := strings.TrimSpace(values[field.ID])
		switch field.Type {
		case "checkbox":
			if value != "" && value != "true" && value != "false" {
				return "", fmt.Errorf("%s must be checked or unchecked.", field.Label)
			}
			if field.Required && value != "true" {
				return "", fmt.Errorf("%s must be checked.", field.Label)
			}
			if value == "true" {
				fmt.Fprintf(&body, "- [x] %s\n\n", field.Label)
			}
			continue
		case "dropdown":
			if value != "" && !containsString(field.Options, value) {
				return "", fmt.Errorf("Choose one of the listed options for %s.", field.Label)
			}
		case "text":
			if len(value) > 300 {
				return "", fmt.Errorf("%s can be up to 300 characters.", field.Label)
			}
		default:
			if len(value) > 8000 {
				return "", fmt.Errorf("%s can be up to 8,000 characters.", field.Label)
			}
		}
		if field.Required && value == "" {
			return "", fmt.Errorf("%s is required.", field.Label)
		}
		if value != "" {
			fmt.Fprintf(&body, "### %s\n\n%s\n\n", field.Label, value)
		}
	}
	for id := range values {
		if !known[id] {
			return "", errors.New("The form contains an unknown field.")
		}
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		fmt.Fprintf(&body, "### Additional context\n\n%s\n", extra)
	}
	return strings.TrimSpace(body.String()), nil
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

const visitorIssuesPerHour = 20

func (a *App) createIssue(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil || !activeRepository(w, repo) {
		return
	}
	var in struct {
		Title    string            `json:"title"`
		Body     string            `json:"body"`
		Template string            `json:"template"`
		Fields   map[string]string `json:"fields"`
		Parent   *int              `json:"parent"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Parent != nil && !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required to create sub-issues.")
		return
	}
	if repo.Role == "" {
		// Visitors without a role can report issues, within a modest hourly
		// budget that keeps a single account from flooding repositories.
		var recent int
		if err := a.db.QueryRow(r.Context(), `SELECT count(*)::int FROM issues WHERE author_id=$1 AND created_at>now()-interval '1 hour'`, u.ID).Scan(&recent); err != nil {
			serverError(w, err)
			return
		}
		if recent >= visitorIssuesPerHour {
			w.Header().Set("Retry-After", "3600")
			fail(w, 429, "rate_limited", "You have opened many issues recently. Try again later.")
			return
		}
	}
	if in.Template != "" {
		raw, err := a.issueTemplateFields(r.Context(), repo.ID, in.Template)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 422, "validation_failed", "That issue template no longer exists.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if len(raw) > 0 || in.Fields != nil {
			body, formErr := composeIssueForm(raw, in.Fields, in.Body)
			if formErr != nil {
				fail(w, 422, "validation_failed", formErr.Error())
				return
			}
			in.Body = body
		}
	} else if in.Fields != nil {
		fail(w, 422, "validation_failed", "Form fields require an issue template.")
		return
	}
	if !validContent(in.Title, in.Body) {
		fail(w, 422, "validation_failed", "A title up to 200 characters and body up to 20,000 characters are required.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM repositories WHERE id=$1 FOR UPDATE`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	var parentID any
	if in.Parent != nil {
		id, depth, parentErr := issueAncestry(r.Context(), tx, repo.ID, *in.Parent, "")
		if parentErr != nil {
			fail(w, 422, "validation_failed", parentErr.Error())
			return
		}
		if depth >= maxIssueDepth {
			fail(w, 422, "validation_failed", fmt.Sprintf("Sub-issues can be nested up to %d levels.", maxIssueDepth))
			return
		}
		parentID = id
	}
	i := Issue{ID: auth.ID(), Title: strings.TrimSpace(in.Title), Body: in.Body, State: "open", Author: u.Username, Labels: []IssueLabelBrief{}, Assignees: []string{}}
	err = tx.QueryRow(r.Context(), `INSERT INTO issues(id,repository_id,number,author_id,title,body,parent_id) SELECT $1,$2,COALESCE(MAX(number),0)+1,$3,$4,$5,$6 FROM issues WHERE repository_id=$2 RETURNING number,created_at,updated_at,priority,iteration`, i.ID, repo.ID, u.ID, i.Title, i.Body, parentID).Scan(&i.Number, &i.CreatedAt, &i.UpdatedAt, &i.Priority, &i.Iteration)
	if err != nil {
		serverError(w, err)
		return
	}
	if in.Parent != nil {
		i.Parent = in.Parent
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_subscriptions(issue_id,user_id) VALUES($1,$2)`, i.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = a.recordReferences(r.Context(), tx, repo, u.ID, "issue", i.ID, i.ID, "", i.Body); err != nil {
		serverError(w, err)
		return
	}
	if err = a.AnnounceIssue(r.Context(), tx, repo, u, i.ID, i.Title+"\n"+i.Body); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.opened',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, i.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = a.fireWebhook(r.Context(), tx, repo.ID, repo.DistrictID, "issue.opened", webhookIssuePayload(repo, u.Username, i)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, i)
}

// webhookIssuePayload is the shared envelope for every issue-related
// webhook event: which repository, who acted, and the issue itself.
func webhookIssuePayload(repo *Repository, actor string, i Issue) map[string]any {
	return map[string]any{
		"repository": map[string]string{"owner": repo.Owner, "name": repo.Name},
		"actor":      actor,
		"issue": map[string]any{
			"number": i.Number,
			"title":  i.Title,
			"state":  i.State,
			"url":    fmt.Sprintf("/repos/%s/%s/issues/%d", repo.Owner, repo.Name, i.Number),
		},
	}
}

// updateIssue changes an issue's state, title, or body. Triage users can
// change any issue; authors can edit and close or reopen their own.
func (a *App) updateIssue(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil || !activeRepository(w, repo) {
		return
	}
	var in struct {
		State       string  `json:"state"`
		StateReason string  `json:"state_reason"`
		Title       *string `json:"title"`
		Body        *string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.State != "" && in.State != "open" && in.State != "closed" {
		fail(w, 422, "validation_failed", "State must be open or closed.")
		return
	}
	if in.State == "" && in.Title == nil && in.Body == nil {
		fail(w, 422, "validation_failed", "Provide a state, title, or body to update.")
		return
	}
	if in.StateReason != "" && (in.State != "closed" || (in.StateReason != "completed" && in.StateReason != "not_planned")) {
		fail(w, 422, "validation_failed", "Close issues as completed or not planned.")
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM repositories WHERE id=$1 FOR UPDATE`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	var issueID, previousState, authorID, title, body string
	if err = tx.QueryRow(r.Context(), `SELECT id,state,author_id,title,body FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID, &previousState, &authorID, &title, &body); errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	if !repo.CanTriage && authorID != u.ID {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if in.Title != nil || in.Body != nil {
		if in.Title != nil {
			title = strings.TrimSpace(*in.Title)
		}
		if in.Body != nil {
			body = *in.Body
		}
		if !validContent(title, body) {
			fail(w, 422, "validation_failed", "A title up to 200 characters and body up to 20,000 characters are required.")
			return
		}
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET title=$1,body=$2,updated_at=now() WHERE id=$3`, title, body, issueID); err != nil {
			serverError(w, err)
			return
		}
		if in.Body != nil {
			if err = a.recordReferences(r.Context(), tx, repo, u.ID, "issue", issueID, issueID, "", body); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if in.State != "" {
		reason := ""
		if in.State == "closed" {
			reason = in.StateReason
			if reason == "" {
				reason = "completed"
			}
		}
		if in.State == "closed" && reason == "completed" && previousState != "closed" {
			blocked, blockerErr := hasOpenBlockers(r.Context(), tx, issueID)
			if blockerErr != nil {
				serverError(w, blockerErr)
				return
			}
			if blocked {
				fail(w, 409, "open_blockers", "Close this issue's blockers before closing the issue.")
				return
			}
		}
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET state=$1,state_reason=$2,updated_at=now() WHERE id=$3`, in.State, reason, issueID); err != nil {
			serverError(w, err)
			return
		}
		if previousState != in.State {
			kind := "issue_closed"
			if in.State == "open" {
				kind = "issue_reopened"
			}
			if err = notifyIssue(r.Context(), tx, issueID, u.ID, kind); err != nil {
				serverError(w, err)
				return
			}
		}
		if err = syncIssueBoard(r.Context(), tx, repo.ID, issueID, in.State == "closed"); err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "issue."+in.State, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)); err != nil {
			serverError(w, err)
			return
		}
		if previousState != in.State {
			webhookKind := "issue.closed"
			if in.State == "open" {
				webhookKind = "issue.reopened"
			}
			if err = a.fireWebhook(r.Context(), tx, repo.ID, repo.DistrictID, webhookKind, webhookIssuePayload(repo, u.Username, Issue{Number: number, Title: title, State: in.State})); err != nil {
				serverError(w, err)
				return
			}
		}
	} else if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.edited',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	state := in.State
	if state == "" {
		state = previousState
	}
	respond(w, 200, map[string]string{"state": state, "title": title, "body": body})
}

func (a *App) issueComments(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issues WHERE repository_id=$1 AND number=$2)`, repo.ID, number).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	viewer := ""
	if u := a.user(r); u != nil {
		viewer = u.ID
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.id,c.body,u.username,c.created_at,c.updated_at,EXISTS(SELECT 1 FROM issue_comment_revisions rv WHERE rv.comment_id=c.id),c.author_id::text=$3
		FROM issue_comments c JOIN issues i ON i.id=c.issue_id JOIN users u ON u.id=c.author_id WHERE i.repository_id=$1 AND i.number=$2 ORDER BY c.created_at,c.id LIMIT 200`, repo.ID, number, viewer)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	comments := []IssueComment{}
	for rows.Next() {
		var comment IssueComment
		if err = rows.Scan(&comment.ID, &comment.Body, &comment.Author, &comment.CreatedAt, &comment.UpdatedAt, &comment.Edited, &comment.Editable); err != nil {
			serverError(w, err)
			return
		}
		comment.Editable = comment.Editable && !repo.Archived
		comments = append(comments, comment)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, comments)
}

func (a *App) createIssueComment(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil || !activeRepository(w, repo) {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || len(in.Body) > 10000 {
		fail(w, 422, "validation_failed", "A comment between 1 and 10,000 characters is required.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	comment := IssueComment{ID: auth.ID(), Body: in.Body, Author: u.Username, Editable: true}
	var issueID string
	err = tx.QueryRow(r.Context(), `INSERT INTO issue_comments(id,issue_id,author_id,body) SELECT $1,i.id,$2,$3 FROM issues i WHERE i.repository_id=$4 AND i.number=$5 RETURNING issue_id,created_at`, comment.ID, u.ID, comment.Body, repo.ID, number).Scan(&issueID, &comment.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE issues SET updated_at=now() WHERE id=$1`, issueID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_subscriptions(issue_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, issueID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = notifyIssueEvent(r.Context(), tx, issueID, u.ID, "issue_comment", "", comment.Body); err != nil {
		serverError(w, err)
		return
	}
	if err = a.noteMentions(r.Context(), tx, repo, u, comment.Body, issueID, "", comment.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = a.recordReferences(r.Context(), tx, repo, u.ID, "issue_comment", comment.ID, issueID, "", comment.Body); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.commented',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)); err != nil {
		serverError(w, err)
		return
	}
	if err = a.fireWebhook(r.Context(), tx, repo.ID, repo.DistrictID, "issue.commented", map[string]any{
		"repository": map[string]string{"owner": repo.Owner, "name": repo.Name},
		"actor":      u.Username,
		"issue":      map[string]any{"number": number},
		"comment":    map[string]string{"body": comment.Body},
	}); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, comment)
}
