package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

type BoardColumn struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type BoardField struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Options  []string `json:"options"`
	Position int      `json:"position"`
}

type BoardSettings struct {
	Columns    []BoardColumn `json:"columns"`
	Automation bool          `json:"automation"`
	Fields     []BoardField  `json:"fields"`
}

type BoardItem struct {
	Kind         string            `json:"kind"`
	ItemID       string            `json:"item_id"`
	IssueID      string            `json:"issue_id,omitempty"`
	PullID       string            `json:"pull_id,omitempty"`
	Owner        string            `json:"owner"`
	Repository   string            `json:"repository"`
	Number       int               `json:"number"`
	Title        string            `json:"title"`
	State        string            `json:"state"`
	Status       string            `json:"status"`
	Author       string            `json:"author"`
	Priority     string            `json:"priority"`
	Iteration    string            `json:"iteration"`
	Pinned       bool              `json:"pinned"`
	Estimate     *int              `json:"estimate"`
	DueDate      *time.Time        `json:"due_date"`
	Milestone    *string           `json:"milestone"`
	MilestoneDue *time.Time        `json:"milestone_due"`
	Fields       map[string]string `json:"fields"`
	repositoryID string
}

var defaultBoardColumns = []BoardColumn{{"todo", "To do"}, {"progress", "In progress"}, {"done", "Done"}}
var boardColumnKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,29}$`)

const maxBoardColumns = 10
const maxBoardFields = 20

func loadBoardColumns(ctx context.Context, db queryRower, repositoryID string) ([]BoardColumn, bool, error) {
	var columns []BoardColumn
	automation := true
	err := db.QueryRow(ctx, `SELECT columns,automation FROM boards WHERE repository_id=$1`, repositoryID).Scan(&columns, &automation)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && len(columns) < 2) {
		return defaultBoardColumns, true, nil
	}
	return columns, automation, err
}

func columnKnown(columns []BoardColumn, key string) bool {
	for _, column := range columns {
		if column.Key == key {
			return true
		}
	}
	return false
}

// boardStatus maps a stored status onto the board's current columns: closed
// issues are always done and unknown statuses fall back to the first column.
func boardStatus(columns []BoardColumn, kind, state, stored string) string {
	if kind == "issue" && state == "closed" {
		return "done"
	}
	if kind == "issue" && stored == "done" {
		return columns[0].Key
	}
	if !columnKnown(columns, stored) {
		return columns[0].Key
	}
	return stored
}

// syncIssueBoard keeps an issue's board column in step with its state.
func syncIssueBoard(ctx context.Context, tx pgx.Tx, repositoryID, issueID string, closed bool) error {
	if closed {
		_, err := tx.Exec(ctx, `INSERT INTO issue_board_status(issue_id,status) VALUES($1,'done') ON CONFLICT (issue_id) DO UPDATE SET status='done',updated_at=now()`, issueID)
		return err
	}
	columns, _, err := loadBoardColumns(ctx, tx, repositoryID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE issue_board_status SET status=$2,updated_at=now() WHERE issue_id=$1 AND status='done'`, issueID, columns[0].Key)
	return err
}

// syncPullBoard applies board automation when a unite request is opened,
// closed, merged, or reopened. event is one of those four words.
func syncPullBoard(ctx context.Context, tx pgx.Tx, repositoryID, pullID, event string) error {
	columns, automation, err := loadBoardColumns(ctx, tx, repositoryID)
	if err != nil || !automation {
		return err
	}
	switch event {
	case "opened":
		_, err = tx.Exec(ctx, `INSERT INTO pull_board_items(pull_request_id,status) VALUES($1,$2) ON CONFLICT DO NOTHING`, pullID, columns[0].Key)
	case "closed", "merged":
		_, err = tx.Exec(ctx, `UPDATE pull_board_items SET status='done',updated_at=now() WHERE pull_request_id=$1`, pullID)
	case "reopened":
		_, err = tx.Exec(ctx, `UPDATE pull_board_items SET status=$2,updated_at=now() WHERE pull_request_id=$1 AND status='done'`, pullID, columns[0].Key)
	}
	return err
}

func (a *App) boardFields(ctx context.Context, repositoryID string) ([]BoardField, error) {
	rows, err := a.db.Query(ctx, `SELECT id,name,kind,options,position FROM board_fields WHERE repository_id=$1 ORDER BY position,created_at`, repositoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := []BoardField{}
	for rows.Next() {
		var field BoardField
		if err = rows.Scan(&field.ID, &field.Name, &field.Kind, &field.Options, &field.Position); err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, rows.Err()
}

const boardItemColumns = `SELECT 'issue',i.id,i.repository_id,o.username,r.name,i.number,i.title,i.state,COALESCE(bs.status,''),u.username,i.priority,i.iteration,i.pinned,i.estimate,i.due_date,m.title,m.due_date
	FROM issues i JOIN users u ON u.id=i.author_id JOIN repositories r ON r.id=i.repository_id JOIN users o ON o.id=r.owner_id
	LEFT JOIN issue_board_status bs ON bs.issue_id=i.id LEFT JOIN milestones m ON m.id=i.milestone_id
	WHERE i.repository_id=ANY($1::uuid[])
	UNION ALL
	SELECT 'pull',p.id,p.repository_id,o.username,r.name,p.number,p.title,p.state,pb.status,u.username,'none','',false,NULL::smallint,NULL::date,NULL,NULL::date
	FROM pull_board_items pb JOIN pull_requests p ON p.id=pb.pull_request_id JOIN users u ON u.id=p.author_id JOIN repositories r ON r.id=p.repository_id JOIN users o ON o.id=r.owner_id
	WHERE p.repository_id=ANY($1::uuid[])`

// loadBoardItems returns one page of board items across repositories along
// with the total number of items.
func (a *App) loadBoardItems(ctx context.Context, repositoryIDs []string, columns map[string][]BoardColumn, page, perPage int) ([]BoardItem, int, error) {
	var total int
	if err := a.db.QueryRow(ctx, `SELECT count(*)::int FROM (`+boardItemColumns+`) items`, repositoryIDs).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := a.db.Query(ctx, `SELECT * FROM (`+boardItemColumns+`) items ORDER BY 13 DESC, 1, 6 DESC LIMIT $2 OFFSET $3`, repositoryIDs, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []BoardItem{}
	issueIDs, pullIDs := []string{}, []string{}
	for rows.Next() {
		var item BoardItem
		if err = rows.Scan(&item.Kind, &item.ItemID, &item.repositoryID, &item.Owner, &item.Repository, &item.Number, &item.Title, &item.State, &item.Status, &item.Author, &item.Priority, &item.Iteration, &item.Pinned, &item.Estimate, &item.DueDate, &item.Milestone, &item.MilestoneDue); err != nil {
			return nil, 0, err
		}
		item.Status = boardStatus(columns[item.repositoryID], item.Kind, item.State, item.Status)
		item.Fields = map[string]string{}
		if item.Kind == "issue" {
			item.IssueID = item.ItemID
			issueIDs = append(issueIDs, item.ItemID)
		} else {
			item.PullID = item.ItemID
			pullIDs = append(pullIDs, item.ItemID)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	values, err := a.db.Query(ctx, `SELECT field_id,COALESCE(issue_id::text,''),COALESCE(pull_request_id::text,''),value FROM board_field_values WHERE issue_id=ANY($1::uuid[]) OR pull_request_id=ANY($2::uuid[])`, issueIDs, pullIDs)
	if err != nil {
		return nil, 0, err
	}
	defer values.Close()
	byItem := map[string]map[string]string{}
	for values.Next() {
		var fieldID, issueID, pullID, value string
		if err = values.Scan(&fieldID, &issueID, &pullID, &value); err != nil {
			return nil, 0, err
		}
		key := issueID + pullID
		if byItem[key] == nil {
			byItem[key] = map[string]string{}
		}
		byItem[key][fieldID] = value
	}
	if err = values.Err(); err != nil {
		return nil, 0, err
	}
	for i := range items {
		if found := byItem[items[i].ItemID]; found != nil {
			items[i].Fields = found
		}
	}
	return items, total, nil
}

func (a *App) board(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	page, perPage, ok := pageParams(w, r, 100, 200)
	if !ok {
		return
	}
	columns, _, err := loadBoardColumns(r.Context(), a.db, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	items, total, err := a.loadBoardItems(r.Context(), []string{repo.ID}, map[string][]BoardColumn{repo.ID: columns}, page, perPage)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	respond(w, 200, items)
}

func (a *App) boardSettings(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	columns, automation, err := loadBoardColumns(r.Context(), a.db, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	fields, err := a.boardFields(r.Context(), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, BoardSettings{Columns: columns, Automation: automation, Fields: fields})
}

func (a *App) boardManager(w http.ResponseWriter, r *http.Request) *Repository {
	repo := a.access(w, r, false)
	if repo == nil {
		return nil
	}
	if !repo.CanWrite {
		fail(w, 403, "forbidden", "Repository write permission is required to configure the board.")
		return nil
	}
	if !activeRepository(w, repo) {
		return nil
	}
	return repo
}

func (a *App) updateBoardSettings(w http.ResponseWriter, r *http.Request) {
	repo := a.boardManager(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Columns    []BoardColumn `json:"columns"`
		Automation bool          `json:"automation"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Columns) < 2 || len(in.Columns) > maxBoardColumns || in.Columns[len(in.Columns)-1].Key != "done" {
		fail(w, 422, "validation_failed", fmt.Sprintf("Use between 2 and %d columns and keep Done as the last column.", maxBoardColumns))
		return
	}
	seen := map[string]bool{}
	keys := []string{}
	for i, column := range in.Columns {
		column.Key = strings.TrimSpace(column.Key)
		column.Name = strings.TrimSpace(column.Name)
		if !boardColumnKey.MatchString(column.Key) || column.Name == "" || len(column.Name) > 30 || seen[column.Key] {
			fail(w, 422, "validation_failed", "Give each column a distinct key of lowercase letters, numbers, or hyphens and a name up to 30 characters.")
			return
		}
		seen[column.Key] = true
		keys = append(keys, column.Key)
		in.Columns[i] = column
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `INSERT INTO boards(repository_id,columns,automation) VALUES($1,$2,$3)
		ON CONFLICT (repository_id) DO UPDATE SET columns=EXCLUDED.columns,automation=EXCLUDED.automation,updated_at=now()`, repo.ID, in.Columns, in.Automation); err != nil {
		serverError(w, err)
		return
	}
	// Items in a removed column move to the first column.
	if _, err = tx.Exec(r.Context(), `UPDATE issue_board_status SET status=$2,updated_at=now() WHERE status<>ALL($3::text[]) AND issue_id IN (SELECT id FROM issues WHERE repository_id=$1)`, repo.ID, keys[0], keys); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE pull_board_items SET status=$2,updated_at=now() WHERE status<>ALL($3::text[]) AND pull_request_id IN (SELECT id FROM pull_requests WHERE repository_id=$1)`, repo.ID, keys[0], keys); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.board_updated',$2)`, u.ID, repo.Owner+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.boardSettings(w, r)
}

func validBoardOptions(kind string, options []string) ([]string, bool) {
	if kind != "single_select" {
		return []string{}, len(options) == 0
	}
	if len(options) == 0 || len(options) > 20 {
		return nil, false
	}
	seen := map[string]bool{}
	cleaned := []string{}
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" || len(option) > 40 || seen[strings.ToLower(option)] {
			return nil, false
		}
		seen[strings.ToLower(option)] = true
		cleaned = append(cleaned, option)
	}
	return cleaned, true
}

func (a *App) createBoardField(w http.ResponseWriter, r *http.Request) {
	repo := a.boardManager(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Name    string   `json:"name"`
		Kind    string   `json:"kind"`
		Options []string `json:"options"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	options, valid := validBoardOptions(in.Kind, in.Options)
	if in.Name == "" || len(in.Name) > 40 || (in.Kind != "text" && in.Kind != "number" && in.Kind != "date" && in.Kind != "single_select") || !valid {
		fail(w, 422, "validation_failed", "Name the field (up to 40 characters), choose text, number, date, or single select, and give single-select fields 1 to 20 distinct options.")
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
	var count, position int
	if err = tx.QueryRow(r.Context(), `SELECT count(*)::int,COALESCE(max(position),0)+1 FROM board_fields WHERE repository_id=$1`, repo.ID).Scan(&count, &position); err != nil {
		serverError(w, err)
		return
	}
	if count >= maxBoardFields {
		fail(w, 422, "validation_failed", fmt.Sprintf("A board can have up to %d custom fields.", maxBoardFields))
		return
	}
	field := BoardField{ID: auth.ID(), Name: in.Name, Kind: in.Kind, Options: options, Position: position}
	_, err = tx.Exec(r.Context(), `INSERT INTO board_fields(id,repository_id,name,kind,options,position) VALUES($1,$2,$3,$4,$5,$6)`, field.ID, repo.ID, field.Name, field.Kind, field.Options, field.Position)
	if conflict(err) {
		fail(w, 409, "field_exists", "A field with that name already exists.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.board_field_created',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+field.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, field)
}

func (a *App) updateBoardField(w http.ResponseWriter, r *http.Request) {
	repo := a.boardManager(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Name    string   `json:"name"`
		Options []string `json:"options"`
	}
	if !decode(w, r, &in) {
		return
	}
	var field BoardField
	err := a.db.QueryRow(r.Context(), `SELECT id,name,kind,options,position FROM board_fields WHERE id::text=$1 AND repository_id=$2`, r.PathValue("id"), repo.ID).Scan(&field.ID, &field.Name, &field.Kind, &field.Options, &field.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Field not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	options, valid := validBoardOptions(field.Kind, in.Options)
	if in.Name == "" || len(in.Name) > 40 || !valid {
		fail(w, 422, "validation_failed", "Name the field (up to 40 characters) and give single-select fields 1 to 20 distinct options.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `UPDATE board_fields SET name=$1,options=$2 WHERE id=$3`, in.Name, options, field.ID)
	if conflict(err) {
		fail(w, 409, "field_exists", "A field with that name already exists.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if field.Kind == "single_select" {
		// Values for options that no longer exist are cleared.
		if _, err = tx.Exec(r.Context(), `DELETE FROM board_field_values WHERE field_id=$1 AND value<>ALL($2::text[])`, field.ID, options); err != nil {
			serverError(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	field.Name, field.Options = in.Name, options
	respond(w, 200, field)
}

func (a *App) deleteBoardField(w http.ResponseWriter, r *http.Request) {
	repo := a.boardManager(w, r)
	if repo == nil {
		return
	}
	var name string
	err := a.db.QueryRow(r.Context(), `DELETE FROM board_fields WHERE id::text=$1 AND repository_id=$2 RETURNING name`, r.PathValue("id"), repo.ID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Field not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.board_field_deleted',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+name)
	respond(w, 200, map[string]bool{"deleted": true})
}

// updateBoardValue sets or clears one custom field value on an issue or a
// unite request.
func (a *App) updateBoardValue(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	var in struct {
		Kind    string `json:"kind"`
		Number  int    `json:"number"`
		FieldID string `json:"field_id"`
		Value   string `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Value = strings.TrimSpace(in.Value)
	var field BoardField
	err := a.db.QueryRow(r.Context(), `SELECT id,name,kind,options FROM board_fields WHERE id::text=$1 AND repository_id=$2`, in.FieldID, repo.ID).Scan(&field.ID, &field.Name, &field.Kind, &field.Options)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Field not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if in.Value != "" {
		valid := len(in.Value) <= 200
		switch field.Kind {
		case "number":
			_, parseErr := strconv.ParseFloat(in.Value, 64)
			valid = valid && parseErr == nil
		case "date":
			_, parseErr := time.Parse("2006-01-02", in.Value)
			valid = parseErr == nil
		case "single_select":
			valid = containsString(field.Options, in.Value)
		}
		if !valid {
			fail(w, 422, "validation_failed", fmt.Sprintf("Enter a valid %s value for %s.", strings.ReplaceAll(field.Kind, "_", " "), field.Name))
			return
		}
	}
	var itemID string
	table, column := "issues", "issue_id"
	if in.Kind == "pull" {
		table, column = "pull_requests", "pull_request_id"
	} else if in.Kind != "issue" {
		fail(w, 422, "validation_failed", "Kind must be issue or pull.")
		return
	}
	err = a.db.QueryRow(r.Context(), `SELECT id FROM `+table+` WHERE repository_id=$1 AND number=$2`, repo.ID, in.Number).Scan(&itemID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Board item not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if in.Value == "" {
		_, err = a.db.Exec(r.Context(), `DELETE FROM board_field_values WHERE field_id=$1 AND `+column+`=$2`, field.ID, itemID)
	} else {
		_, err = a.db.Exec(r.Context(), `INSERT INTO board_field_values(field_id,`+column+`,value) VALUES($1,$2,$3)
			ON CONFLICT (field_id,`+column+`) WHERE `+column+` IS NOT NULL DO UPDATE SET value=EXCLUDED.value`, field.ID, itemID, in.Value)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"field_id": field.ID, "value": in.Value})
}

func (a *App) updateBoardItem(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &in) {
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
	columns, _, err := loadBoardColumns(r.Context(), tx, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if !columnKnown(columns, in.Status) {
		fail(w, 422, "validation_failed", "Choose one of this board's columns.")
		return
	}
	var issueID, previousState string
	if err = tx.QueryRow(r.Context(), `SELECT id,state FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID, &previousState); errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	state := "open"
	if in.Status == "done" {
		if previousState != "closed" {
			blocked, blockerErr := hasOpenBlockers(r.Context(), tx, issueID)
			if blockerErr != nil {
				serverError(w, blockerErr)
				return
			}
			if blocked {
				fail(w, 409, "open_blockers", "Close this issue's blockers before marking it Done.")
				return
			}
		}
		state = "closed"
	}
	reason := ""
	if state == "closed" {
		reason = "completed"
	}
	if _, err = tx.Exec(r.Context(), `UPDATE issues SET state=$1,state_reason=CASE WHEN state=$1 THEN state_reason ELSE $2 END,updated_at=now() WHERE id=$3`, state, reason, issueID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_board_status(issue_id,status) VALUES($1,$2)
		ON CONFLICT (issue_id) DO UPDATE SET status=EXCLUDED.status,updated_at=now()`, issueID, in.Status); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	if previousState != state {
		kind := "issue_closed"
		if state == "open" {
			kind = "issue_reopened"
		}
		if err = notifyIssue(r.Context(), tx, issueID, u.ID, kind); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.board_status_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%s", repo.Owner, repo.Name, number, in.Status)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": in.Status, "state": state})
}

// updatePullBoard adds a unite request to the board or moves it between
// columns. Moving a unite request never changes its state.
func (a *App) updatePullBoard(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	columns, _, err := loadBoardColumns(r.Context(), a.db, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if in.Status == "" {
		in.Status = columns[0].Key
	}
	if !columnKnown(columns, in.Status) {
		fail(w, 422, "validation_failed", "Choose one of this board's columns.")
		return
	}
	var pullID string
	err = a.db.QueryRow(r.Context(), `SELECT id FROM pull_requests WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&pullID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Unite request not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO pull_board_items(pull_request_id,status) VALUES($1,$2)
		ON CONFLICT (pull_request_id) DO UPDATE SET status=EXCLUDED.status,updated_at=now()`, pullID, in.Status); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": in.Status})
}

func (a *App) removePullBoard(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	result, err := a.db.Exec(r.Context(), `DELETE FROM pull_board_items WHERE pull_request_id=(SELECT id FROM pull_requests WHERE repository_id=$1 AND number=$2)`, repo.ID, number)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "That unite request is not on the board.")
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}

// districtBoard aggregates the boards of every district repository the
// viewer can read. Columns are merged by key with Done kept last.
func (a *App) districtBoard(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, false)
	if d == nil {
		return
	}
	page, perPage, ok := pageParams(w, r, 100, 200)
	if !ok {
		return
	}
	userID := ""
	if u := a.user(r); u != nil {
		userID = u.ID
	}
	rows, err := a.db.Query(r.Context(), `SELECT r.id FROM repositories r WHERE r.district_id=$1 AND r.deleted_at IS NULL AND (r.visibility='public' OR $2<>'' AND (r.owner_id::text=$2 OR EXISTS (SELECT 1 FROM repository_members rm WHERE rm.repository_id=r.id AND rm.user_id::text=$2) OR $3 IN ('owner','admin') OR (r.visibility='internal' AND $3<>'') OR ($3='member' AND $4<>'none'))) ORDER BY r.pushed_at DESC LIMIT 100`, d.ID, userID, d.Role, d.BasePermission)
	if err != nil {
		serverError(w, err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	perRepository := map[string][]BoardColumn{}
	merged := []BoardColumn{}
	for _, id := range ids {
		columns, _, loadErr := loadBoardColumns(r.Context(), a.db, id)
		if loadErr != nil {
			serverError(w, loadErr)
			return
		}
		perRepository[id] = columns
		for _, column := range columns {
			if column.Key != "done" && !columnKnown(merged, column.Key) {
				merged = append(merged, column)
			}
		}
	}
	if len(merged) == 0 {
		merged = append(merged, defaultBoardColumns[0])
	}
	merged = append(merged, BoardColumn{"done", "Done"})
	items, total, err := a.loadBoardItems(r.Context(), ids, perRepository, page, perPage)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	respond(w, 200, map[string]any{"columns": merged, "items": items, "total": total})
}
