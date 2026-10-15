package cmd

import (
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newInitCommand(a *app) *cobra.Command {
	var scope, project string
	cmd := &cobra.Command{Use: "init", Short: "Initialize .llm (legacy cwd scope in skeleton)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := a.store.Ensure(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.Scope(scope), ProjectKey: project})
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "init", output.LinkData(result), result.Warnings)
	}}
	cmd.Flags().StringVar(&scope, "scope", "cwd", "Context scope: cwd (legacy), repo (unavailable in skeleton)")
	cmd.Flags().StringVar(&project, "project", "", "Explicit project key")
	return cmd
}
