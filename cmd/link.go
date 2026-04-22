package cmd

import (
	"fmt"
	"io"
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
			fmt.Printf("Backed up .llm → .llm.bak (safety copy)\n")

			added, skipped, err := mergeDir(backupPath, centralPath)
			if err != nil {
				return fmt.Errorf("merge .llm.bak → %s: %w", tildefy(centralPath), err)
			}
			reportMerge(centralPath, added, skipped)
		}
	}

	if err := os.Symlink(centralPath, llmPath); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}

	fmt.Printf("Linked: .llm → %s\n", tildefy(centralPath))
	return nil
}

// mergeDir copies regular files from src into dst. Files that already exist
// in dst are left untouched and returned in `skipped` so the caller can
// surface them. Directories and intermediate paths are created as needed.
// Non-regular, non-directory entries (symlinks, sockets, etc.) are skipped.
func mergeDir(src, dst string) (added, skipped []string, err error) {
	err = filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		destPath := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if _, statErr := os.Stat(destPath); statErr == nil {
			skipped = append(skipped, rel)
			return nil
		}
		if err := copyFileMode(path, destPath, info.Mode()); err != nil {
			return err
		}
		added = append(added, rel)
		return nil
	})
	return
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func reportMerge(centralPath string, added, skipped []string) {
	central := tildefy(centralPath)
	if len(added) == 0 && len(skipped) == 0 {
		fmt.Printf("(.llm was empty; nothing to move)\n")
		return
	}
	if len(added) > 0 {
		fmt.Printf("Moved %d file(s) into %s:\n", len(added), central)
		for _, f := range added {
			fmt.Printf("  + %s\n", f)
		}
	}
	if len(skipped) > 0 {
		fmt.Printf("Skipped %d file(s) (already exist in %s — resolve from .llm.bak if needed):\n", len(skipped), central)
		for _, f := range skipped {
			fmt.Printf("  ~ %s\n", f)
		}
	}
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
