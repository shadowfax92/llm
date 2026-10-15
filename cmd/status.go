package cmd

import (
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newStatusCommand(a *app) *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Inspect the current .llm binding without writing", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		binding, err := a.store.Resolve(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.ScopeRepo})
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "status", output.StatusData{Binding: binding}, nil)
	}}
}
