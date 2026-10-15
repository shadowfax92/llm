package cmd

import (
	"dotllm/internal/config"
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func newConfigCommand(a *app) *cobra.Command {
	var pathOnly bool
	cmd := &cobra.Command{Use: "config", Short: "Print effective configuration without opening an editor", Args: cobra.NoArgs, PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if pathOnly || cmd.Name() == "edit" {
			// Recovery commands must work even when Load rejects the config. They need
			// only its location, and must not open/validate/create a context store.
			if a.deps.Home == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return operationError{err}
				}
				a.deps.Home = home
			}
			if !filepath.IsAbs(a.deps.Home) {
				return &contextstore.Error{Code: "invalid_input", Message: "home must be absolute", NextAction: "Use an absolute home directory."}
			}
			return nil
		}
		return a.prepare()
	}, RunE: func(cmd *cobra.Command, args []string) error {
		path := config.Path(a.deps.Home)
		if pathOnly {
			return a.write(cmd, "config", output.PathData{Path: path}, nil)
		}
		binding, err := a.store.Resolve(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.ScopeRepo})
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "config", output.ConfigData{Path: path, StoreRoot: a.storeRoot, Source: a.configSource, Binding: binding}, nil)
	}}
	cmd.Flags().BoolVar(&pathOnly, "path", false, "Print the config file path")
	cmd.AddCommand(&cobra.Command{Use: "edit", Short: "Explicitly open configuration in an editor (TTY only)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if !a.deps.IsTTY || a.json || a.noInput {
			return &contextstore.Error{Code: "invalid_input", Message: "config edit requires an interactive terminal", NextAction: "Use dotllm config --path and edit the file explicitly."}
		}
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			editor = "vi"
		}
		// Editor arguments are passed directly; never evaluate environment text in a
		// shell. Set EDITOR to an executable/wrapper when quoting is required.
		editorArgs := strings.Fields(editor)
		if len(editorArgs) == 0 {
			return &contextstore.Error{Code: "invalid_input", Message: "empty editor command", NextAction: "Set EDITOR to an executable."}
		}
		path, err := config.Create(a.deps.Home)
		if err != nil {
			return operationError{err}
		}
		child := exec.CommandContext(cmd.Context(), editorArgs[0], append(editorArgs[1:], path)...)
		child.Stdin = cmd.InOrStdin()
		child.Stdout = cmd.OutOrStdout()
		child.Stderr = cmd.ErrOrStderr()
		if err := child.Run(); err != nil {
			return operationError{err}
		}
		if _, err := config.Load(a.deps.Home); err != nil {
			return &contextstore.Error{Code: "invalid_config", Message: err.Error(), NextAction: "Correct the edited config file."}
		}
		return a.write(cmd, "config", output.PathData{Path: path}, nil)
	}})
	return cmd
}
