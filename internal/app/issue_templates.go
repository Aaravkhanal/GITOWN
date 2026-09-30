package app

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

type IssueTemplate struct {
	Name    string           `json:"name"`
	Title   string           `json:"title"`
	Body    string           `json:"body"`
	Kind    string           `json:"kind"`
	Fields  []issueFormField `json:"fields"`
	Builtin bool             `json:"builtin,omitempty"`
}

const maxTemplateFields = 20

var formFieldID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

// builtinIssueTemplates are offered when a repository has not defined its
// own template of the same kind.
var builtinIssueTemplates = []IssueTemplate{
	{Name: "Bug report", Title: "Bug: ", Kind: "bug", Builtin: true, Fields: []issueFormField{
		{ID: "summary", Label: "What happened?", Type: "textarea", Required: true},
		{ID: "steps", Label: "Steps to reproduce", Type: "textarea", Required: true},
		{ID: "expected", Label: "What did you expect to happen?", Type: "textarea", Required: true},
		{ID: "severity", Label: "Severity", Type: "dropdown", Options: []string{"Low", "Medium", "High", "Critical"}},
		{ID: "environment", Label: "Environment", Type: "text"},
	}},
	{Name: "Feature request", Title: "Feature: ", Kind: "feature", Builtin: true, Fields: []issueFormField{
		{ID: "problem", Label: "What problem would this solve?", Type: "textarea", Required: true},
		{ID: "proposal", Label: "Proposed solution", Type: "textarea", Required: true},
		{ID: "alternatives", Label: "Alternatives considered", Type: "textarea"},
		{ID: "contribute", Label: "I would like to help build this", Type: "checkbox"},
	}},
}

func validateFormFields(fields []issueFormField) ([]issueFormField, bool) {
	if len(fields) > maxTemplateFields {
		return nil, false
	}
	seen := map[string]bool{}
	cleaned := make([]issueFormField, 0, len(fields))
	for _, field := range fields {
		field.ID = strings.TrimSpace(field.ID)
		field.Label = strings.TrimSpace(field.Label)
		if !formFieldID.MatchString(field.ID) || seen[field.ID] || field.Label == "" || len(field.Label) > 100 {
			return nil, false
		}
		seen[field.ID] = true
		switch field.Type {
		case "text", "textarea", "checkbox":
			if len(field.Options) > 0 {
				return nil, false
			}
			field.Options = []string{}
		case "dropdown":
			options, ok := validBoardOptions("single_select", field.Options)
			if !ok {
				return nil, false
			}
			field.Options = options
		default:
			return nil, false
		}
		cleaned = append(cleaned, field)
	}
	return cleaned, true
}

func (a *App) storedIssueTemplates(ctx context.Context, repositoryID string) ([]IssueTemplate, error) {
	rows, err := a.db.Query(ctx, `SELECT name,title,body,kind,fields FROM issue_templates WHERE repository_id=$1 ORDER BY position`, repositoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	templates := []IssueTemplate{}
	for rows.Next() {
		var template IssueTemplate
		if err = rows.Scan(&template.Name, &template.Title, &template.Body, &template.Kind, &template.Fields); err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, rows.Err()
}

// withBuiltinTemplates appends the built-in forms whose kind and name the
// repository has not already defined.
func withBuiltinTemplates(templates []IssueTemplate) []IssueTemplate {
	kinds := map[string]bool{}
	names := map[string]bool{}
	for _, template := range templates {
		kinds[template.Kind] = true
		names[strings.ToLower(template.Name)] = true
	}
	for _, builtin := range builtinIssueTemplates {
		if !kinds[builtin.Kind] && !names[strings.ToLower(builtin.Name)] {
			templates = append(templates, builtin)
		}
	}
	return templates
}

// issueTemplateFields finds the form fields for a named template, including
// built-in forms that the repository has not overridden.
func (a *App) issueTemplateFields(ctx context.Context, repositoryID, name string) ([]issueFormField, error) {
	var fields []issueFormField
	err := a.db.QueryRow(ctx, `SELECT fields FROM issue_templates WHERE repository_id=$1 AND lower(name)=lower($2)`, repositoryID, name).Scan(&fields)
	if !errors.Is(err, pgx.ErrNoRows) {
		return fields, err
	}
	stored, err := a.storedIssueTemplates(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	for _, template := range withBuiltinTemplates(stored) {
		if template.Builtin && strings.EqualFold(template.Name, name) {
			return template.Fields, nil
		}
	}
	return nil, pgx.ErrNoRows
}

func (a *App) issueTemplates(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	templates, err := a.storedIssueTemplates(r.Context(), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if r.URL.Query().Get("include_defaults") == "true" {
		templates = withBuiltinTemplates(templates)
	}
	respond(w, 200, templates)
}

func (a *App) updateIssueTemplates(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanManage {
		fail(w, 403, "forbidden", "Only the repository owner can change issue templates.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	var in struct {
		Templates []IssueTemplate `json:"templates"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Templates == nil || len(in.Templates) > 10 {
		fail(w, 422, "validation_failed", "Choose up to ten issue templates.")
		return
	}
	seen := map[string]bool{}
	for i, template := range in.Templates {
		template.Name = strings.TrimSpace(template.Name)
		template.Title = strings.TrimSpace(template.Title)
		template.Body = strings.TrimSpace(template.Body)
		template.Builtin = false
		if template.Kind == "" {
			template.Kind = "custom"
		}
		folded := strings.ToLower(template.Name)
		if template.Name == "" || len(template.Name) > 80 || len(template.Title) > 200 || len(template.Body) > 10000 || seen[folded] || (template.Kind != "bug" && template.Kind != "feature" && template.Kind != "custom") {
			fail(w, 422, "validation_failed", "Use distinct template names up to 80 characters, titles up to 200, and bodies up to 10000.")
			return
		}
		fields, ok := validateFormFields(template.Fields)
		if !ok {
			fail(w, 422, "validation_failed", "Use up to 20 form fields with distinct lowercase IDs, labels up to 100 characters, and a text, textarea, dropdown, or checkbox type. Dropdowns need 1 to 20 options.")
			return
		}
		template.Fields = fields
		seen[folded] = true
		in.Templates[i] = template
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
	if _, err = tx.Exec(r.Context(), `DELETE FROM issue_templates WHERE repository_id=$1`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	for index, template := range in.Templates {
		if _, err = tx.Exec(r.Context(), `INSERT INTO issue_templates(repository_id,name,title,body,position,kind,fields) VALUES($1,$2,$3,$4,$5,$6,$7)`, repo.ID, template.Name, template.Title, template.Body, index+1, template.Kind, template.Fields); err != nil {
			serverError(w, err)
			return
		}
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.issue_templates_updated',$2)`, u.ID, repo.Owner+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.issueTemplates(w, r)
}
