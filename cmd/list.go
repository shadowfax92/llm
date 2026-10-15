package cmd

import (
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newListCommand(a *app) *cobra.Command {
	return &cobra.Command{Use: "list", Aliases: []string{"ls", "l"}, Short: "List managed projects", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		projects, err := a.store.Projects(cmd.Context())
		if err != nil {
			return operationError{err}
		}
		binding, err := a.store.Resolve(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.ScopeRepo})
		if err != nil {
			return operationError{err}
		}
		current := ""
		if binding.State == "managed" {
			current = binding.Project.Key
		}
		return a.write(cmd, "list", output.ListData{Projects: projects, Current: current}, nil)
	}}
}
