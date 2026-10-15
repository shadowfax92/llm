package archive

import (
	"context"
	"crypto/rand"
	"dotllm/internal/contextstore"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Journal records the immutable reviewed plan and original identities before any
// data move. Intent is synced before rename; outcome follows it. A crash between
// those steps is reconciled by fingerprints, never by guessing new candidates.
// State lives under store/.dotllm; payloads remain in project/archive/ID/items.
type journal struct {
	Version int               `json:"version"`
	Plan    Plan              `json:"plan"`
	Origins map[string]string `json:"origins"`
	Intents []bool            `json:"intents"`
	// Observed moves never become pending again when Recover records a conflict.
	Moved  []bool `json:"moved"`
	Result Result `json:"result"`
	// Terminal success is monotonic: later Recover observations must not rearm
	// an operation after its data has been explicitly restored.
	Completed *Result `json:"completed,omitempty"`
}

const journalDir = ".dotllm/archive"
const maxJournalBytes = 32 << 20

func loadJournal(root *os.Root, id string) (*journal, error) {
	if !validID(id) {
		return nil, problem("invalid_input", "invalid journal ID")
	}
	dir, err := openDir(root, journalDir, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := dir.OpenFile(id+".json", os.O_RDONLY|noFollow, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxJournalBytes {
		return nil, problem("conflict", "invalid or oversized journal")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxJournalBytes+1))
	if err != nil {
		return nil, err
	}
	var j journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, problem("conflict", "unreadable archive journal: "+err.Error())
	}
	if j.Version != 1 || j.Plan.ID != id || len(j.Intents) != len(j.Plan.Entries) {
		return nil, problem("conflict", "invalid archive journal")
	}
	if _, err := validatePlan(j.Plan); err != nil {
		return nil, err
	}
	if len(j.Moved) == 0 {
		j.Moved = make([]bool, len(j.Plan.Entries))
	}
	if len(j.Moved) != len(j.Plan.Entries) {
		return nil, problem("conflict", "invalid journal move history")
	}
	for i, out := range j.Result.Outcomes {
		if i < len(j.Moved) && (out.State == "moved" || out.State == "moved_unverified") {
			j.Moved[i] = true
		}
	}
	if j.Result.Complete && j.Completed == nil {
		copy := j.Result
		j.Completed = &copy
	}
	return &j, nil
}
func validateJournal(j *journal, projects []contextstore.Project) error {
	for _, result := range []*Result{&j.Result, j.Completed} {
		if result == nil || !result.Complete {
			continue
		}
		if result.PlanID != j.Plan.ID || len(result.Outcomes) != len(j.Plan.Entries) {
			return problem("conflict", "completed journal has missing or inconsistent outcomes")
		}
		for i, out := range result.Outcomes {
			e := j.Plan.Entries[i]
			expected := filepath.Join(j.Origins[e.ProjectKey], "archive", j.Plan.ID, "items", e.RelativePath)
			if j.Plan.Operation == "restore" {
				expected = filepath.Join(j.Origins[e.ProjectKey], e.RelativePath)
			}
			if out.State != "moved" || out.ProjectKey != e.ProjectKey || out.RelativePath != e.RelativePath || out.Destination != expected || !j.Intents[i] {
				return problem("conflict", "completed journal has inconsistent move records")
			}
		}
	}
	if j.Completed != nil && !j.Completed.Complete {
		return problem("conflict", "invalid terminal journal result")
	}

	for _, entry := range j.Plan.Entries {
		p, ok := findProject(projects, entry.ProjectKey)
		if !ok || j.Origins[entry.ProjectKey] != p.Root {
			return problem("conflict", "recorded original project changed or is unregistered: "+entry.ProjectKey)
		}
	}
	return nil
}

func saveJournal(root *os.Root, j *journal) error {
	dir, err := openDir(root, journalDir, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	if j.Result.Complete && j.Completed == nil {
		copy := j.Result
		j.Completed = &copy
	}
	if len(j.Moved) == 0 {
		j.Moved = make([]bool, len(j.Plan.Entries))
	}
	for i, out := range j.Result.Outcomes {
		if i < len(j.Moved) && (out.State == "moved" || out.State == "moved_unverified") {
			j.Moved[i] = true
		}
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(data) > maxJournalBytes {
		return problem("unavailable", "journal exceeds 32 MiB limit")
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	temp := ".journal-" + hex.EncodeToString(random[:])
	f, err := dir.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0600)
	if err != nil {
		return err
	}
	defer dir.Remove(temp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	final := j.Plan.ID + ".json"
	if info, err := dir.Lstat(final); err == nil && !info.Mode().IsRegular() {
		return problem("conflict", "journal destination is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := dir.Rename(temp, final); err != nil {
		return err
	}
	return syncDir(dir)
}
func syncDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func lockStore(root *os.Root) (func(), error) {
	dir, err := openDir(root, ".dotllm", true)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := dir.OpenFile("archive.lock", os.O_CREATE|os.O_RDWR|noFollow, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, problem("conflict", "archive lock is not a regular file")
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, problem("conflict", "another archive writer holds the store lock; retry after it finishes")
	}
	return func() { unlockFile(f); f.Close() }, nil
}

func movePaths(plan Plan, index int) (string, string) {
	entry := plan.Entries[index]
	origin := filepath.Join(entry.ProjectKey, entry.RelativePath)
	archiveID := plan.ID
	if plan.Operation == "restore" {
		parts := strings.Split(plan.ID, "-")
		archiveID = "a-" + parts[1]
	}
	archived := filepath.Join(entry.ProjectKey, "archive", archiveID, "items", entry.RelativePath)
	if plan.Operation == "restore" {
		return archived, origin
	}
	return origin, archived
}

// Reconciliation is read-only on data. Exactly one location must contain the
// recorded tree. Both present/absent, a changed tree, or a disappeared recorded
// success is a conflict; pending sources are never silently called successful.
func reconcile(ctx context.Context, root *os.Root, j *journal, projects []contextstore.Project) Result {
	result := Result{PlanID: j.Plan.ID, Outcomes: make([]Outcome, len(j.Plan.Entries)), Complete: true}
	for i, entry := range j.Plan.Entries {
		src, dst := movePaths(j.Plan, i)
		out := Outcome{ProjectKey: entry.ProjectKey, RelativePath: entry.RelativePath, Destination: filepath.Join(root.Name(), dst), State: "conflict"}
		srcInfo, srcErr := root.Lstat(src)
		dstInfo, dstErr := root.Lstat(dst)
		sourceExists := srcErr == nil
		destExists := dstErr == nil
		switch {
		case srcErr != nil && !os.IsNotExist(srcErr):
			out.Error = srcErr.Error()
		case dstErr != nil && !os.IsNotExist(dstErr):
			out.Error = dstErr.Error()
		case sourceExists == destExists:
			out.Error = "both source and destination exist, or both are absent; recorded source: " + filepath.Join(root.Name(), src)
		case sourceExists:
			if !srcInfo.Mode().IsRegular() && !srcInfo.IsDir() {
				out.Error = "source is a symlink or special file"
				break
			}
			if j.Moved[i] || i < len(j.Result.Outcomes) && j.Result.Outcomes[i].State == "moved" {
				out.Error = "previously moved entry is back at its origin; refusing to move it again"
				break
			}
			current, err := scan(ctx, root, src, j.Origins[entry.ProjectKey]+"/"+entry.RelativePath, scanProjects(j.Plan, projects))
			if err != nil {
				out.Error = err.Error() + "; retained at " + filepath.Join(root.Name(), src)
				if ctx.Err() != nil {
					out.State = "pending_unverified"
				}
			} else if !sameEntry(entry, current) {
				out.Error = "source fingerprint changed; retained at " + filepath.Join(root.Name(), src)
			} else {
				out.State = "pending"
			}
		case destExists:
			if !j.Intents[i] {
				out.Error = "destination exists without a recorded move intent"
				break
			}
			// Presence after intent is a durable observation of retained data;
			// once observed, a later restore/replacement can never rearm this move.
			j.Moved[i] = true
			if !dstInfo.Mode().IsRegular() && !dstInfo.IsDir() {
				out.Error = "destination is a symlink or special file"
				break
			}
			current, err := scan(ctx, root, dst, j.Origins[entry.ProjectKey]+"/"+entry.RelativePath, nil)
			if err != nil {
				out.Error = err.Error() + "; moved bytes retained at " + filepath.Join(root.Name(), dst)
				if ctx.Err() != nil {
					out.State = "moved_unverified"
				}
			} else if !sameEntry(entry, current) {
				out.Error = "destination fingerprint changed; moved bytes retained"
			} else {
				out.State = "moved"
			}
		}
		if out.State != "moved" {
			result.Complete = false
		}
		result.Outcomes[i] = out
	}
	return result
}

func runJournal(ctx context.Context, root *os.Root, j *journal, projects []contextstore.Project) (Result, error) {
	// A completed retry returns its recorded outcome. In particular, restoring an
	// archive must not make retrying its old archive plan move those files again.
	if j.Completed != nil {
		return *j.Completed, nil
	}
	if j.Result.Complete {
		return j.Result, nil
	}
	result := reconcile(ctx, root, j, projects)
	finish := func(cause error) (Result, error) {
		j.Result = result
		if err := saveJournal(root, j); err != nil {
			cause = errors.Join(cause, err)
		}
		if cause != nil {
			return result, &ApplyError{result, cause}
		}
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return finish(err)
	}
	if slices.ContainsFunc(result.Outcomes, func(o Outcome) bool { return o.State == "conflict" }) {
		return finish(problem("conflict", "recorded operation has conflicting paths; all bytes retained"))
	}
	for i := range j.Plan.Entries {
		if result.Outcomes[i].State == "moved" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		src, dst := movePaths(j.Plan, i)
		sourceParent, err := openDir(root, filepath.Dir(src), false)
		if err != nil {
			return finish(err)
		}
		destinationParent, err := openDir(root, filepath.Dir(dst), j.Plan.Operation == "archive")
		if err != nil {
			sourceParent.Close()
			return finish(err)
		}
		// Rehash immediately before intent/move as well as in full preflight. There
		// is no snapshot guarantee for noncooperating writers; post-move hashing
		// exposes concurrent changes while keeping their actual bytes intact.
		e := j.Plan.Entries[i]
		current, err := scan(ctx, sourceParent, filepath.Base(src), j.Origins[e.ProjectKey]+"/"+e.RelativePath, nil)
		if err == nil && !sameEntry(e, current) {
			err = problem("stale_plan", "entry changed after preflight: "+src)
		}
		if err == nil {
			err = absent(destinationParent, filepath.Base(dst))
		}
		if err != nil {
			sourceParent.Close()
			destinationParent.Close()
			result.Outcomes[i].State = "conflict"
			result.Outcomes[i].Error = err.Error()
			return finish(err)
		}
		j.Intents[i] = true
		j.Result = result
		if err := saveJournal(root, j); err != nil {
			sourceParent.Close()
			destinationParent.Close()
			return finish(err)
		}
		if err := ctx.Err(); err != nil {
			sourceParent.Close()
			destinationParent.Close()
			return finish(err)
		}
		err = renameNoReplace(sourceParent, filepath.Base(src), destinationParent, filepath.Base(dst))
		if err == nil {
			err = errors.Join(syncDir(sourceParent), syncDir(destinationParent))
		}
		sourceParent.Close()
		destinationParent.Close()
		if err != nil {
			result = reconcile(ctx, root, j, projects)
			return finish(fmt.Errorf("move %s (no copy/delete fallback): %w", src, err))
		}
		single := *j
		single.Plan.Entries = []Entry{j.Plan.Entries[i]}
		single.Intents = []bool{j.Intents[i]}
		single.Moved = []bool{true}
		j.Moved[i] = true
		single.Result.Outcomes = nil
		checked := reconcile(ctx, root, &single, projects)
		result.Outcomes[i] = checked.Outcomes[0]
		result.Complete = slices.IndexFunc(result.Outcomes, func(o Outcome) bool { return o.State != "moved" }) < 0
		j.Result = result
		if err := saveJournal(root, j); err != nil {
			return finish(err)
		}
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		if result.Outcomes[i].State != "moved" {
			return finish(problem("conflict", "moved entry changed concurrently; retained at its recorded path"))
		}
	}
	result.Complete = true
	return finish(nil)
}
