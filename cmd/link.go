package cmd

import (
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newLinkCommand(a *app) *cobra.Command {
	var auto, pick bool
	var scope string
	cmd := &cobra.Command{Use: "link [project]", Short: "Link .llm to an existing project", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if auto && (len(args) > 0 || pick) {
			return &contextstore.Error{Code: "invalid_input", Message: "--auto cannot be combined with a project or --pick", NextAction: "Use dotllm link --auto or dotllm link <project>."}
		}
		if pick {
			return &contextstore.Error{Code: "unavailable", Message: "interactive project picker is unavailable in the skeleton", NextAction: "Use dotllm list, then dotllm link <project>."}
		}
		var key string
		if auto {
			binding, err := a.store.Resolve(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.ScopeRepo})
			if err != nil {
				return operationError{err}
			}
			key = binding.Project.Key
		} else if len(args) > 0 {
			key = args[0]
		} else {
			return &contextstore.Error{Code: "selector_required", Message: "an explicit project or --auto is required", NextAction: "Run dotllm list, then dotllm link <project>."}
		}
		result, err := a.store.Link(cmd.Context(), a.cwd, key, contextstore.Scope(scope))
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "link", output.LinkData(result), result.Warnings)
	}}
	cmd.Flags().BoolVar(&auto, "auto", false, "Use legacy Git worktree-relative project lookup")
	cmd.Flags().BoolVar(&pick, "pick", false, "Explicitly request a project picker (unavailable in skeleton)")
	cmd.Flags().StringVar(&scope, "scope", "cwd", "Link scope")
	return cmd
}
