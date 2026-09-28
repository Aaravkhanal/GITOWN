package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/Aaravkhanal/GITOWN/internal/owncli"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(owncli.Help())
		return
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-v" {
		fmt.Println("gitown", owncli.Version)
		return
	}
	gitArgs, err := owncli.Resolve(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitown:", err)
		fmt.Fprintln(os.Stderr, "Run 'gitown help' for available commands.")
		os.Exit(2)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitown: Git is required but was not found in PATH")
		os.Exit(127)
	}
	command := exec.Command(git, gitArgs...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	if err = command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "gitown:", err)
		os.Exit(1)
	}
}
