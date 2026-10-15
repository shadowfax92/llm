package main

import (
	"dotllm/cmd"
	"os"
)

func main() {
	input, _ := os.Stdin.Stat()
	output, _ := os.Stdout.Stat()
	terminal := input != nil && output != nil && input.Mode()&os.ModeCharDevice != 0 && output.Mode()&os.ModeCharDevice != 0
	os.Exit(cmd.Execute(os.Args[1:], cmd.Dependencies{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, IsTTY: terminal}))
}
