package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var linkCmd = &cobra.Command{
	Use:   "link [project]",
	Short: "Link .llm to an existing project",
	Long:  "Links .llm in the current directory to an existing project in ~/llm/.\nWithout arguments, shows an interactive picker.",
	RunE:  linkRun,
}

func init() {
	rootCmd.AddCommand(linkCmd)
}

func linkRun(cmd *cobra.Command, args []string) error {
	var project string

	if len(args) > 0 {
		project = args[0]
	} else {
		projects, err := loadRegistry()
		if err != nil {
			return fmt.Errorf("load registry: %w", err)
		}
		if len(projects) == 0 {
			return fmt.Errorf("no projects registered; run 'llm init' in a project first")
		}

		if _, err := exec.LookPath("fzf"); err != nil {
			return fmt.Errorf("fzf not found; install it or pass project as argument")
		}

		selected, err := fzfPick(projects, "Project> ")
		if err != nil {
			return err
		}
		project = selected
	}

	centralPath := filepath.Join(llmRoot, project)
	if _, err := os.Stat(centralPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("project %q not found at %s", project, tildefy(centralPath))
		}
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	llmPath := filepath.Join(cwd, ".llm")

	if info, err := os.Lstat(llmPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			existing, _ := os.Readlink(llmPath)
			if existing == centralPath {
				fmt.Println("Already linked.")
				return nil
			}
			os.Remove(llmPath)
		} else if info.IsDir() {
			backupPath := llmPath + ".bak"
			if _, err := os.Stat(backupPath); err == nil {
				return fmt.Errorf(".llm and .llm.bak both exist; resolve manually")
			}
			if err := os.Rename(llmPath, backupPath); err != nil {
				return fmt.Errorf("backup .llm: %w", err)
			}
			fmt.Printf("Backed up .llm → .llm.bak\n")
		}
	}

	if err := os.Symlink(centralPath, llmPath); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}

	fmt.Printf("Linked: .llm → %s\n", tildefy(centralPath))
	return nil
}

func fzfPick(items []string, prompt string) (string, error) {
	fzf := exec.Command("fzf", "--prompt", prompt)
	fzf.Stdin = strings.NewReader(strings.Join(items, "\n"))
	fzf.Stderr = os.Stderr
	out, err := fzf.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", fmt.Errorf("cancelled")
		}
		return "", fmt.Errorf("fzf: %w", err)
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		return "", fmt.Errorf("no selection")
	}
	return result, nil
}
