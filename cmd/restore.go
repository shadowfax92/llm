package cmd

import (
	"dotllm/internal/archive"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newRestoreCommand(a *app) *cobra.Command {
	var archiveID, planPath, planOut string
	var entries []string
	var apply bool
	cmd := &cobra.Command{Use: "restore", Short: "Review exact recorded origins; restore only to absent paths", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if apply && (planPath == "" || archiveID != "" || len(entries) > 0 || planOut != "") || !apply && (planPath != "" || archiveID == "") {
			return archiveInput("restore requires --archive <id>, or --apply --plan <file>")
		}
		manager := archive.New(a.store)
		if apply {
			return a.applyArchivePlan(cmd, manager, planPath, "restore")
		}
		result, err := manager.PlanRestore(cmd.Context(), archiveID, entries)
		if err != nil {
			return operationError{err}
		}
		if planOut != "" {
			if err := writeArchivePlan(a.archivePlanPath(planOut), result); err != nil {
				return operationError{err}
			}
		}
		return a.write(cmd, "restore", output.ArchiveData{Plan: result}, result.Warnings)
	}}
	cmd.Flags().StringVar(&archiveID, "archive", "", "Journaled archive plan ID")
	cmd.Flags().StringSliceVar(&entries, "entry", nil, "Exact archived relative entry (project/name disambiguates)")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply a reviewed restore plan")
	cmd.Flags().StringVar(&planPath, "plan", "", "Reviewed restore plan file")
	cmd.Flags().StringVar(&planOut, "plan-out", "", "Write a new restore plan without overwriting")
	return cmd
}
