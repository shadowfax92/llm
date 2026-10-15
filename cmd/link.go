package cmd

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"

	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newLinkCommand(a *app) *cobra.Command {
	var auto, pick bool
	var scope string
	cmd := &cobra.Command{Use: "link [project]", Short: "Link .llm to an exact existing project", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if (auto && (len(args) > 0 || pick)) || (pick && len(args) > 0) {
			return &contextstore.Error{Code: "invalid_input", Message: "choose one of a project, --auto or --pick", NextAction: "Use dotllm link --auto or dotllm link <project>."}
		}
		if auto {
			result, err := a.store.Ensure(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.Scope(scope)})
			if err != nil {
				return operationError{err}
			}
			return a.write(cmd, "link", output.LinkData(result), result.Warnings)
		}
		key := ""
		if pick {
			// Selection is the only interactive seam. Fzf sees exact registry keys and
			// cannot run in an agent data stream, even if --pick was passed explicitly.
			if !a.deps.IsTTY || a.json || a.noInput {
				return &contextstore.Error{Code: "invalid_input", Message: "--pick requires an interactive terminal", NextAction: "Use dotllm list, then dotllm link <project>."}
			}
			projects, err := a.store.Projects(cmd.Context())
			if err != nil {
				return operationError{err}
			}
			if len(projects) == 0 {
				return &contextstore.Error{Code: "missing_project", Message: "no registered projects to select", NextAction: "Initialize a project first."}
			}
			var input bytes.Buffer
			for _, p := range projects {
				input.WriteString(p.Key)
				input.WriteByte(0)
			}
			picker := exec.CommandContext(cmd.Context(), "fzf", "--read0", "--print0", "--no-multi", "--prompt=Project> ")
			picker.Stdin = &input
			picker.Stderr = cmd.ErrOrStderr()
			selected, err := picker.Output()
			if cmd.Context().Err() != nil {
				return operationError{cmd.Context().Err()}
			}
			if err != nil {
				var exit *exec.ExitError
				if errors.As(err, &exit) && (exit.ExitCode() == 1 || exit.ExitCode() == 130) {
					return &contextstore.Error{Code: "selector_required", Message: "project selection cancelled", NextAction: "Pass an exact project key to link."}
				}
				return operationError{err}
			}
			key = strings.TrimSuffix(string(selected), "\x00")
			found := false
			for _, p := range projects {
				if p.Key == key {
					found = true
					break
				}
			}
			if !found {
				return &contextstore.Error{Code: "invalid_input", Message: "picker returned an unknown project", NextAction: "Pass an exact key from dotllm list."}
			}
		} else if len(args) > 0 {
			key = args[0]
		} else {
			return &contextstore.Error{Code: "selector_required", Message: "an explicit project, --auto or --pick is required", NextAction: "Run dotllm list, then dotllm link <project>."}
		}
		result, err := a.store.Link(cmd.Context(), a.cwd, key, contextstore.Scope(scope))
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "link", output.LinkData(result), result.Warnings)
	}}
	cmd.Flags().BoolVar(&auto, "auto", false, "Ensure the canonical repository context")
	cmd.Flags().BoolVar(&pick, "pick", false, "Select an exact project with fzf (TTY only)")
	cmd.Flags().StringVar(&scope, "scope", "repo", "Link scope: repo or explicit cwd")
	return cmd
}
