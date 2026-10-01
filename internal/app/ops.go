package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

var errStorageQuota = errors.New("storage quota exceeded")

func repoQuota() int64 {
	return envBytes("GITOWN_REPO_QUOTA_BYTES", 512<<20)
}

func userQuota() int64 {
	return envBytes("GITOWN_USER_QUOTA_BYTES", 2<<30)
}

func envBytes(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func (a *App) extraRoot() string {
	return filepath.Join(a.git.Root, "_gitown")
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func (a *App) withinQuota(ctx context.Context, repo *Repository, incoming int64) error {
	if incoming < 0 {
		incoming = 0
	}
	if repo.SizeBytes+incoming > repoQuota() {
		return errStorageQuota
	}
	var total int64
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(sum(size_bytes),0) FROM repositories WHERE owner_id=$1 AND deleted_at IS NULL`, repo.OwnerID).Scan(&total); err != nil {
		return err
	}
	if total+incoming > userQuota() {
		return errStorageQuota
	}
	return nil
}

func (a *App) noteRepositoryFacts(ctx context.Context, repo *Repository) error {
	size := dirSize(a.git.Path(repo.ID)) + dirSize(filepath.Join(a.extraRoot(), "lfs", repo.ID))
	language := ""
	branch := repo.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	if out, err := a.git.Run(ctx, repo.ID, nil, "ls-tree", "-r", "--name-only", branch); err == nil {
		language = primaryLanguage(string(out))
	}
	now := time.Now()
	if _, err := a.db.Exec(ctx, `UPDATE repositories SET language=$1,size_bytes=$2,pushed_at=$3 WHERE id=$4`, language, size, now, repo.ID); err != nil {
		return err
	}
	repo.Language = language
	repo.SizeBytes = size
	repo.PushedAt = now
	_ = a.indexRepositoryCode(ctx, repo)
	return nil
}

func (a *App) enforceUnpackedQuota(ctx context.Context, repo *Repository, updates []refUpdate) error {
	if err := a.noteRepositoryFacts(ctx, repo); err != nil {
		return nil
	}
	if err := a.withinQuota(ctx, repo, 0); err != nil {
		if errors.Is(err, errStorageQuota) {
			a.rollbackRefs(ctx, repo.ID, updates)
			_ = a.noteRepositoryFacts(ctx, repo)
			return errStorageQuota
		}
		return nil
	}
	return nil
}

func (a *App) rollbackRefs(ctx context.Context, repoID string, updates []refUpdate) {
	zero := strings.Repeat("0", 40)
	for _, update := range updates {
		if !strings.HasPrefix(update.Ref, "refs/") || strings.Contains(update.Ref, "..") || strings.ContainsAny(update.Ref, " \r\n") {
			continue
		}
		if update.Old == zero && len(update.New) == 40 {
			_, _, _ = a.git.Command(ctx, 20*time.Second, repoID, "update-ref", "-d", update.Ref, update.New)
			continue
		}
		if len(update.Old) == 40 && len(update.New) == 40 {
			_, _, _ = a.git.Command(ctx, 20*time.Second, repoID, "update-ref", update.Ref, update.Old, update.New)
		}
	}
}

func primaryLanguage(listing string) string {
	counts := map[string]int{}
	for _, line := range strings.Split(listing, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if language, ok := extensionLanguages[ext]; ok {
			counts[language]++
		}
	}
	best := ""
	bestCount := 0
	for language, count := range counts {
		if count > bestCount || (count == bestCount && (best == "" || language < best)) {
			best = language
			bestCount = count
		}
	}
	return best
}

var extensionLanguages = map[string]string{
	".go": "Go", ".ts": "TypeScript", ".tsx": "TypeScript", ".js": "JavaScript", ".jsx": "JavaScript",
	".py": "Python", ".rs": "Rust", ".java": "Java", ".rb": "Ruby", ".md": "Markdown", ".css": "CSS",
	".html": "HTML", ".c": "C", ".h": "C", ".cpp": "C++", ".json": "JSON", ".yml": "YAML", ".yaml": "YAML",
}

func (a *App) finishReceive(ctx context.Context, repo *Repository, actorID string, updates []refUpdate, via string) {
	var actor any
	if actorID != "" {
		actor = actorID
	}
	_, _ = a.db.Exec(ctx, `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'git.receive_completed',$2)`, actor, repo.Owner+"/"+repo.Name)
	if actorID != "" {
		for _, update := range updates {
			if !strings.HasPrefix(update.Ref, "refs/heads/") {
				continue
			}
			branch := strings.TrimPrefix(update.Ref, "refs/heads/")
			_, _ = a.db.Exec(ctx, `INSERT INTO pull_events(pull_request_id,actor_id,kind,body)
				SELECT id,$2,'push',$3 FROM pull_requests WHERE repository_id=$1 AND head_branch=$4 AND state='open'`, repo.ID, actorID, update.Old+".."+update.New, branch)
		}
	}
	a.recordRefEvents(ctx, repo.ID, actorID, updates, via)
	for _, update := range updates {
		if update.Ref == "refs/heads/"+repo.DefaultBranch {
			a.closeIssuesFromPush(ctx, repo, actorID, update.Old, update.New)
		}
	}
	if len(updates) > 0 {
		branches := make([]string, 0, len(updates))
		for _, update := range updates {
			branches = append(branches, strings.TrimPrefix(update.Ref, "refs/heads/"))
		}
		if err := a.fireWebhook(ctx, a.db, repo.ID, repo.DistrictID, "push", map[string]any{
			"repository": map[string]string{"owner": repo.Owner, "name": repo.Name},
			"via":        via,
			"branches":   branches,
		}); err != nil {
			slog.Error("webhook enqueue failed", "error", err, "kind", "push")
		}
	}
	zeroSHA := strings.Repeat("0", 40)
	for _, update := range updates {
		if branch, ok := strings.CutPrefix(update.Ref, "refs/heads/"); ok && update.New != "" && update.New != zeroSHA {
			a.evaluateRouteTriggers(ctx, repo, actorID, branch, update.New, "push", "push via "+via)
		}
	}
	_ = a.noteRepositoryFacts(ctx, repo)
}

func (a *App) recordRefEvents(ctx context.Context, repositoryID, actorID string, updates []refUpdate, via string) {
	var actor any
	if actorID != "" {
		actor = actorID
	}
	for _, update := range updates {
		if !strings.HasPrefix(update.Ref, "refs/") || len(update.Old) != 40 || len(update.New) != 40 {
			continue
		}
		_, _ = a.db.Exec(ctx, `INSERT INTO ref_events(id,repository_id,actor_id,ref,old_sha,new_sha,via) VALUES($1,$2,$3,$4,$5,$6,$7)`, auth.ID(), repositoryID, actor, update.Ref, update.Old, update.New, via)
	}
}

func (a *App) refEvents(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 5000 {
			fail(w, 422, "validation_failed", "Offset must be between zero and 5000.")
			return
		}
	}
	rows, err := a.db.Query(r.Context(), `SELECT e.ref,e.old_sha,e.new_sha,e.via,e.created_at,COALESCE(u.username,'') FROM ref_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.repository_id=$1 ORDER BY e.created_at DESC,e.id DESC LIMIT 26 OFFSET $2`, repo.ID, offset)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	hasMore := false
	for rows.Next() {
		var ref, oldSHA, newSHA, via, actor string
		var created time.Time
		if err = rows.Scan(&ref, &oldSHA, &newSHA, &via, &created, &actor); err != nil {
			serverError(w, err)
			return
		}
		if len(items) == 25 {
			hasMore = true
			break
		}
		items = append(items, map[string]any{"ref": ref, "old_sha": oldSHA, "new_sha": newSHA, "via": via, "created_at": created, "actor": actor})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "has_more": hasMore})
}

func (a *App) maintainRepository(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	if err := a.runMaintenance(r.Context(), repo); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.maintained',$2)`, u.ID, repo.Owner+"/"+repo.Name)
	respond(w, 200, map[string]any{"size_bytes": repo.SizeBytes, "language": repo.Language, "last_maintained_at": repo.LastMaintainedAt})
}

func higherRole(current, next string) string {
	if roleRank(next) > roleRank(current) {
		return next
	}
	return current
}

func roleRank(role string) int {
	switch role {
	case "owner":
		return 5
	case "maintain":
		return 4
	case "write":
		return 3
	case "triage":
		return 2
	case "read":
		return 1
	default:
		return 0
	}
}
