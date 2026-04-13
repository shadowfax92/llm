package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize .llm for the current project",
	Long:  "Creates a central directory in ~/llm/ and symlinks .llm to it. Migrates existing .llm content if present.",
	RunE:  initRun,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func initRun(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	rel, err := filepath.Rel(homeDir, cwd)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("project must be under home directory (%s)", homeDir)
	}

	centralPath := filepath.Join(llmRoot, rel)
	llmPath := filepath.Join(cwd, ".llm")

	info, err := os.Lstat(llmPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(llmPath)
			if target == centralPath {
				fmt.Println("Already initialized.")
				return nil
			}
			return fmt.Errorf(".llm is already symlinked to %s\nUse 'llm link' to change it", target)
		}

		if info.IsDir() {
			if _, err := os.Stat(centralPath); err == nil {
				return fmt.Errorf("central path %s already exists; resolve manually", tildefy(centralPath))
			}

			fmt.Printf("Migrating .llm → %s\n", tildefy(centralPath))
			if err := os.MkdirAll(filepath.Dir(centralPath), 0755); err != nil {
				return fmt.Errorf("create parent dirs: %w", err)
			}
			if err := os.Rename(llmPath, centralPath); err != nil {
				return fmt.Errorf("migrate .llm: %w", err)
			}
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(centralPath, 0755); err != nil {
			return fmt.Errorf("create central directory: %w", err)
		}
	} else {
		return err
	}

	if err := os.Symlink(centralPath, llmPath); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}

	copyTemplates(centralPath)

	if err := addToRegistry(rel); err != nil {
		return fmt.Errorf("update registry: %w", err)
	}

	fmt.Printf("Initialized: .llm → %s\n", tildefy(centralPath))
	return nil
}

func copyTemplates(destDir string) {
	templateDir := filepath.Join(llmRoot, "templates")
	entries, err := os.ReadDir(templateDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dest := filepath.Join(destDir, e.Name())
		if _, err := os.Stat(dest); err == nil {
			continue // don't overwrite existing files
		}
		copyFile(filepath.Join(templateDir, e.Name()), dest)
	}
}

func copyFile(src, dst string) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer out.Close()
	io.Copy(out, in)
}
