package app

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strings"
)

func (a *App) indexRepositoryCode(ctx context.Context, repo *Repository) error {
	if repo == nil || repo.ID == "" {
		return nil
	}
	if _, err := a.db.Exec(ctx, `DELETE FROM code_documents WHERE repository_id=$1`, repo.ID); err != nil {
		return err
	}
	if repo.Visibility != "public" {
		return nil
	}
	branch := repo.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	out, err := a.git.Run(ctx, repo.ID, nil, "ls-tree", "-r", "--name-only", branch)
	if err != nil {
		return nil
	}
	files := 0
	for _, path := range strings.Split(string(out), "\n") {
		path = strings.TrimSpace(path)
		if path == "" || strings.Contains(path, "..") || strings.Contains(path, ":") || strings.HasPrefix(path, "/") {
			continue
		}
		if files == 40 {
			break
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != "" && ext != ".txt" && ext != ".mod" {
			if _, ok := extensionLanguages[ext]; !ok {
				continue
			}
		}
		shown, showErr := a.git.Run(ctx, repo.ID, nil, "show", branch+":"+path)
		if showErr != nil || len(shown) == 0 || len(shown) > 64*1024 || bytes.Contains(shown, []byte{0}) {
			continue
		}
		files++
		lines := strings.Split(string(shown), "\n")
		stored := 0
		for i, line := range lines {
			if stored == 40 {
				break
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if len(line) > 200 {
				line = line[:200]
			}
			if _, err = a.db.Exec(ctx, `INSERT INTO code_documents(repository_id,path,line,content) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, repo.ID, path, i+1, line); err != nil {
				return err
			}
			stored++
		}
	}
	return nil
}

func (a *App) searchCode(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 || len(q) > 80 || strings.HasPrefix(q, "-") || strings.ContainsAny(q, "\x00\r\n") {
		fail(w, 422, "validation_failed", "Use a code query between 2 and 80 characters.")
		return
	}
	like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	rows, err := a.db.Query(r.Context(), `SELECT u.username,r.name,c.path,c.line,c.content,
		ts_rank(to_tsvector('simple', c.content), plainto_tsquery('simple', $1))
		FROM code_documents c
		JOIN repositories r ON r.id=c.repository_id
		JOIN users u ON u.id=r.owner_id
		WHERE r.visibility='public' AND r.deleted_at IS NULL
		AND (to_tsvector('simple', c.content) @@ plainto_tsquery('simple', $1) OR c.content ILIKE $2 ESCAPE '\')
		ORDER BY 6 DESC, r.pushed_at DESC, c.path, c.line
		LIMIT 40`, q, "%"+like+"%")
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	matches := []CodeMatch{}
	for rows.Next() {
		var item CodeMatch
		if err = rows.Scan(&item.Owner, &item.Repository, &item.Path, &item.Line, &item.Snippet, &item.Rank); err != nil {
			serverError(w, err)
			return
		}
		matches = append(matches, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if len(matches) > 0 {
		respond(w, 200, map[string]any{"items": matches, "limited": len(matches) == 40, "ranked": true})
		return
	}
	matches = a.grepPublicCode(r, q)
	respond(w, 200, map[string]any{"items": matches, "limited": len(matches) == 40, "ranked": false})
}

func (a *App) grepPublicCode(r *http.Request, q string) []CodeMatch {
	rows, err := a.db.Query(r.Context(), `SELECT r.id,u.username,r.name,r.default_branch FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.visibility='public' AND r.deleted_at IS NULL ORDER BY r.pushed_at DESC,r.id DESC LIMIT 12`)
	if err != nil {
		return []CodeMatch{}
	}
	defer rows.Close()
	type candidate struct{ id, owner, name, branch string }
	var repos []candidate
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.id, &item.owner, &item.name, &item.branch); err != nil {
			return []CodeMatch{}
		}
		repos = append(repos, item)
	}
	matches := []CodeMatch{}
	for _, repo := range repos {
		if repo.branch == "" {
			continue
		}
		text, grepErr := a.git.Grep(r.Context(), repo.id, repo.branch, q)
		if grepErr != nil || text == "" {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			path, number, snippet, ok := parseGrepLine(line)
			if !ok {
				continue
			}
			matches = append(matches, CodeMatch{Owner: repo.owner, Repository: repo.name, Path: path, Line: number, Snippet: snippet})
			if len(matches) == 40 {
				return matches
			}
		}
	}
	return matches
}
