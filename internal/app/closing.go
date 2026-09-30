package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

// closingNumbers returns the issue numbers named by closing keywords such as
// "fixes #12" or "closes #3" in text.
func closingNumbers(text string) []int {
	seen := map[int]bool{}
	numbers := []int{}
	for _, match := range closingReference.FindAllStringSubmatch(text, 50) {
		n, err := strconv.Atoi(match[1])
		if err == nil && n > 0 && !seen[n] {
			seen[n] = true
			numbers = append(numbers, n)
		}
	}
	return numbers
}

// closeIssueNumbers closes the open issues with the given numbers, skipping
// issues that still have open blockers. A non-empty note is added to each
// closed issue as a comment by the actor.
func (a *App) closeIssueNumbers(ctx context.Context, tx pgx.Tx, repo *Repository, numbers []int, actorID, note string) error {
	sort.Ints(numbers)
	for _, number := range numbers {
		var issueID, state string
		err := tx.QueryRow(ctx, `SELECT id,state FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if state != "open" {
			continue
		}
		blocked, err := hasOpenBlockers(ctx, tx, issueID)
		if err != nil {
			return err
		}
		if blocked {
			continue
		}
		if _, err = tx.Exec(ctx, `UPDATE issues SET state='closed',state_reason='completed',updated_at=now() WHERE id=$1`, issueID); err != nil {
			return err
		}
		if err = syncIssueBoard(ctx, tx, repo.ID, issueID, true); err != nil {
			return err
		}
		if note != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO issue_comments(id,issue_id,author_id,body) VALUES($1,$2,$3,$4)`, auth.ID(), issueID, actorID, note); err != nil {
				return err
			}
		}
		if err = notifyIssue(ctx, tx, issueID, actorID, "issue_closed"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.closed',$2)`, actorID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)); err != nil {
			return err
		}
	}
	return nil
}

const maxClosingCommits = 200

// closeIssuesFromPush closes issues named by closing keywords in the commits
// a push or browser edit added to the default branch.
func (a *App) closeIssuesFromPush(ctx context.Context, repo *Repository, actorID, oldSHA, newSHA string) {
	zero := strings.Repeat("0", 40)
	if len(newSHA) != 40 || newSHA == zero {
		return
	}
	span := newSHA
	if len(oldSHA) == 40 && oldSHA != zero {
		span = oldSHA + ".." + newSHA
	}
	out, err := a.git.Run(ctx, repo.ID, nil, "log", "--max-count="+strconv.Itoa(maxClosingCommits), "--format=%H%x1f%B%x1e", span)
	if err != nil {
		return
	}
	type closing struct {
		sha     string
		numbers []int
	}
	var commits []closing
	for _, record := range strings.Split(string(out), "\x1e") {
		sha, message, found := strings.Cut(strings.TrimSpace(record), "\x1f")
		if !found || len(sha) != 40 {
			continue
		}
		if numbers := closingNumbers(message); len(numbers) > 0 {
			commits = append(commits, closing{sha, numbers})
		}
	}
	if len(commits) == 0 {
		return
	}
	if actorID == "" {
		actorID = repo.OwnerID
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		slog.Error("closing issues from push", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	// Oldest commits first so the note names the commit that closed the issue.
	for i := len(commits) - 1; i >= 0; i-- {
		note := "Closed by commit " + commits[i].sha[:12] + "."
		if err = a.closeIssueNumbers(ctx, tx, repo, commits[i].numbers, actorID, note); err != nil {
			slog.Error("closing issues from push", "error", err)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		slog.Error("closing issues from push", "error", err)
	}
}
