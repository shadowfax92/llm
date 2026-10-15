package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

func newArchiveCommand(a *app) *cobra.Command {
	var days int
	var list, all, pick, apply bool
	var planPath, planOut, recoverID string
	cmd := &cobra.Command{Use: "archive [project...]", Short: "Review stale context; move only with --apply --plan", RunE: func(cmd *cobra.Command, args []string) error {
		if days < 0 || days > int((time.Duration(1<<63-1))/(24*time.Hour)) || all && len(args) > 0 || pick || apply && (planPath == "" || len(args) > 0 || all || list || planOut != "" || recoverID != "" || cmd.Flags().Changed("days")) || !apply && planPath != "" || recoverID != "" && (len(args) > 0 || all || list || planOut != "" || cmd.Flags().Changed("days")) {
			return archiveInput("invalid archive options; use a project, --all, or --apply --plan <file>; --pick is unavailable")
		}
		manager := archive.New(a.store)
		if recoverID != "" {
			result, err := manager.Recover(cmd.Context(), recoverID)
			if err != nil {
				return operationError{err}
			}
			return a.write(cmd, "archive", output.ApplyData{Result: result}, nil)
		}
		if apply {
			return a.applyArchivePlan(cmd, manager, planPath, "archive")
		}
		projects, err := a.store.Projects(cmd.Context())
		if err != nil {
			return operationError{err}
		}
		selected := []contextstore.Project{}
		if all {
			selected = projects
		} else if len(args) > 0 {
			for _, key := range args {
				found := false
				for _, p := range projects {
					if p.Key == key {
						selected = append(selected, p)
						found = true
						break
					}
				}
				if !found {
					return operationError{&contextstore.Error{Code: "missing_project", Message: "unknown registered project: " + key, NextAction: "Run dotllm list."}}
				}
			}
		} else {
			binding, err := a.store.Resolve(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.ScopeRepo})
			if err != nil {
				return operationError{err}
			}
			for _, p := range projects {
				if p.Key == binding.Project.Key {
					selected = append(selected, p)
					break
				}
			}
			if len(selected) == 0 {
				return archiveInput("no registered current project; select a project or explicitly use --all")
			}
		}
		if len(selected) == 0 {
			return archiveInput("no projects selected")
		}
		result, err := manager.Plan(cmd.Context(), archive.Selection{Projects: selected, OlderThan: time.Now().Add(-time.Duration(days) * 24 * time.Hour)})
		if err != nil {
			return operationError{err}
		}
		if planOut != "" {
			if err := writeArchivePlan(a.archivePlanPath(planOut), result); err != nil {
				return operationError{err}
			}
		}
		return a.write(cmd, "archive", output.ArchiveData{Plan: result}, result.Warnings)
	}}
	cmd.Flags().IntVar(&days, "days", 30, "Select entries strictly older than this many days")
	cmd.Flags().BoolVar(&list, "list", false, "Read-only compatibility view of the exact plan")
	cmd.Flags().BoolVar(&all, "all", false, "Explicitly select all registered projects")
	cmd.Flags().BoolVar(&pick, "pick", false, "Reserved interactive picker (unavailable)")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply an exact reviewed plan")
	cmd.Flags().StringVar(&planPath, "plan", "", "Reviewed plan file")
	cmd.Flags().StringVar(&planOut, "plan-out", "", "Write a new plan file without overwriting")
	cmd.Flags().StringVar(&recoverID, "recover", "", "Reconcile a recorded operation without moving data")
	return cmd
}
func archiveInput(message string) error {
	return &contextstore.Error{Code: "invalid_input", Message: message, NextAction: "Review archive/restore --help; planning never moves data."}
}

// Plans cross the filesystem/CLI seam as one bounded JSON value. Never truncate
// a reviewed file: explicit publication uses O_EXCL, including symlink clashes.
func writeArchivePlan(path string, plan archive.Plan) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
func readArchivePlan(path string) (archive.Plan, error) {
	var plan archive.Plan
	f, err := os.Open(path)
	if err != nil {
		return plan, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return plan, err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return plan, archiveInput("plan must be a regular JSON file no larger than 32 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, (32<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return plan, archiveInput("invalid plan JSON: " + err.Error())
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return plan, archiveInput("plan must contain exactly one JSON object")
	}
	return plan, nil
}
func (a *app) applyArchivePlan(cmd *cobra.Command, manager *archive.Manager, path, operation string) error {
	plan, err := readArchivePlan(a.archivePlanPath(path))
	if err != nil {
		return operationError{err}
	}
	if plan.Operation != operation {
		return archiveInput(fmt.Sprintf("%s requires a reviewed %s plan", operation, operation))
	}
	result, err := manager.Apply(cmd.Context(), plan)
	if err != nil {
		return operationError{err}
	}
	return a.write(cmd, operation, output.ApplyData{Result: result}, nil)
}

// -C applies to file arguments too; the process working directory stays intact.
func (a *app) archivePlanPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(a.cwd, path)
}
