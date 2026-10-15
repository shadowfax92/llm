package main

import (
	"dotllm/cmd"
	"os"

	"golang.org/x/term"
)

func main() {
	// Character devices include /dev/null. Interaction requires real terminal
	// descriptors on both streams; Dependencies.IsTTY remains injectable by callers.
	terminal := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	os.Exit(cmd.Execute(os.Args[1:], cmd.Dependencies{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, IsTTY: terminal}))
}
