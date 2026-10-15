package cmd

import "github.com/spf13/cobra"

// Presentation owns this hook so help changes do not require command wiring
// edits. Keep the skeleton's unavailable operations visible until implemented.
func configureHelp(root *cobra.Command) {
	root.Long = "Manage central .llm context with dotllm (compatible with llm).\n\nThis skeleton supports explicit cwd init/link and read-only status/list/config.\nCanonical root/path initialization and reviewed archive/restore are unavailable."
}
