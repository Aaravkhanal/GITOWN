package gitstore

import (
	"context"
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
}
