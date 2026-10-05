package owncli

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		input []string
		want  []string
	}{
		{[]string{"bring", "https://example.test/team/repo.git"}, []string{"clone", "https://example.test/team/repo.git"}},
		{[]string{"track", "README.md", "src"}, []string{"add", "README.md", "src"}},
		{[]string{"save", "-m", "Document commands"}, []string{"commit", "-m", "Document commands"}},
		{[]string{"send", "origin", "main"}, []string{"push", "origin", "main"}},
		{[]string{"send"}, []string{"push", "--set-upstream", "origin", "HEAD"}},
		{[]string{"sync"}, []string{"pull", "--ff-only"}},
		{[]string{"unite", "feature"}, []string{"merge", "feature"}},
		{[]string{"move", "-c", "feature"}, []string{"switch", "-c", "feature"}},
		{[]string{"look"}, []string{"status", "--short", "--branch"}},
		{[]string{"git", "log", "--oneline"}, []string{"log", "--oneline"}},
	}
	for _, test := range tests {
		got, err := Resolve(test.input)
		if err != nil {
			t.Fatalf("Resolve(%v): %v", test.input, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("Resolve(%v) = %v, want %v", test.input, got, test.want)
		}
	}
}

func TestResolveRejectsIncompleteAndUnknownCommands(t *testing.T) {
	for _, input := range [][]string{{}, {"bring"}, {"track"}, {"unite"}, {"git"}, {"unknown"}} {
		if _, err := Resolve(input); err == nil {
			t.Fatalf("Resolve(%v) unexpectedly succeeded", input)
		}
	}
}

func TestHelpDocumentsEveryCommand(t *testing.T) {
	help := Help()
	for _, name := range []string{"bring", "look", "move", "save", "send", "sync", "track", "unite", "git"} {
		if !strings.Contains(help, name) {
			t.Fatalf("help does not mention %s", name)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, err := Completion(shell)
		if err != nil || !strings.Contains(script, "gitown") || !strings.Contains(script, "bring") || !strings.Contains(script, "unite") {
			t.Fatalf("Completion(%q) = %q, %v", shell, script, err)
		}
	}
	if _, err := Completion("powershell"); err == nil {
		t.Fatal("unsupported shell unexpectedly accepted")
	}
}
