package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	llmRoot string
	homeDir string
)

var rootCmd = &cobra.Command{
	Use:   "llm",
	Short: "Manage .llm project context directories",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatus()
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home: %w", err)
		}
		homeDir = home
		llmRoot = filepath.Join(home, "llm")
		return nil
	}
}

func runStatus() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	llmPath := filepath.Join(cwd, ".llm")
	info, err := os.Lstat(llmPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No .llm directory. Run 'llm init' to initialize.")
			return nil
		}
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(llmPath)
		if err != nil {
			return err
		}
		fmt.Printf(".llm → %s\n", tildefy(target))

		entries, err := os.ReadDir(target)
		if err == nil {
			files := 0
			dirs := 0
			for _, e := range entries {
				if e.IsDir() {
					dirs++
				} else {
					files++
				}
			}
			fmt.Printf("  %d files, %d dirs\n", files, dirs)
		}
	} else if info.IsDir() {
		fmt.Println(".llm exists as a local directory (not managed)")
		fmt.Println("Run 'llm init' to centralize.")
	}

	return nil
}

func tildefy(path string) string {
	if rel, err := filepath.Rel(homeDir, path); err == nil && rel[0] != '.' {
		return "~/" + rel
	}
	return path
}
