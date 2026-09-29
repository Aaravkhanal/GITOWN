package app

import (
	"net/http"
	"strings"
)

type IssueTemplate struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (a *App) issueTemplates(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT name,title,body FROM issue_templates WHERE repository_id=$1 ORDER BY position`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	templates := []IssueTemplate{}
	for rows.Next() {
		var template IssueTemplate
		if err = rows.Scan(&template.Name, &template.Title, &template.Body); err != nil {
			serverError(w, err)
			return
		}
		templates = append(templates, template)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
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
		folded := strings.ToLower(template.Name)
		if template.Name == "" || len(template.Name) > 80 || len(template.Title) > 200 || len(template.Body) > 10000 || seen[folded] {
			fail(w, 422, "validation_failed", "Use distinct template names up to 80 characters, titles up to 200, and bodies up to 10000.")
			return
		}
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
		if _, err = tx.Exec(r.Context(), `INSERT INTO issue_templates(repository_id,name,title,body,position) VALUES($1,$2,$3,$4,$5)`, repo.ID, template.Name, template.Title, template.Body, index+1); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.issue_templates_updated',$2)`, repo.OwnerID, repo.Owner+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.issueTemplates(w, r)
}
