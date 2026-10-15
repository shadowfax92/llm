package output

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
)

// Human rendering of command results.
//
// Commands hand WriteText the same typed Envelope they would encode as JSON, so
// people and agents always see the same facts. This file only decides how those
// facts read in a terminal: Grove's palette and ├──/└── trees, ~ for the home
// directory, and a `next:` line wherever the state needs action. It never
// touches the filesystem, Git or the store, and it never decides exit codes.
//
// Constraints callers rely on:
//   - Path output (`dotllm path`, `config --path`) is exactly the path plus a
//     newline: no colour, no ~, so `cd "$(dotllm path)"` keeps working.
//   - Envelope warnings are not rendered here; the command adapter already wrote
//     them to stderr before calling WriteText.
//   - Colour comes only from Options.Color. The command adapter resolves
//     --color, NO_COLOR, TERM=dumb and TTY state before calling.
//   - Output is assembled in memory and written once, so a renderer error never
//     leaves half a report on the stream.

// now is read once per WriteText call; tests replace it so relative ages are
// deterministic. The home directory follows $HOME, as the user's shell does.
var now = time.Now

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
}

// WriteText renders one command result for a person reading a terminal.
// The command adapter may render an archive/restore ApplyData on stderr when
// an apply is partial; the rendering is the same on either stream.
func WriteText(w io.Writer, value Envelope, opts Options) error {
	r := renderer{style: palette{enabled: opts.Color}, home: homeDir(), now: now()}
	switch data := value.Data.(type) {
	case PathData:
		// Grove's validatePathOutput rule: a path that would split into two lines
		// cannot be consumed safely by line-oriented shells; JSON can carry it.
		if strings.ContainsAny(data.Path, "\n\r") {
			return fmt.Errorf("refusing to print a path containing a line break; use --json")
		}
		// Byte-exact: no trimming, so a name ending in a space survives.
		r.out.WriteString(data.Path + "\n")
	case StatusData:
		r.status(data.Binding)
	case LinkData:
		r.link(data)
	case ListData:
		r.list(data)
	case ConfigData:
		r.config(data)
	case ArchiveData:
		r.plan(value.Command, data.Plan)
	case ApplyData:
		r.applied(value.Command, data.Result)
	default:
		return fmt.Errorf("no text renderer for %T", value.Data)
	}
	_, err := io.WriteString(w, r.out.String())
	return err
}

type renderer struct {
	out   strings.Builder
	style palette
	home  string
	now   time.Time
}

func (r *renderer) line(parts ...string) {
	r.out.WriteString(strings.TrimRight(strings.Join(parts, ""), " "))
	r.out.WriteByte('\n')
}

// ---- palette -------------------------------------------------------------

// palette is Grove's outputStyle (grove/cmd/style.go): the same six roles and
// ANSI codes, so dotllm and grove read as one toolset.
type palette struct{ enabled bool }

type role int

const (
	plain role = iota
	heading
	branch
	info
	attention
	danger
	muted
)

var roleCodes = map[role]string{heading: "1;36", branch: "32", info: "36", attention: "33", danger: "31", muted: "2"}

func (p palette) paint(r role, value string) string {
	code, ok := roleCodes[r]
	if !p.enabled || !ok || value == "" {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

// ---- shared layout -------------------------------------------------------

type cell struct {
	text string
	role role
}

// treeRow is one ├── line. Its extra lines hang under the second column
// behind │, so detail such as an error never reads as a sibling entry.
type treeRow struct {
	cells []cell
	extra []cell
}

// tree prints rows under ├──/└── connectors with every column but the
// rightmost padded to its widest plain-text value, including rows that stop
// early, so a trailing marker lines up. Padding is measured before colour is
// applied, because ANSI escapes take no width on screen.
func (r *renderer) tree(indent string, rows []treeRow) {
	columns := 0
	for _, row := range rows {
		columns = max(columns, len(row.cells))
	}
	widths := make([]int, max(columns-1, 0))
	for _, row := range rows {
		for i := range min(len(row.cells), len(widths)) {
			widths[i] = max(widths[i], width(row.cells[i].text))
		}
	}
	hang := ""
	if len(widths) > 0 {
		hang = strings.Repeat(" ", widths[0]+2)
	}
	for index, row := range rows {
		connector, continuation := "├── ", "│   "
		if index == len(rows)-1 {
			connector, continuation = "└── ", "    "
		}
		r.line(indent, r.style.paint(muted, connector), r.cells(row.cells, widths))
		for _, extra := range row.extra {
			r.line(indent, r.style.paint(muted, continuation), hang, r.style.paint(extra.role, extra.text))
		}
	}
}

func (r *renderer) cells(cells []cell, widths []int) string {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(r.style.paint(c.role, c.text))
		if i < len(cells)-1 && i < len(widths) {
			b.WriteString(strings.Repeat(" ", widths[i]-width(c.text)))
		}
	}
	return b.String()
}

func width(s string) int { return utf8.RuneCountInString(s) }

// next is one suggested follow-up. Commands are exact and copyable; the note
// is a muted shell comment, so pasting the whole line is still harmless.
type next struct{ command, note string }

func (r *renderer) next(label string, items []next) {
	commandWidth := 0
	for _, item := range items {
		commandWidth = max(commandWidth, width(item.command))
	}
	for i, item := range items {
		prefix := r.style.paint(muted, label+":") + " "
		if i > 0 {
			prefix = strings.Repeat(" ", width(label)+2)
		}
		text := r.style.paint(branch, item.command)
		if item.note != "" {
			text += strings.Repeat(" ", commandWidth-width(item.command)) + "   " + r.style.paint(muted, "# "+item.note)
		}
		r.line(prefix, text)
	}
}

// tilde shortens paths under $HOME for reading. JSON and path output keep
// absolute paths; this is display only.
func (r *renderer) tilde(path string) string {
	if path == "" || r.home == "" || r.home == "/" {
		return path
	}
	if path == r.home {
		return "~"
	}
	if strings.HasPrefix(path, r.home+string(filepath.Separator)) {
		return "~" + path[len(r.home):]
	}
	return path
}

// shellArg renders a path inside a suggested command. The ~ form is used only
// when it is shell-safe unquoted, because quoting disables ~ expansion.
func (r *renderer) shellArg(path string) string {
	if short := r.tilde(path); shellSafe(short) {
		return short
	}
	return shellWord(path)
}

func shellWord(word string) string {
	if shellSafe(word) {
		return word
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

func shellSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("~/._-+@:,%=", c):
		default:
			return false
		}
	}
	return true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ---- status and config ---------------------------------------------------

// stateLabel maps contextstore Binding.State to the header tag. A state this
// renderer does not know is shown verbatim rather than guessed at.
func stateLabel(state string) cell {
	switch state {
	case "managed":
		return cell{"[linked]", branch}
	case "missing":
		return cell{"[no .llm]", attention}
	case "local":
		return cell{"[local dir]", attention}
	case "dangling":
		return cell{"[dangling]", danger}
	case "external":
		return cell{"[external]", attention}
	case "conflict":
		return cell{"[conflict]", danger}
	}
	return cell{"[" + state + "]", muted}
}

// bindingRows shows the anchor (the .llm entry in a checkout) and the central
// store it uses, plus the Git identity when the resolver reports one.
func (r *renderer) bindingRows(b contextstore.Binding) []treeRow {
	anchor := []cell{{"anchor", muted}, {r.tilde(b.Anchor), plain}}
	switch b.State {
	case "missing":
		anchor = append(anchor, cell{"missing", attention})
	case "local":
		anchor = append(anchor, cell{"real directory, not shared with other checkouts", attention})
	case "dangling":
		anchor = append(anchor, cell{"symlink to a store directory that no longer exists", danger})
	case "external":
		anchor = append(anchor, cell{"symlink to a directory outside the store", attention})
	case "conflict":
		anchor = append(anchor, cell{"not a directory or a managed symlink", danger})
	}
	rows := []treeRow{{cells: anchor}}
	if b.Project.Root != "" {
		store := []cell{{"store", muted}, {r.tilde(b.Project.Root), plain}}
		if b.State != "managed" {
			store = append(store, cell{"not linked", muted})
		}
		rows = append(rows, treeRow{cells: store})
	}
	if b.WorktreeRoot != "" {
		checkout := []cell{{"checkout", muted}, {r.tilde(b.WorktreeRoot), plain}}
		if b.CanonicalRoot != "" && b.CanonicalRoot != b.WorktreeRoot {
			checkout = append(checkout, cell{"worktree", muted})
		}
		rows = append(rows, treeRow{cells: checkout})
	}
	if b.CanonicalRoot != "" && b.CanonicalRoot != b.WorktreeRoot {
		rows = append(rows, treeRow{cells: []cell{{"canonical", muted}, {r.tilde(b.CanonicalRoot), plain}}})
	}
	return rows
}

// bindingNext suggests what resolves a non-linked state, matching how
// contextstore treats each one: init (Ensure) adopts missing and local
// anchors, a dangling one needs an explicit link, and external or conflicting
// entries are never replaced, so they must be moved aside first. Every
// suggestion is non-destructive: init and link never overwrite store files,
// and rm on a symlink removes only the link, never its target.
//
// Commands carry -C <anchor directory> because status may have run with -C
// elsewhere; without it, pasting would act on the shell's current directory.
// Repository scope needs Git, so outside a Git checkout init gets --scope cwd.
func (r *renderer) bindingNext(b contextstore.Binding) []next {
	dotllm := "dotllm"
	if b.Anchor != "" {
		dotllm += " -C " + r.shellArg(filepath.Dir(b.Anchor))
	}
	initCmd := dotllm + " init"
	if b.WorktreeRoot == "" {
		initCmd += " --scope cwd"
	}
	switch b.State {
	case "missing":
		return []next{{initCmd, "create the store if needed and link .llm"}}
	case "local":
		return []next{
			{initCmd, "copy its files into the store; the original stays as .llm.bak"},
			{dotllm + " link <project>", "or merge into another project's store instead"},
		}
	case "dangling":
		return []next{
			{dotllm + " link <project>", "point .llm at an existing store (see dotllm list)"},
			{"rm " + r.shellArg(b.Anchor) + " && " + initCmd, "or drop the broken link and create a fresh store"},
		}
	case "external":
		return []next{{"rm " + r.shellArg(b.Anchor) + " && " + initCmd, "removes only the symlink; copy anything you need from its target"}}
	case "conflict":
		// -n: never clobber an existing .llm.aside. If the move is skipped,
		// the init after it reports the same conflict and changes nothing.
		return []next{{"mv -n " + r.shellArg(b.Anchor) + " " + r.shellArg(b.Anchor+".aside") + " && " + initCmd, "keep it under a new name, then link a store"}}
	}
	return nil
}

func (r *renderer) bindingTitle(b contextstore.Binding) string {
	if b.Project.Key != "" {
		return b.Project.Key
	}
	if b.Anchor != "" {
		return r.tilde(filepath.Dir(b.Anchor))
	}
	return "this directory"
}

func (r *renderer) status(b contextstore.Binding) {
	label := stateLabel(b.State)
	r.line(r.style.paint(heading, r.bindingTitle(b)), "  ", r.style.paint(label.role, label.text))
	r.tree("", r.bindingRows(b))
	if items := r.bindingNext(b); len(items) > 0 {
		r.next("next", items)
	}
}

// configSource names where the effective store root came from, in the words a
// user would type to change it. Precedence is --store > $DOTLLM_ROOT > config
// file > ~/llm (resolved in cmd/root.go, loaded by internal/config).
func configSource(source string) string {
	switch source {
	case "default":
		return "default"
	case "config":
		return "from config file"
	case "DOTLLM_ROOT":
		return "from $DOTLLM_ROOT"
	case "--store":
		return "from --store"
	}
	return source
}

func (r *renderer) config(c ConfigData) {
	r.line(r.style.paint(heading, "dotllm config"))
	fileNote := cell{"not in use", muted}
	if c.Source == "config" {
		fileNote = cell{"in use", info}
	}
	link := r.tilde(c.Binding.Anchor)
	if c.Binding.State == "managed" {
		link += " → " + r.tilde(c.Binding.Project.Root)
	}
	r.tree("", []treeRow{
		{cells: []cell{{"store root", muted}, {r.tilde(c.StoreRoot), plain}, {configSource(c.Source), muted}}},
		{cells: []cell{{"config file", muted}, {r.tilde(c.Path), plain}, fileNote}},
		{cells: []cell{{"this .llm", muted}, {link, plain}, stateLabel(c.Binding.State)}},
	})
	r.line(r.style.paint(muted, "order: --store, $DOTLLM_ROOT, config file, ~/llm"))
	if items := r.bindingNext(c.Binding); len(items) > 0 {
		r.next("next", items)
	}
}

// ---- link and init -------------------------------------------------------

// maxAddedShown bounds the copied-file list. Copied files are safely in the
// store, so a long migration need not flood the terminal; JSON lists them all.
// Entries that were NOT copied are always listed in full, with where they are.
const maxAddedShown = 10

func (r *renderer) link(l LinkData) {
	anchor := r.tilde(l.Binding.Anchor)
	store := l.Binding.Project.Root
	if !l.Changed && l.Binding.State == "managed" && len(l.Added) == 0 && len(l.Conflicts) == 0 && l.BackupPath == "" {
		r.line(r.style.paint(info, "Already linked:"), " ", anchor, " → ", r.tilde(store))
		return
	}
	if l.BackupPath != "" {
		r.line(r.style.paint(info, "Moved"), " the existing .llm directory aside to ", r.tilde(l.BackupPath))
	}
	if len(l.Added) > 0 {
		r.line(r.style.paint(info, "Copied"), " ", plural(len(l.Added), "file", "files"), " into ", r.tilde(store))
		for i, change := range l.Added {
			if i == maxAddedShown {
				r.line(r.style.paint(muted, fmt.Sprintf("  … %d more (--json lists every file)", len(l.Added)-maxAddedShown)))
				break
			}
			r.line("  ", r.style.paint(branch, "+"), " ", r.storeRelative(store, change.Path))
		}
	}
	if len(l.Conflicts) > 0 {
		where := "where they were"
		if len(l.Conflicts) == 1 {
			where = "where it was"
		}
		r.line(r.style.paint(attention, "Kept"), " ", plural(len(l.Conflicts), "entry", "entries"), " ", where, " ", r.style.paint(muted, "(the store already has that path, or it is not a regular file)"))
		names, widest := make([]string, len(l.Conflicts)), 0
		for i, change := range l.Conflicts {
			names[i] = r.storeRelative(store, change.Path)
			widest = max(widest, width(names[i]))
		}
		for i, change := range l.Conflicts {
			pad := strings.Repeat(" ", widest-width(names[i]))
			r.line("  ", r.style.paint(attention, "~"), " ", names[i], pad, "  ", r.style.paint(muted, "kept at "+r.tilde(change.PreservedAt)))
		}
	}
	if l.Changed {
		r.line(r.style.paint(info, "Linked"), " ", anchor, " → ", r.tilde(store))
	} else {
		label := stateLabel(l.Binding.State)
		r.line(anchor, " → ", r.tilde(store), "  ", r.style.paint(label.role, label.text))
	}
	switch source := conflictSource(store, l.Conflicts); {
	case source != "":
		r.next("next", []next{{"diff -r " + r.shellArg(source) + " " + r.shellArg(store), "review what was not copied; nothing was overwritten"}})
	case len(l.Conflicts) > 0:
		r.line(r.style.paint(muted, "note: compare each kept entry above with its store copy before removing anything"))
	case l.BackupPath != "":
		r.line(r.style.paint(muted, "note: every file is in the store; "+r.tilde(l.BackupPath)+" stays until you remove it"))
	}
}

// insideRel reports whether a filepath.Rel result stays inside its base. Only
// a real ".." component leaves it; a name such as "..notes.md" does not.
func insideRel(rel string) bool {
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (r *renderer) storeRelative(store, path string) string {
	if store != "" {
		if rel, err := filepath.Rel(store, path); err == nil && insideRel(rel) {
			return rel
		}
	}
	return r.tilde(path)
}

// conflictSource is the directory the uncopied entries still live in (the
// .llm.bak backup or the previously linked store), when they all share one.
func conflictSource(store string, conflicts []contextstore.Change) string {
	source := ""
	for _, change := range conflicts {
		rel, err := filepath.Rel(store, change.Path)
		if err != nil || !insideRel(rel) {
			return ""
		}
		dir, ok := strings.CutSuffix(change.PreservedAt, string(filepath.Separator)+rel)
		if !ok || (source != "" && dir != source) {
			return ""
		}
		source = dir
	}
	return source
}

// ---- list ----------------------------------------------------------------

func (r *renderer) list(l ListData) {
	if len(l.Projects) == 0 {
		r.line("No projects in the store yet.")
		r.next("next", []next{{"dotllm init", "run inside a project directory"}})
		return
	}
	// ListData carries no store root, but every Root is <store>/<key>, so a
	// shared prefix names it. Mixed prefixes fall back to a plain title.
	title := "Projects"
	if store := storeRoot(l.Projects); store != "" {
		title = r.tilde(store)
	}
	r.line(r.style.paint(heading, title), "  ", r.style.paint(muted, plural(len(l.Projects), "project", "projects")))
	rows := make([]treeRow, 0, len(l.Projects))
	for _, project := range l.Projects {
		row := []cell{{project.Key, branch}}
		if project.Key == l.Current {
			row = append(row, cell{"← here", branch})
		}
		rows = append(rows, treeRow{cells: row})
	}
	r.tree("", rows)
}

func storeRoot(projects []contextstore.Project) string {
	root := ""
	for _, project := range projects {
		candidate, ok := strings.CutSuffix(project.Root, string(filepath.Separator)+project.Key)
		if !ok || (root != "" && candidate != root) {
			return ""
		}
		root = candidate
	}
	return root
}

// ---- archive and restore -------------------------------------------------

// operationName prefers the plan's own operation; the command name covers a
// result, which does not carry one.
func operationName(command, operation string) string {
	if operation != "" {
		return operation
	}
	if command == "restore" {
		return "restore"
	}
	return "archive"
}

// byProject groups items by project key in first-seen order, so a plan or
// result spanning several projects reads one project at a time.
func byProject[T any](items []T, key func(T) string) (keys []string, groups map[string][]T) {
	groups = map[string][]T{}
	for _, item := range items {
		k := key(item)
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], item)
	}
	return keys, groups
}

func projectsPhrase(keys []string) string {
	if len(keys) == 1 {
		return keyOrUnknown(keys[0])
	}
	return plural(len(keys), "project", "projects")
}

func keyOrUnknown(key string) string {
	if key == "" {
		return "(no project)"
	}
	return key
}

// projectTrees prints one tree per project. A single project is a flat tree
// (the summary line already names it); several get a heading each.
func (r *renderer) projectTrees(keys []string, rows func(key string) []treeRow) {
	for _, key := range keys {
		if len(keys) == 1 {
			r.tree("", rows(key))
			continue
		}
		r.line(r.style.paint(heading, keyOrUnknown(key)))
		r.tree("  ", rows(key))
	}
}

// plan renders a dry run. Planning never moves anything, and applying needs
// the saved plan file, so the guidance names both steps instead of implying
// the preview itself can be applied.
func (r *renderer) plan(command string, p archive.Plan) {
	op := operationName(command, p.Operation)
	if len(p.Entries) == 0 {
		switch {
		case op == "restore":
			r.line("Nothing to restore.")
		case !p.Cutoff.IsZero():
			r.line("Nothing to archive: no entries last modified before ", r.when(p.Cutoff), ".")
		default:
			r.line("Nothing to archive.")
		}
		return
	}
	keys, groups := byProject(p.Entries, func(e archive.Entry) string { return e.ProjectKey })
	entries := plural(len(p.Entries), "entry", "entries")
	if op == "restore" {
		r.line(r.style.paint(info, "Would restore"), " ", entries, " into ", projectsPhrase(keys), " ", r.style.paint(muted, "(nothing moved)"))
	} else {
		r.line(r.style.paint(info, "Would archive"), " ", entries, " from ", projectsPhrase(keys), " ", r.style.paint(muted, "(nothing moved)"))
	}
	r.projectTrees(keys, func(key string) []treeRow {
		rows := []treeRow{}
		for _, e := range groups[key] {
			row := []cell{{e.RelativePath, plain}, {formatBytes(e.SizeBytes), muted}}
			if !e.NewestModTime.IsZero() {
				row = append(row, cell{"modified " + r.age(e.NewestModTime), muted})
			}
			rows = append(rows, treeRow{cells: row})
		}
		return rows
	})
	meta := "plan " + p.ID
	if !p.Cutoff.IsZero() {
		meta += " · last modified before " + r.when(p.Cutoff)
	}
	r.line(r.style.paint(muted, meta))
	// Both commands save exact plans. Retain the caller's selectors: the restore
	// plan ID is a new operation ID, not the parent archive's --archive selector.
	if op == "restore" {
		r.next("next", []next{
			{"dotllm restore <same arguments> --plan-out <file>", "save this exact plan for review"},
			{"dotllm restore --apply --plan <file>", "then apply only what that file lists"},
		})
		return
	}
	r.next("next", []next{
		{"dotllm archive <same arguments> --plan-out <file>", "save this exact plan for review"},
		{"dotllm archive --apply --plan <file>", "then apply only what that file lists"},
	})
}

// applied renders an apply result, complete or partial. After a partial apply
// this is the only map of which entries moved and which stayed, so every
// outcome is listed with its state, entry, destination and error; nothing is
// summarised away. States are printed exactly as the archive module reports
// them: the renderer does not assume a fixed set.
func (r *renderer) applied(command string, result archive.Result) {
	op := operationName(command, "")
	keys, groups := byProject(result.Outcomes, func(o archive.Outcome) string { return o.ProjectKey })

	counts, order, errors := map[string]int{}, []string{}, 0
	for _, o := range result.Outcomes {
		state := outcomeState(o)
		if counts[state] == 0 {
			order = append(order, state)
		}
		counts[state]++
		if o.Error != "" {
			errors++
		}
	}
	tally := []string{}
	for _, state := range order {
		tally = append(tally, fmt.Sprintf("%d %s", counts[state], state))
	}
	if len(tally) == 0 {
		tally = append(tally, "no entries")
	}
	if errors > 0 {
		tally = append(tally, plural(errors, "error", "errors"))
	}
	verb := strings.ToUpper(op[:1]) + op[1:]
	title := cell{verb + " complete", info}
	if !result.Complete {
		title = cell{verb + " incomplete", attention}
	}
	where := ""
	if len(keys) > 0 {
		where = " · " + projectsPhrase(keys)
	}
	r.line(r.style.paint(title.role, title.text), r.style.paint(muted, where+" · plan "+result.PlanID+" · "), strings.Join(tally, ", "))

	r.projectTrees(keys, func(key string) []treeRow {
		rows := []treeRow{}
		for _, o := range groups[key] {
			row := treeRow{cells: []cell{{outcomeState(o), muted}, {o.RelativePath, plain}}}
			if o.Error == "" {
				if o.Destination != "" {
					row.cells = append(row.cells, cell{"→ " + r.tilde(o.Destination), muted})
				}
			} else {
				row.cells[0].role = danger
				row.extra = append(row.extra, cell{"error: " + o.Error, danger})
				if o.Destination != "" {
					row.extra = append(row.extra, cell{"destination: " + r.tilde(o.Destination), muted})
				}
			}
			rows = append(rows, row)
		}
		return rows
	})

	// Follow-ups are previews only, so they stay safe whatever state the
	// filesystem was left in; applying anything still needs a saved plan.
	id := shellWord(result.PlanID)
	switch {
	case op == "archive" && result.Complete:
		r.next("undo", []next{{"dotllm restore --archive " + id, "previews the restore; nothing moves until --apply"}})
	case op == "archive":
		items := []next{{"dotllm restore --archive " + id, "preview putting back what this plan moved"}}
		if projects := shellWords(keys); len(projects) > 0 {
			items = append(items, next{"dotllm archive " + strings.Join(projects, " "), "plan again for entries that did not move"})
		}
		r.next("next", items)
	case !result.Complete:
		r.next("next", []next{{"dotllm archive --recover " + id, "reconcile this exact restore; nothing moves"}})
	}
}

func outcomeState(o archive.Outcome) string {
	if o.State == "" {
		return "unknown"
	}
	return o.State
}

func shellWords(words []string) []string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		if word != "" {
			out = append(out, shellWord(word))
		}
	}
	return out
}

// ---- small formatters ----------------------------------------------------

func (r *renderer) when(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04 MST")
}

func (r *renderer) age(t time.Time) string {
	d := r.now.Sub(t)
	switch {
	case d < 0:
		return "in the future"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, suffix := float64(n)/unit, "KB"
	for _, larger := range []string{"MB", "GB", "TB"} {
		if value < unit {
			break
		}
		value, suffix = value/unit, larger
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}
