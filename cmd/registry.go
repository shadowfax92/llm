package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func registryPath() string {
	return filepath.Join(llmRoot, ".projects")
}

func loadRegistry() ([]string, error) {
	f, err := os.Open(registryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var projects []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		projects = append(projects, line)
	}
	return projects, scanner.Err()
}

func saveRegistry(projects []string) error {
	if err := os.MkdirAll(llmRoot, 0755); err != nil {
		return err
	}
	f, err := os.Create(registryPath())
	if err != nil {
		return err
	}
	defer f.Close()

	for _, p := range projects {
		fmt.Fprintln(f, p)
	}
	return nil
}

func addToRegistry(project string) error {
	projects, err := loadRegistry()
	if err != nil {
		return err
	}
	if slices.Contains(projects, project) {
		return nil
	}
	projects = append(projects, project)
	slices.Sort(projects)
	return saveRegistry(projects)
}
