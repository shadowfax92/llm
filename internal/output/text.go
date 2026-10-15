package output

import (
	"fmt"
	"io"
)

// WriteText is intentionally plain in the skeleton. The presentation owner can
// replace this renderer without editing command logic or the JSON contract.
func WriteText(w io.Writer, value Envelope, opts Options) error {
	switch data := value.Data.(type) {
	case PathData:
		_, err := fmt.Fprintln(w, data.Path)
		return err
	case StatusData:
		_, err := fmt.Fprintf(w, ".llm: %s\nProject: %s\nAnchor: %s\n", data.Binding.State, data.Binding.Project.Key, data.Binding.Anchor)
		return err
	case LinkData:
		if _, err := fmt.Fprintf(w, ".llm → %s\nChanged: %t\n", data.Binding.Project.Root, data.Changed); err != nil {
			return err
		}
		if data.BackupPath != "" {
			if _, err := fmt.Fprintf(w, "Backup preserved at %s\n", data.BackupPath); err != nil {
				return err
			}
		}
		for _, conflict := range data.Conflicts {
			if _, err := fmt.Fprintf(w, "Preserved %s (%s; destination %s)\n", conflict.PreservedAt, conflict.Reason, conflict.Path); err != nil {
				return err
			}
		}
		return nil
	case ListData:
		if len(data.Projects) == 0 {
			_, err := fmt.Fprintln(w, "No projects. Run dotllm init in a project.")
			return err
		}
		for _, project := range data.Projects {
			marker := "  "
			if project.Key == data.Current {
				marker = "→ "
			}
			if _, err := fmt.Fprintf(w, "%s%s\n", marker, project.Key); err != nil {
				return err
			}
		}
		return nil
	case ConfigData:
		_, err := fmt.Fprintf(w, "Store: %s\nSource: %s\nConfig: %s\nLink: %s\n", data.StoreRoot, data.Source, data.Path, data.Binding.State)
		return err
	case ArchiveData:
		_, err := fmt.Fprintf(w, "%s plan %s: %d entries\n", data.Plan.Operation, data.Plan.ID, len(data.Plan.Entries))
		return err
	case ApplyData:
		_, err := fmt.Fprintf(w, "Plan %s: complete=%t\n", data.Result.PlanID, data.Result.Complete)
		return err
	default:
		return fmt.Errorf("no text renderer for %T", value.Data)
	}
}
