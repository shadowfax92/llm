package cmd

import (
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newPathCommand(a *app) *cobra.Command {
	return &cobra.Command{Use: "path", Short: "Ensure canonical context and print its path", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := a.store.Ensure(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.ScopeRepo})
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "path", output.PathData{Path: result.Binding.Project.Root, Anchor: result.Binding.Anchor, Changed: result.Changed}, result.Warnings)
	}}
}
