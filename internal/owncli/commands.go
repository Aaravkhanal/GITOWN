package owncli

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const Version = "0.1.0"

type Command struct {
	Name        string
	GitArgs     []string
	Usage       string
	Description string
	MinimumArgs int
}

var commands = map[string]Command{
	"bring": {"bring", []string{"clone"}, "gitown bring <repository> [directory]", "Clone a repository into your workspace.", 1},
	"look":  {"look", []string{"status", "--short", "--branch"}, "gitown look", "Show a compact view of your current work.", 0},
	"move":  {"move", []string{"switch"}, "gitown move <branch>", "Switch branches or create one with -c.", 1},
	"save":  {"save", []string{"commit"}, "gitown save -m <message>", "Create a commit from tracked changes.", 1},
	"send":  {"send", []string{"push"}, "gitown send [remote] [branch]", "Push commits and refs to GITOWN.", 0},
	"sync":  {"sync", []string{"pull", "--ff-only"}, "gitown sync [remote] [branch]", "Pull without creating an accidental merge commit.", 0},
	"track": {"track", []string{"add"}, "gitown track <path>...", "Stage paths for the next save.", 1},
	"unite": {"unite", []string{"merge"}, "gitown unite <branch>", "Merge another branch into the current branch.", 1},
}

func Resolve(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New("missing command")
	}
	if args[0] == "git" {
		if len(args) == 1 {
			return nil, errors.New("gitown git requires a Git command")
		}
		return append([]string(nil), args[1:]...), nil
	}
	// A new local branch has no upstream, so plain `git push` fails. GITOWN's
	// zero-argument send publishes the current branch and remembers origin.
	if args[0] == "send" && len(args) == 1 {
		return []string{"push", "--set-upstream", "origin", "HEAD"}, nil
	}
	command, ok := commands[args[0]]
	if !ok {
		return nil, fmt.Errorf("unknown command %q", args[0])
	}
	provided := args[1:]
	if len(provided) < command.MinimumArgs {
		return nil, fmt.Errorf("usage: %s", command.Usage)
	}
	result := append([]string(nil), command.GitArgs...)
	return append(result, provided...), nil
}

func Help() string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	var body strings.Builder
	body.WriteString("GITOWN CLI — familiar Git, simpler words.\n\nUsage:\n  gitown <command> [arguments]\n\nCommands:\n")
	for _, name := range names {
		command := commands[name]
		fmt.Fprintf(&body, "  %-8s %s\n", command.Name, command.Description)
	}
	body.WriteString("  git      Run any standard Git command.\n  help     Show this guide.\n  version  Show the CLI version.\n\nRun standard Git at any time with: gitown git <command> ...\n")
	return body.String()
}
