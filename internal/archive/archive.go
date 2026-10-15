package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dotllm/internal/contextstore"
)

// Manager owns the plan -> preflight -> durable intent -> rename -> outcome
// lifecycle. Contextstore supplies identities; no Git/registry policy lives here.
// Filesystem dependencies are local-substitutable through real temporary stores.
// Scans stream file bytes, cap tree entries, and observe cancellation. Advisory
// locks serialize archive writers, but cannot freeze unrelated editors.
type Manager struct{ store *contextstore.Store }

func New(store *contextstore.Store) *Manager { return &Manager{store: store} }

// ApplyError preserves progress when cancellation, a collision or I/O interrupts
// an operation. Commands must put it on the error stream, never print success.
type ApplyError struct {
	Result Result
	Err    error
}

func (e *ApplyError) Error() string          { return e.Err.Error() }
func (e *ApplyError) Unwrap() error          { return e.Err }
func (e *ApplyError) PartialResult() *Result { return &e.Result }

func problem(code, message string) error {
	return &contextstore.Error{Code: code, Message: message, NextAction: "Review the exact paths; use archive --recover <id> for recorded partial moves. No data is automatically removed."}
}

// inventory opens the store using identities returned by contextstore, without
// reading its registry or recreating its key/Git rules. Revalidating every root
// also makes a plan from another store fail rather than selecting similar names.
func (m *Manager) inventory(ctx context.Context) (*os.Root, []contextstore.Project, error) {
	projects, err := m.store.Projects(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(projects) == 0 {
		return nil, nil, problem("missing_project", "no registered context projects")
	}
	first := projects[0]
	rootPath := first.Root
	for range strings.Split(first.Key, string(filepath.Separator)) {
		rootPath = filepath.Dir(rootPath)
	}
	for _, p := range projects {
		if !filepath.IsLocal(p.Key) || p.Key == "." || filepath.Clean(p.Key) != p.Key || filepath.Join(rootPath, p.Key) != p.Root {
			return nil, nil, problem("conflict", "contextstore returned inconsistent project roots")
		}
	}
	root, err := os.OpenRoot(rootPath)
	return root, projects, err
}

func (m *Manager) Plan(ctx context.Context, selection Selection) (Plan, error) {
	plan := Plan{Version: 1, Operation: "archive", Cutoff: selection.OlderThan.UTC(), Entries: []Entry{}, Warnings: []string{}}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	if len(selection.Projects) == 0 || selection.OlderThan.IsZero() {
		return plan, problem("selector_required", "select explicit projects and an age cutoff")
	}
	if len(selection.Entries) > 0 && len(selection.Projects) != 1 {
		return plan, problem("invalid_input", "exact entry selection requires one project")
	}
	root, projects, err := m.inventory(ctx)
	if err != nil {
		return plan, err
	}
	defer root.Close()
	selected := map[string]bool{}
	wanted := map[string]bool{}
	for _, name := range selection.Entries {
		if !validEntry(name) {
			return plan, problem("invalid_input", "entry must be an exact top-level name")
		}
		wanted[name] = false
	}
	for _, p := range selection.Projects {
		actual, ok := findProject(projects, p.Key)
		if !ok || actual != p {
			return plan, problem("missing_project", "project is not a current contextstore identity: "+p.Key)
		}
		if selected[p.Key] {
			continue
		}
		selected[p.Key] = true
		dir, err := openDir(root, p.Key, false)
		if err != nil {
			return plan, err
		}
		children, err := readNames(dir)
		dir.Close()
		if err != nil {
			return plan, err
		}
		for _, name := range children {
			if len(wanted) > 0 {
				if _, ok := wanted[name]; !ok {
					continue
				}
				wanted[name] = true
			}
			path := filepath.Join(p.Key, name)
			if reserved(name) || containsProject(path, projects) {
				plan.Warnings = append(plan.Warnings, "excluded protected path "+path)
				continue
			}
			entry, err := scan(ctx, root, path, p.Root+"/"+name, projects)
			if err != nil {
				var skip *excludedError
				if errors.As(err, &skip) {
					plan.Warnings = append(plan.Warnings, skip.Error())
					continue
				}
				return plan, err
			}
			if entry.NewestModTime.Before(plan.Cutoff) {
				entry.ProjectKey = p.Key
				entry.RelativePath = name
				plan.Entries = append(plan.Entries, entry)
				if len(plan.Entries) > maxTreeEntries {
					return plan, problem("unavailable", "plan exceeds 100000 entries")
				}
			}
		}
		warnings, err := legacyWarnings(root, p.Key)
		if err != nil {
			return plan, err
		}
		plan.Warnings = append(plan.Warnings, warnings...)
	}
	for name, found := range wanted {
		if !found {
			return plan, problem("invalid_input", "selected entry does not exist: "+name)
		}
	}
	sortEntries(plan.Entries)
	slices.Sort(plan.Warnings)
	plan.ID = planID(plan, "")
	return plan, nil
}

func (m *Manager) PlanRestore(ctx context.Context, archiveID string, entries []string) (Plan, error) {
	plan := Plan{Version: 1, Operation: "restore", Entries: []Entry{}, Warnings: []string{}}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	if !validArchiveID(archiveID) {
		return plan, problem("invalid_input", "restore requires an exact journaled --archive ID; legacy archives have no recorded origin")
	}
	root, projects, err := m.inventory(ctx)
	if err != nil {
		return plan, err
	}
	defer root.Close()
	j, err := loadJournal(root, archiveID)
	if err != nil {
		if os.IsNotExist(err) {
			return plan, problem("missing_provenance", "archive has no recorded journal/origin; legacy provenance is unknown")
		}
		return plan, err
	}
	if err := validateJournal(j, projects); err != nil {
		return plan, err
	}
	if j.Plan.Operation != "archive" {
		return plan, problem("invalid_input", "restore requires an archive operation")
	}
	result := reconcile(ctx, root, j, projects)
	wanted := map[string]bool{}
	for _, entry := range entries {
		if entry == "" {
			return plan, problem("invalid_input", "empty restore entry")
		}
		wanted[entry] = false
	}
	for name := range wanted {
		if !strings.Contains(name, "/") {
			matches := 0
			for _, e := range j.Plan.Entries {
				if e.RelativePath == name {
					matches++
				}
			}
			if matches > 1 {
				return plan, problem("invalid_input", "ambiguous restore entry; use project/name: "+name)
			}
		}
	}
	for i, entry := range j.Plan.Entries {
		qualified := entry.ProjectKey + "/" + entry.RelativePath
		if len(wanted) > 0 {
			_, plain := wanted[entry.RelativePath]
			_, full := wanted[qualified]
			if !plain && !full {
				continue
			}
			if plain {
				wanted[entry.RelativePath] = true
			}
			if full {
				wanted[qualified] = true
			}
		}
		if result.Outcomes[i].State == "conflict" {
			return plan, problem("conflict", "archive entry has conflicting origin/destination: "+qualified+": "+result.Outcomes[i].Error)
		}
		if result.Outcomes[i].State != "moved" {
			if len(wanted) > 0 {
				return plan, problem("conflict", "selected archive entry is not safely restorable: "+qualified)
			}
			plan.Warnings = append(plan.Warnings, "not restorable: "+qualified+" ("+result.Outcomes[i].State+")")
			continue
		}
		_, dst := movePaths(j.Plan, i)
		if err := absent(root, filepath.Join(entry.ProjectKey, entry.RelativePath)); err != nil {
			return plan, err
		}
		current, err := scan(ctx, root, dst, j.Origins[entry.ProjectKey]+"/"+entry.RelativePath, nil)
		if err != nil {
			return plan, err
		}
		if !sameEntry(entry, current) {
			return plan, problem("stale_plan", "archived bytes changed: "+qualified)
		}
		plan.Entries = append(plan.Entries, entry)
	}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	for name, found := range wanted {
		if !found {
			return plan, problem("invalid_input", "entry not recorded in archive: "+name)
		}
	}
	sortEntries(plan.Entries)
	plan.ID = planID(plan, archiveID)
	return plan, nil
}

func (m *Manager) Apply(ctx context.Context, plan Plan) (Result, error) {
	result := Result{PlanID: plan.ID, Outcomes: []Outcome{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	parent, err := validatePlan(plan)
	if err != nil {
		return result, err
	}
	if !renameSupported {
		return result, problem("unavailable", "atomic no-replace rename requires macOS with cgo, or Linux amd64/arm64")
	}
	root, projects, err := m.inventory(ctx)
	if err != nil {
		return result, err
	}
	defer root.Close()
	unlock, err := lockStore(root)
	if err != nil {
		return result, err
	}
	defer unlock()
	// Identity can change between opening the store and acquiring its lock.
	projects, err = m.store.Projects(ctx)
	if err != nil {
		return result, err
	}
	if prior, err := loadJournal(root, plan.ID); err == nil {
		if err := validateJournal(prior, projects); err != nil {
			return result, err
		}
		if !equalPlan(prior.Plan, plan) {
			return result, problem("stale_plan", "journal does not match reviewed plan")
		}
		return runJournal(ctx, root, prior, projects)
	} else if !os.IsNotExist(err) {
		return result, err
	}
	j := &journal{Version: 1, Plan: plan, Origins: map[string]string{}, Intents: make([]bool, len(plan.Entries)), Moved: make([]bool, len(plan.Entries))}
	var parentJournal *journal
	if parent != "" {
		original, err := loadJournal(root, parent)
		if err != nil {
			return result, err
		}
		if err := validateJournal(original, projects); err != nil {
			return result, err
		}
		observed := reconcile(ctx, root, original, projects)
		for _, entry := range plan.Entries {
			index := slices.Index(original.Plan.Entries, entry)
			if index >= 0 && observed.Outcomes[index].State != "moved" {
				return result, problem("conflict", "restore source is not a verified recorded move")
			}
			if !slices.Contains(original.Plan.Entries, entry) {
				return result, problem("stale_plan", "restore entry is not recorded in the original archive")
			}
		}
		parentJournal = original
	}
	// Full preflight precedes the journal and every data move. Apply does not
	// rediscover candidates: only the immutable reviewed entry set can be moved.
	for i, entry := range plan.Entries {
		p, ok := findProject(projects, entry.ProjectKey)
		if !ok {
			return result, problem("missing_project", "project is no longer registered: "+entry.ProjectKey)
		}
		j.Origins[p.Key] = p.Root
		src, dst := movePaths(plan, i)
		if plan.Operation == "archive" {
			if err := absent(root, filepath.Join(entry.ProjectKey, "archive", plan.ID)); err != nil {
				return result, err
			}
		}
		if plan.Operation == "archive" && containsProject(src, projects) {
			return result, problem("stale_plan", "entry now contains a registered project")
		}
		current, err := scan(ctx, root, src, p.Root+"/"+entry.RelativePath, scanProjects(plan, projects))
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, problem("stale_plan", fmt.Sprintf("cannot preflight %s: %v", src, err))
		}
		if !sameEntry(entry, current) || plan.Operation == "archive" && !current.NewestModTime.Before(plan.Cutoff) {
			return result, problem("stale_plan", "reviewed entry changed: "+src)
		}
		if err := absent(root, dst); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// A partial archive may be restored before its final outcome write. Persist
	// that observed move history BEFORE restoring its payload, so later recovery
	// cannot mistake the restored source for an archive move that never happened.
	if parentJournal != nil {
		if err := saveJournal(root, parentJournal); err != nil {
			return result, err
		}
	}
	if err := saveJournal(root, j); err != nil {
		return result, err
	}
	return runJournal(ctx, root, j, projects)
}

func (m *Manager) Recover(ctx context.Context, id string) (Result, error) {
	result := Result{PlanID: id, Outcomes: []Outcome{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !validID(id) {
		return result, problem("invalid_input", "recover requires an exact operation ID")
	}
	root, projects, err := m.inventory(ctx)
	if err != nil {
		return result, err
	}
	defer root.Close()
	unlock, err := lockStore(root)
	if err != nil {
		return result, err
	}
	defer unlock()
	// Identity can change between opening the store and acquiring its lock.
	projects, err = m.store.Projects(ctx)
	if err != nil {
		return result, err
	}
	j, err := loadJournal(root, id)
	if err != nil {
		return result, err
	}
	if err := validateJournal(j, projects); err != nil {
		return result, err
	}
	result = reconcile(ctx, root, j, projects)
	j.Result = result
	if err := saveJournal(root, j); err != nil {
		return result, &ApplyError{result, err}
	}
	if !result.Complete {
		return result, &ApplyError{result, problem("conflict", "recorded operation is incomplete; inspect outcomes before retrying its exact plan")}
	}
	return result, nil
}

func findProject(projects []contextstore.Project, key string) (contextstore.Project, bool) {
	for _, p := range projects {
		if p.Key == key {
			return p, true
		}
	}
	return contextstore.Project{}, false
}
func reserved(name string) bool {
	switch name {
	case "archive", ".dotllm", "internal", "runs", "templates", ".projects":
		return true
	}
	return false
}
func validEntry(name string) bool {
	return name != "" && name != "." && filepath.Base(name) == name && !reserved(name) && filepath.IsLocal(name)
}
func containsProject(path string, projects []contextstore.Project) bool {
	for _, p := range projects {
		if p.Key == path || strings.HasPrefix(p.Key, path+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
func sortEntries(entries []Entry) {
	slices.SortFunc(entries, func(a, b Entry) int {
		if c := strings.Compare(a.ProjectKey, b.ProjectKey); c != 0 {
			return c
		}
		return strings.Compare(a.RelativePath, b.RelativePath)
	})
}
func sameEntry(a, b Entry) bool {
	return a.Fingerprint == b.Fingerprint && a.SizeBytes == b.SizeBytes && a.NewestModTime.Equal(b.NewestModTime)
}
func scanProjects(plan Plan, projects []contextstore.Project) []contextstore.Project {
	if plan.Operation == "archive" {
		return projects
	}
	return nil
}

// The frozen Plan has no parent field. Restore IDs encode the archive digest
// plus their own content digest, so their source provenance survives JSON export.
func planID(plan Plan, parent string) string {
	canonical := struct {
		Version   int
		Operation string
		Cutoff    time.Time
		Entries   []Entry
		Parent    string
	}{plan.Version, plan.Operation, plan.Cutoff.UTC(), plan.Entries, parent}
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	if parent != "" {
		return "r-" + strings.TrimPrefix(parent, "a-") + "-" + hex.EncodeToString(sum[:])
	}
	return "a-" + hex.EncodeToString(sum[:])
}
func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func validArchiveID(id string) bool {
	return strings.HasPrefix(id, "a-") && validDigest(strings.TrimPrefix(id, "a-"))
}
func validID(id string) bool {
	if validArchiveID(id) {
		return true
	}
	parts := strings.Split(id, "-")
	return len(parts) == 3 && parts[0] == "r" && validDigest(parts[1]) && validDigest(parts[2])
}
func validatePlan(plan Plan) (string, error) {
	parent := ""
	if len(plan.Entries) > maxTreeEntries {
		return "", problem("invalid_input", "plan exceeds 100000 entries")
	}
	if plan.Version != 1 || !validID(plan.ID) {
		return "", problem("invalid_input", "unsupported or invalid plan")
	}
	switch plan.Operation {
	case "archive":
		if !validArchiveID(plan.ID) || plan.Cutoff.IsZero() {
			return "", problem("invalid_input", "invalid archive plan")
		}
	case "restore":
		parts := strings.Split(plan.ID, "-")
		if len(parts) != 3 || parts[0] != "r" {
			return "", problem("invalid_input", "invalid restore provenance")
		}
		parent = "a-" + parts[1]
	default:
		return "", problem("invalid_input", "unknown plan operation")
	}
	sorted := append([]Entry(nil), plan.Entries...)
	sortEntries(sorted)
	for i, e := range plan.Entries {
		if !validEntry(e.RelativePath) || !filepath.IsLocal(e.ProjectKey) || e.ProjectKey == "." || filepath.Clean(e.ProjectKey) != e.ProjectKey || !validDigest(e.Fingerprint) || e.SizeBytes < 0 || e.NewestModTime.IsZero() || e != sorted[i] {
			return "", problem("invalid_input", "invalid or unsorted plan entry")
		}
		if i > 0 && e.ProjectKey == plan.Entries[i-1].ProjectKey && e.RelativePath == plan.Entries[i-1].RelativePath {
			return "", problem("invalid_input", "duplicate plan entry")
		}
	}
	if plan.ID != planID(plan, parent) {
		return "", problem("stale_plan", "plan digest does not match its exact reviewed contents")
	}
	return parent, nil
}
func equalPlan(a, b Plan) bool {
	return a.ID == b.ID && a.Operation == b.Operation && a.Version == b.Version && a.Cutoff.Equal(b.Cutoff) && slices.Equal(a.Entries, b.Entries)
}
