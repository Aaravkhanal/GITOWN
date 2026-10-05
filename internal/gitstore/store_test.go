package gitstore

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

func TestCloneLocalAndCopyCommit(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := auth.ID()
	if err = s.Init(ctx, source, "source", "Owner", "owner@example.test", true); err != nil {
		t.Fatal(err)
	}
	sha, err := s.Resolve(ctx, source, "main")
	if err != nil {
		t.Fatal(err)
	}
	dest := auth.ID()
	if err = s.CloneLocal(ctx, dest, source); err != nil {
		t.Fatal(err)
	}
	copied, err := s.Resolve(ctx, dest, "main")
	if err != nil || copied != sha {
		t.Fatalf("local clone did not preserve main: %v %s", err, copied)
	}
	other := auth.ID()
	if err = s.Init(ctx, other, "other", "Owner", "owner@example.test", false); err != nil {
		t.Fatal(err)
	}
	if err = s.CopyCommit(ctx, other, source, sha); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Run(ctx, other, nil, "cat-file", "-e", sha); err != nil {
		t.Fatal(err)
	}
	if err = s.CloneHTTPS(ctx, auth.ID(), "file:///tmp/not-allowed"); err == nil {
		t.Fatal("https clone accepted a file URL")
	}
}

func TestExportBundleContainsAllRefs(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := auth.ID()
	ctx := context.Background()
	if err = s.Init(ctx, id, "export", "Owner", "owner@example.test", true); err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	if err = s.ExportBundle(ctx, id, &bundle); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bundle.Bytes(), []byte("# v2 git bundle")) {
		t.Fatalf("export is not a Git bundle: %q", bundle.Bytes()[:min(bundle.Len(), 40)])
	}
}

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
	renamed, err := s.MoveFile(ctx, id, "main", "docs/guide.md", "docs/renamed.md", []byte("# Renamed guide\n"), "Rename guide", "Owner", "owner@example.test", commit)
	if err != nil || renamed == commit {
		t.Fatalf("web rename failed: %s %v", renamed, err)
	}
	moved, err := s.Browse(ctx, id, "main", "docs/renamed.md")
	if err != nil || moved.Content == nil || *moved.Content != "# Renamed guide\n" {
		t.Fatalf("renamed file was not readable: %+v %v", moved, err)
	}
	if _, err = s.Browse(ctx, id, "main", "docs/guide.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("original path remained after rename: %v", err)
	}
	blame, err := s.Blame(ctx, id, "main", "docs/renamed.md")
	if err != nil || len(blame) != 1 || blame[0].Text != "# Renamed guide" {
		t.Fatalf("blame did not report the renamed file: %+v %v", blame, err)
	}
	if _, err = s.CommitFile(ctx, id, "main", "docs/guide.md", []byte("stale"), "Stale edit", "Owner", "owner@example.test", head); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit returned %v", err)
	}
	deleted, err := s.DeleteFile(ctx, id, "main", "docs/renamed.md", "Remove guide", "Owner", "owner@example.test", renamed)
	if err != nil || deleted == renamed {
		t.Fatalf("web delete failed: %s %v", deleted, err)
	}
	if _, err = s.Browse(ctx, id, "main", "docs/renamed.md"); !errors.Is(err, ErrNotFound) {
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
