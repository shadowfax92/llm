package cmd

import (
	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newArchiveCommand(a *app) *cobra.Command {
	var days int
	var list, all, pick, apply bool
	var plan, planOut string
	cmd := &cobra.Command{Use: "archive [project...]", Short: "Review stale context (unavailable in skeleton; never moves files)", RunE: func(cmd *cobra.Command, args []string) error {
		if days < 0 || (apply && (plan == "" || len(args) > 0 || all || pick || list)) || (!apply && plan != "") {
			return &contextstore.Error{Code: "invalid_input", Message: "invalid archive options; apply requires only --apply --plan <file>", NextAction: "Review a plan before applying it."}
		}
		manager := archive.New(a.store)
		result, err := manager.Plan(cmd.Context(), archive.Selection{})
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "archive", output.ArchiveData{Plan: result}, result.Warnings)
	}}
	cmd.Flags().IntVar(&days, "days", 30, "Select entries older than this many days")
	cmd.Flags().BoolVar(&list, "list", false, "Read-only project counts")
	cmd.Flags().BoolVar(&all, "all", false, "Explicitly select all projects")
	cmd.Flags().BoolVar(&pick, "pick", false, "Explicitly request a TTY picker")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply an exact reviewed plan")
	cmd.Flags().StringVar(&plan, "plan", "", "Reviewed plan file")
	cmd.Flags().StringVar(&planOut, "plan-out", "", "Write a plan to this path")
	return cmd
}
