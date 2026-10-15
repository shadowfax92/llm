package cmd

import (
	"dotllm/internal/config"
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newConfigCommand(a *app) *cobra.Command {
	var pathOnly bool
	cmd := &cobra.Command{Use: "config", Short: "Print effective configuration without opening an editor", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		path := config.Path(a.deps.Home)
		if pathOnly {
			return a.write(cmd, "config", output.PathData{Path: path}, nil)
		}
		binding, err := a.store.Resolve(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.ScopeCwd})
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "config", output.ConfigData{Path: path, StoreRoot: a.storeRoot, Source: a.configSource, Binding: binding}, nil)
	}}
	cmd.Flags().BoolVar(&pathOnly, "path", false, "Print the config file path")
	cmd.AddCommand(&cobra.Command{Use: "edit", Short: "Edit configuration (unavailable in skeleton)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return &contextstore.Error{Code: "unavailable", Message: "config editing is unavailable in the skeleton", NextAction: "Use dotllm config --path, then edit the file explicitly."}
	}})
	return cmd
}
