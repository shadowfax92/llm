package cmd

import (
	"dotllm/internal/archive"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newRestoreCommand(a *app) *cobra.Command {
	var archiveID, plan string
	var entries []string
	var apply bool
	cmd := &cobra.Command{Use: "restore", Short: "Review an archive restore (unavailable in skeleton)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := archive.New(a.store).PlanRestore(cmd.Context(), archiveID, entries)
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "restore", output.ArchiveData{Plan: result}, result.Warnings)
	}}
	cmd.Flags().StringVar(&archiveID, "archive", "", "Archive plan ID")
	cmd.Flags().StringSliceVar(&entries, "entry", nil, "Exact archived relative entry")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply a reviewed restore plan")
	cmd.Flags().StringVar(&plan, "plan", "", "Reviewed plan file")
	return cmd
}
