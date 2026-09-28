package gitstore

import (
	"context"
	"errors"
	"testing"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

func TestRepositoryAndSafeBrowsing(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := auth.ID()
	ctx := context.Background()
	if err = s.Init(ctx, id, "project", "Owner", "owner@example.test", true); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Browse(ctx, id, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 1 || tree.Entries[0].Name != "README.md" {
		t.Fatalf("unexpected tree: %+v", tree)
	}
	file, err := s.Browse(ctx, id, "main", "README.md")
	if err != nil || file.Content == nil {
		t.Fatalf("missing README: %v", err)
	}
	for _, ref := range []string{"--help", "../config", "main^{tree}", "main\n", "main:README.md"} {
		if _, err = s.Resolve(ctx, id, ref); err == nil {
			t.Errorf("accepted invalid ref %q", ref)
		}
	}
	for _, path := range []string{"../config", "a/../../config", "\x00", "/etc/passwd"} {
		if _, err = s.Browse(ctx, id, "main", path); err == nil {
			t.Errorf("accepted invalid path %q", path)
		}
	}
	if err = s.Init(ctx, id, "replacement", "Owner", "owner@example.test", true); err == nil {
		t.Fatal("overwrote existing storage")
	}
	head := tree.SHA
	commit, err := s.CommitFile(ctx, id, "main", "docs/guide.md", []byte("# Guide\n"), "Add guide", "Owner", "owner@example.test", head)
	if err != nil || commit == head {
		t.Fatalf("web commit failed: %s %v", commit, err)
	}
	created, err := s.Browse(ctx, id, "main", "docs/guide.md")
	if err != nil || created.Content == nil || *created.Content != "# Guide\n" {
		t.Fatalf("committed file was not readable: %+v %v", created, err)
	}
	raw, err := s.Blob(ctx, id, "main", "docs/guide.md")
	if err != nil || string(raw) != "# Guide\n" {
		t.Fatalf("raw file was not readable: %q %v", raw, err)
	}
	history, err := s.FileCommits(ctx, id, "main", "docs/guide.md")
	if err != nil || len(history) != 1 || history[0].Message != "Add guide" {
		t.Fatalf("file history was not returned: %+v %v", history, err)
	}
	if _, err = s.CommitFile(ctx, id, "main", "docs/guide.md", []byte("stale"), "Stale edit", "Owner", "owner@example.test", head); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit returned %v", err)
	}
	deleted, err := s.DeleteFile(ctx, id, "main", "docs/guide.md", "Remove guide", "Owner", "owner@example.test", commit)
	if err != nil || deleted == commit {
		t.Fatalf("web delete failed: %s %v", deleted, err)
	}
	if _, err = s.Browse(ctx, id, "main", "docs/guide.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted file remained readable: %v", err)
	}
	if _, err = s.DeleteFile(ctx, id, "main", "README.md", "Stale delete", "Owner", "owner@example.test", commit); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete returned %v", err)
	}
	for _, unsafe := range []string{"../config", ".git/config", ".GIT/config", "/tmp/file", "dir\\file"} {
		if _, err = s.CommitFile(ctx, id, "main", unsafe, []byte("bad"), "Bad path", "Owner", "owner@example.test", commit); err == nil {
			t.Fatalf("accepted unsafe web path %q", unsafe)
		}
	}
}
