package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const defaultArchiveDays = 30

var (
	archiveDays     = defaultArchiveDays
	archiveListOnly bool
)

var archiveCmd = &cobra.Command{
	Use:   "archive [project...]",
	Short: "Archive stale .llm entries",
	Long:  "Lists registered projects with stale .llm entries and moves selected old entries into .llm/archive/.",
	RunE:  archiveRun,
}

type archiveCandidate struct {
	name          string
	path          string
	newestModTime time.Time
}

type archiveSummary struct {
	project string
	count   int
}

type archiveMove struct {
	destName string
}

type archiveResult struct {
	project    string
	archiveDir string
	moved      []archiveMove
}

func init() {
	archiveCmd.Flags().IntVar(&archiveDays, "days", defaultArchiveDays, "archive entries older than this many days")
	archiveCmd.Flags().BoolVar(&archiveListOnly, "list", false, "list archiveable project counts without moving files")
	rootCmd.AddCommand(archiveCmd)
}

// archiveRun lists stale project counts, then archives explicit or fzf-selected projects.
func archiveRun(cmd *cobra.Command, args []string) error {
	if archiveDays < 0 {
		return fmt.Errorf("--days must be >= 0")
	}

	now := time.Now()
	cutoff := now.AddDate(0, 0, -archiveDays)
	out := cmd.OutOrStdout()

	if archiveListOnly {
		summaries, err := archiveProjectSummaries(cutoff)
		if err != nil {
			return err
		}
		printArchiveSummaries(out, summaries, archiveDays)
		return nil
	}

	projects := args
	if len(projects) == 0 {
		summaries, err := archiveProjectSummaries(cutoff)
		if err != nil {
			return err
		}
		if len(summaries) == 0 {
			fmt.Fprintf(out, "No archive candidates older than %d days.\n", archiveDays)
			return nil
		}
		projects, err = fzfPickArchiveProjects(summaries)
		if err != nil {
			return err
		}
	}

	return archiveSelectedProjects(out, projects, cutoff, now)
}

// archiveCandidates returns stale top-level project entries using recursive mtime freshness.
func archiveCandidates(projectDir string, cutoff time.Time) ([]archiveCandidate, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}

	var candidates []archiveCandidate
	for _, entry := range entries {
		if entry.Name() == "archive" {
			continue
		}

		path := filepath.Join(projectDir, entry.Name())
		newest, err := newestModTime(path)
		if err != nil {
			return nil, err
		}
		if newest.Before(cutoff) {
			candidates = append(candidates, archiveCandidate{
				name:          entry.Name(),
				path:          path,
				newestModTime: newest,
			})
		}
	}
	slices.SortFunc(candidates, func(a, b archiveCandidate) int {
		return cmpString(a.name, b.name)
	})
	return candidates, nil
}

func archiveProjectSummaries(cutoff time.Time) ([]archiveSummary, error) {
	projects, err := loadRegistry()
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}

	var summaries []archiveSummary
	for _, project := range projects {
		candidates, err := archiveCandidates(filepath.Join(llmRoot, project), cutoff)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan %s: %w", project, err)
		}
		if len(candidates) == 0 {
			continue
		}
		summaries = append(summaries, archiveSummary{
			project: project,
			count:   len(candidates),
		})
	}
	return summaries, nil
}

// archiveProject moves stale top-level entries for one registered project into its archive folder.
func archiveProject(project string, cutoff, now time.Time) (archiveResult, error) {
	projectDir := filepath.Join(llmRoot, project)
	result := archiveResult{
		project:    project,
		archiveDir: filepath.Join(projectDir, "archive"),
	}

	candidates, err := archiveCandidates(projectDir, cutoff)
	if err != nil {
		return result, err
	}
	if len(candidates) == 0 {
		return result, nil
	}
	if err := os.MkdirAll(result.archiveDir, 0755); err != nil {
		return result, err
	}

	for _, candidate := range candidates {
		dest, err := uniqueArchivePath(result.archiveDir, candidate.name, now)
		if err != nil {
			return result, err
		}
		if err := os.Rename(candidate.path, dest); err != nil {
			return result, err
		}
		result.moved = append(result.moved, archiveMove{
			destName: filepath.Base(dest),
		})
	}
	return result, nil
}

func archiveSelectedProjects(out io.Writer, projects []string, cutoff, now time.Time) error {
	for _, project := range projects {
		result, err := archiveProject(project, cutoff, now)
		if err != nil {
			return fmt.Errorf("archive %s: %w", project, err)
		}
		if len(result.moved) == 0 {
			fmt.Fprintf(out, "No archive candidates for %s.\n", project)
			continue
		}
		fmt.Fprintf(out, "Archived %d item(s) from %s into %s\n", len(result.moved), project, tildefy(result.archiveDir))
	}
	return nil
}

func printArchiveSummaries(out io.Writer, summaries []archiveSummary, days int) {
	if len(summaries) == 0 {
		fmt.Fprintf(out, "No archive candidates older than %d days.\n", days)
		return
	}
	for _, summary := range summaries {
		fmt.Fprintf(out, "%5d  %s\n", summary.count, summary.project)
	}
}

func fzfPickArchiveProjects(summaries []archiveSummary) ([]string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return nil, fmt.Errorf("fzf not found; install it or pass project as argument")
	}

	items := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		items = append(items, fmt.Sprintf("%d\t%s", summary.count, summary.project))
	}

	fzf := exec.Command("fzf", "--multi", "--prompt", "Archive projects> ")
	fzf.Stdin = strings.NewReader(strings.Join(items, "\n"))
	fzf.Stderr = os.Stderr
	out, err := fzf.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, fmt.Errorf("cancelled")
		}
		return nil, fmt.Errorf("fzf: %w", err)
	}

	var projects []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		_, project, ok := strings.Cut(line, "\t")
		if !ok || project == "" {
			return nil, fmt.Errorf("invalid fzf selection %q", line)
		}
		projects = append(projects, project)
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("no selection")
	}
	return projects, nil
}

func uniqueArchivePath(archiveDir, name string, now time.Time) (string, error) {
	dest := filepath.Join(archiveDir, name)
	if _, err := os.Lstat(dest); err != nil {
		if os.IsNotExist(err) {
			return dest, nil
		}
		return "", err
	}

	stamp := now.Format("20060102-150405")
	for i := 1; ; i++ {
		suffix := fmt.Sprintf("-archived-%s", stamp)
		if i > 1 {
			suffix = fmt.Sprintf("%s-%d", suffix, i)
		}
		candidate := filepath.Join(archiveDir, name+suffix)
		if _, err := os.Lstat(candidate); err != nil {
			if os.IsNotExist(err) {
				return candidate, nil
			}
			return "", err
		}
	}
}

func newestModTime(path string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(path, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
