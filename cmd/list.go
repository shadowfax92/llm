package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "l"},
	Short:   "List all managed projects",
	RunE:    listRun,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func listRun(cmd *cobra.Command, args []string) error {
	projects, err := loadRegistry()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	if len(projects) == 0 {
		fmt.Println("No projects. Run 'llm init' in a project to get started.")
		return nil
	}

	// Check if cwd links to any project
	var currentTarget string
	cwd, _ := os.Getwd()
	if cwd != "" {
		if target, err := os.Readlink(filepath.Join(cwd, ".llm")); err == nil {
			currentTarget = target
		}
	}

	for _, p := range projects {
		fullPath := filepath.Join(llmRoot, p)
		marker := "  "
		if fullPath == currentTarget {
			marker = "→ "
		} else if _, err := os.Stat(fullPath); err != nil {
			marker = "! "
		}
		fmt.Printf("%s%s\n", marker, p)
	}
	return nil
}
