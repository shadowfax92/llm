package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Help presentation for the dotllm command tree.
//
// NewRoot calls configureHelp once, after every subcommand is registered, so
// help can group and annotate commands without command owners editing this
// file or this file editing their wiring. Command files own Short text and
// flags; this file owns the root description, grouping, examples (only where
// a command has none) and the Grove-style colourised help writer.
//
// The same binary is installed as dotllm and as the compatibility name llm.
// Help mirrors argv[0] so `llm --help` shows `llm` usage, and the alias stays
// silent: no deprecation notice is printed for existing llm scripts.

const (
	primaryName = "dotllm"
	compatName  = "llm"
)

// Each subcommand joins one help section. A command missing from this map
// (for example a newly added one) still appears, under "Additional Commands".
var helpGroups = map[string]string{
	"status": "context", "link": "context", "path": "context", "init": "context",
	"list": "store", "archive": "store", "restore": "store", "config": "store",
}

// Examples use {name} for the invoked program name. Real flags only: every
// example here must run against the current command definitions.
var helpExamples = map[string]string{
	"init": `  {name} init                       # link .llm here, creating or adopting its store
  {name} -C ~/code/foo init --json   # same, for another directory, as JSON`,
	"link": `  {name} link --auto             # infer the store from Git (what grove setup runs)
  {name} link code/shared-notes  # use another project's store; files merge, never overwrite
  {name} list                    # project names to link to`,
	"status": `  {name} status
  {name} status --json | jq -r .data.binding.state   # managed, missing, local, dangling, conflict`,
	"path": `  cd "$({name} path)"     # always one bare absolute path on stdout
  {name} path --json      # path, anchor and whether anything changed`,
	"list": `  {name} list
  {name} list --json | jq -r '.data.projects[].key'`,
	"config": `  {name} config            # effective store root, its source, this checkout's link
  {name} config --path     # config file location only`,
	"archive": `  {name} archive code/foo --days 90                      # preview; nothing moves
  {name} archive code/foo --days 90 --plan-out plan.json  # save that exact plan
  {name} archive --apply --plan plan.json                 # move only what plan.json lists
  {name} archive --list                                   # stale counts per project`,
	"restore": `  {name} restore --archive <id>          # preview putting an archive back
  {name} restore --apply --plan <file>   # restore only what a saved plan lists`,
}

func configureHelp(root *cobra.Command) {
	name := invokedName()
	root.Use = name
	root.Long = strings.ReplaceAll(rootDescription, "{name}", name)
	root.Example = strings.ReplaceAll(`  {name} init                         # link .llm for this project
  {name} status                       # where .llm points (read-only)
  {name} link --auto                  # share the repository's store from a worktree
  {name} path --json                  # store path and anchor for agents
  {name} archive code/foo --days 90   # preview stale entries; nothing moves`, "{name}", name)

	root.AddGroup(&cobra.Group{ID: "context", Title: "Context:"}, &cobra.Group{ID: "store", Title: "Store:"})
	for _, sub := range root.Commands() {
		if group, ok := helpGroups[sub.Name()]; ok {
			sub.GroupID = group
		}
		if example, ok := helpExamples[sub.Name()]; ok && sub.Example == "" {
			sub.Example = strings.ReplaceAll(example, "{name}", name)
		}
	}
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if err := writeHelp(cmd.OutOrStdout(), cmd, name); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
		}
	})
}

const rootDescription = `Shared .llm context for every checkout and Git worktree of a project.

Context lives in one central store (~/llm unless configured) and each checkout
links .llm to it, so notes outlive deleted worktrees. Linking never overwrites:
existing files merge into the store and anything that cannot is kept in place.
Commands are idempotent and never prompt unless you pass --pick on a terminal.

For scripts and agents: --json prints one {"version":1,...} object on stdout.
A failure prints nothing on stdout and one error object on stderr, with exit
2 usage, 3 missing project, 4 conflict, 5 I/O or unavailable, 130 cancelled.`

// invokedName is "llm" when the compatibility name started the process and
// "dotllm" otherwise (including tests and go run).
func invokedName() string {
	if len(os.Args) > 0 && filepath.Base(os.Args[0]) == compatName {
		return compatName
	}
	return primaryName
}

// writeHelp follows Cobra's default help layout (description, then usage)
// and Grove's colouring of it. The compatibility note goes on the root help
// only, and only for dotllm, so llm users see unchanged output.
func writeHelp(w io.Writer, cmd *cobra.Command, name string) error {
	var content strings.Builder
	if description := strings.TrimSpace(cmd.Long); description != "" {
		content.WriteString(description + "\n\n")
	} else if short := strings.TrimSpace(cmd.Short); short != "" {
		content.WriteString(short + "\n\n")
	}
	if cmd.Runnable() || cmd.HasSubCommands() {
		content.WriteString(cmd.UsageString())
	}
	if !cmd.HasParent() && name == primaryName {
		content.WriteString(compatName + " runs this same program; existing llm commands and scripts keep working.\n")
	}
	_, err := io.WriteString(w, colorizeHelp(content.String(), helpColor(cmd)))
	return err
}

// helpColor applies the same policy as command output (cmd/root.go write):
// --json or --color never turns it off, --color always forces it, and auto
// needs a terminal on stdout with NO_COLOR unset and TERM not dumb. Help runs
// before PersistentPreRunE, so it reads the parsed flags directly.
func helpColor(cmd *cobra.Command) bool {
	flags := cmd.Root().PersistentFlags()
	if jsonMode, _ := flags.GetBool("json"); jsonMode {
		return false
	}
	switch mode, _ := flags.GetString("color"); mode {
	case "always":
		return true
	case "never":
		return false
	}
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled || os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := cmd.OutOrStdout().(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Grove's help palette (grove/cmd/style.go): bold cyan section titles, green
// command names, muted pointer to per-command help.
const (
	helpHeading = "\x1b[1;36m"
	helpCommand = "\x1b[32m"
	helpMuted   = "\x1b[2m"
	helpReset   = "\x1b[0m"
)

var helpHeadings = map[string]bool{
	"Usage:": true, "Aliases:": true, "Examples:": true, "Available Commands:": true,
	"Context:": true, "Store:": true, "Additional Commands:": true,
	"Flags:": true, "Global Flags:": true, "Additional help topics:": true,
}

var commandSections = map[string]bool{"Available Commands:": true, "Context:": true, "Store:": true, "Additional Commands:": true}

func colorizeHelp(content string, enabled bool) string {
	if !enabled {
		return content
	}
	section := ""
	lines := strings.SplitAfter(content, "\n")
	for index, line := range lines {
		body, ending := strings.CutSuffix(line, "\n")
		newline := ""
		if ending {
			newline = "\n"
		}
		trimmed := strings.TrimSpace(body)
		switch {
		case helpHeadings[trimmed]:
			section = trimmed
			body = helpHeading + body + helpReset
		case trimmed == "":
			section = ""
		case commandSections[section] && strings.HasPrefix(body, "  "):
			indent := len(body) - len(strings.TrimLeft(body, " "))
			rest := body[indent:]
			if end := strings.IndexAny(rest, " \t"); end > 0 {
				body = body[:indent] + helpCommand + rest[:end] + helpReset + rest[end:]
			}
		case strings.HasPrefix(trimmed, `Use "`):
			body = helpMuted + body + helpReset
		}
		lines[index] = body + newline
	}
	return strings.Join(lines, "")
}
