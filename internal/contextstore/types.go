// Package contextstore owns context identity, registry persistence and .llm
// publication. Callers pass cwd explicitly; the module never prints or prompts.
package contextstore

type Config struct{ Root, Home string }
type Scope string

const (
	ScopeRepo Scope = "repo"
	ScopeCwd  Scope = "cwd"
)

type Project struct {
	Key  string `json:"key"`
	Root string `json:"root"`
}
type Binding struct {
	Project       Project `json:"project"`
	Anchor        string  `json:"anchor"`
	WorktreeRoot  string  `json:"worktree_root"`
	CanonicalRoot string  `json:"canonical_root"`
	CommonGitDir  string  `json:"common_git_dir"`
	State         string  `json:"state"`
}
type ResolveOptions struct {
	Scope      Scope
	ProjectKey string
}
type Change struct {
	Path        string `json:"path"`
	PreservedAt string `json:"preserved_at"`
	Reason      string `json:"reason"`
}
type LinkResult struct {
	Binding    Binding  `json:"binding"`
	Changed    bool     `json:"changed"`
	BackupPath string   `json:"backup_path"`
	Added      []Change `json:"added"`
	Conflicts  []Change `json:"conflicts"`
	Warnings   []string `json:"warnings"`
}

// Error carries actionable domain failures across the command/presentation seam.
// A partial archive result is attached by its caller, never printed by a module.
type Error struct {
	Code       string
	Message    string
	NextAction string
	Retryable  bool
}

func (e *Error) Error() string { return e.Message }

// Store keeps the filesystem/Git dependencies local-substitutable with real
// temporary directories and Git repos. Open and Resolve are read-only.
type Store struct{ cfg Config }
