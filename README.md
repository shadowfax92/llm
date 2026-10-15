<div align="center">

# dotllm

**One central `.llm` per project, linked into every checkout and Git worktree.**

*Formerly `llm`. Same data, same links, and `llm` still works.*

</div>

You keep design docs, plans and agent context in a gitignored `.llm/` folder. Delete a Git worktree and that folder goes with it. dotllm keeps the real files in one central store, `~/llm/`, mirroring your project paths, and makes `.llm` in each checkout a symlink to it. Worktrees come and go; the context stays.

- **Central store.** Everything lives under `~/llm/<path relative to $HOME>`, listed in `~/llm/.projects`.
- **Shared across checkouts.** Every worktree of a repository can point `.llm` at the same store.
- **Never overwrites.** Adopting or relinking a real `.llm` copies what is missing into the store and leaves everything else where it was, then tells you exactly where.
- **Built for agents.** `--json` gives one versioned object on stdout, errors are structured on stderr, exit codes are stable, and nothing ever prompts.
- **Archive with a review step.** Old entries are previewed first; nothing moves without `--apply` on a saved plan.

---

## Install

Requires Go 1.25.7 or newer.

```sh
git clone https://github.com/shadowfax92/llm.git dotllm
cd dotllm
make install       # installs dotllm, plus llm as a symlink to it, into $(go env GOPATH)/bin
make completions   # optional: fish completions for both names
```

`make install` refuses to replace an `llm` it did not create. If you have the old `llm` binary installed, move it out of the way first, then run `make install` again. Your `~/llm` store and existing `.llm` links don't change.

## Quick start

```console
$ cd ~/code/my-project
$ dotllm init
Linked ~/code/my-project/.llm → ~/llm/code/my-project

$ dotllm status
code/my-project  [linked]
├── anchor  ~/code/my-project/.llm
└── store   ~/llm/code/my-project

$ cd ~/code/my-project/.wt/feature
$ dotllm link --auto            # same store as the main checkout
Linked ~/code/my-project/.wt/feature/.llm → ~/llm/code/my-project
```

The *anchor* is the `.llm` entry inside a checkout. The *store* is the central directory it points to:

```
~/code/my-project/.llm               →  ~/llm/code/my-project/
~/code/my-project/.wt/feature/.llm   →  ~/llm/code/my-project/   (shared)
```

## Commands

| Command | What it does | Writes? |
|---|---|---|
| `dotllm status` | Shows the anchor, the store it points to, and what to do if it's missing, a real directory, or broken. | No |
| `dotllm init` | Links `.llm` here. Creates the store, or moves an existing real `.llm` into it when the store has no copy yet. Running it again is a no-op. | Yes |
| `dotllm link --auto` | Links this Git checkout or worktree to its repository's store. This is what Grove setup runs. | Yes |
| `dotllm link <project>` | Links to an existing project's store, for example `code/shared-notes`. Files from the old target are merged and never overwritten. | Yes |
| `dotllm path` | Ensures context exists, then prints the store path and nothing else: `cd "$(dotllm path)"`. | Yes |
| `dotllm list` (`ls`) | Lists the projects in the store and marks the current one. | No |
| `dotllm config` | Shows the effective store root, where it came from, the config file, and this checkout's link. `config --path` prints only the file path. | No |
| `dotllm archive` | Previews old entries, saves an exact plan with `--plan-out`, and applies it with `--apply --plan`. | Only with `--apply` |
| `dotllm restore` | Previews or applies putting an archive back. | Only with `--apply` |

Every command takes `-C <dir>` (run as if in that directory), `--store <dir>`, `--json`, `--no-input` and `--color auto|always|never`. Run `dotllm <command> --help` for examples.

**Not available in this build yet.** Some commands are defined but report `unavailable` (exit 5) and change nothing: running `dotllm` with no command, `dotllm path`, `init --scope repo` (repository-root anchoring from any subdirectory), `link --pick`, `config edit`, and `archive`/`restore`. Until then, use `dotllm init` in the directory that should hold `.llm`, and `dotllm link --auto` in worktrees.

## When `.llm` already exists

dotllm never deletes or overwrites context. Here is what each case does:

- **A real `.llm` directory and an empty store.** `dotllm init` moves the directory into the store and links it.
- **A real `.llm` directory and a store that already exists.** `dotllm link <project>` renames `.llm` to `.llm.bak`, copies every file the store lacks, and leaves the rest in `.llm.bak`:

  ```console
  $ dotllm link code/foo
  Moved the existing .llm directory aside to ~/code/bar/.llm.bak
  Copied 1 file into ~/llm/code/foo
    + extra.md
  Kept 1 entry where it was (the store already has that path, or it is not a regular file)
    ~ notes.md  kept at ~/code/bar/.llm.bak/notes.md
  Linked ~/code/bar/.llm → ~/llm/code/foo
  next: diff -r ~/code/bar/.llm.bak ~/llm/code/foo   # review what was not copied; nothing was overwritten
  ```

  `.llm.bak` stays until you remove it. If a `.llm.bak` already exists, linking stops and changes nothing.
- **`.llm` already points to another store.** Linking copies the files the new store lacks and leaves the old store untouched.
- **A broken `.llm` symlink.** `dotllm status` says so and prints the exact repair command.

## For agents and scripts

Pass `--json` to get exactly one object on stdout:

```console
$ dotllm status --json
{"version":1,"command":"status","data":{"binding":{"project":{"key":"code/foo","root":"/Users/me/llm/code/foo"},"anchor":"/Users/me/code/foo/.llm","worktree_root":"","canonical_root":"","common_git_dir":"","state":"managed"}},"warnings":[]}
```

- Success: `{"version":1,"command":…,"data":…,"warnings":[…]}` on stdout, with absolute paths.
- Failure: stdout stays empty, and one object goes to stderr: `{"version":1,"error":{"code","message","next_action","retryable"}}`. A partial archive or restore also includes the `partial` result.
- `binding.state` is one of `managed`, `missing`, `local` (a real directory), `dangling` or `conflict`. `status`, `list` and `config` never write. A state is data, so `status` exits 0 when it can report one. A link it can't classify, such as one pointing outside the store, fails with exit 4 and a `next_action`.
- Nothing prompts. A bare `dotllm link` fails with exit 2 and a `next_action`; it never opens a picker. Picking interactively will only ever happen with `--pick` on a terminal.
- Human output is coloured only on a terminal. `NO_COLOR`, `TERM=dumb`, `--color never` and `--json` all turn it off. Path output (`path`, `config --path`) is always a single bare line.

| Exit | Meaning | Error codes |
|---|---|---|
| 0 | Success or nothing to do | — |
| 2 | Bad usage, invalid config, or a selector is needed | `invalid_input`, `invalid_config`, `selector_required` |
| 3 | Missing context or project | `missing_context`, `missing_project` |
| 4 | Conflict, or a plan went stale | `conflict`, `stale_plan` |
| 5 | I/O failure or unavailable operation | `io_error`, `unavailable`, … |
| 130 | Cancelled | `cancelled` |

## Archive and restore

The old `llm archive` moved files as soon as you picked a project. `dotllm archive` splits that into three explicit steps:

```sh
dotllm archive code/foo --days 90                       # 1. preview; nothing moves
dotllm archive code/foo --days 90 --plan-out plan.json  # 2. save that exact plan
dotllm archive --apply --plan plan.json                 # 3. move only what plan.json lists
dotllm restore --archive <plan-id>                      # preview putting it back
```

An apply reports every entry's outcome, destination and any error. If it stops partway, that list shows exactly what moved and what stayed. `dotllm archive --list` still shows per-project counts without moving anything. Archived entries stay under the project's `archive/` directory; nothing is pruned.

This flow belongs to the archive slice. Until that slice lands, `archive` and `restore` exit 5 (`unavailable`) and move nothing.

## Configuration

The store root is resolved in this order: `--store`, then `$DOTLLM_ROOT`, then the config file, then `~/llm`. `dotllm config` shows which one won.

```json
{"version": 1, "store_root": "/absolute/path/to/store"}
```

The config file lives at `$XDG_CONFIG_HOME/dotllm/config.json`, falling back to `~/.config/dotllm/config.json`. A file with an unknown version or a relative path is an error, never silently ignored. Changing the root never moves existing data.

## Migrating from `llm`

| `llm` | `dotllm` | What changed |
|---|---|---|
| `llm` (status) | `dotllm status` | Bare `dotllm` ensures context and prints the store path |
| `llm init` | `dotllm init` | Same behaviour, and it reports what it moved |
| `llm link` (fzf) | `dotllm link <project>` or `--auto` | A bare `link` explains the selectors instead of opening fzf |
| `llm link <project>` | unchanged | Lists what was copied and where kept entries are |
| `llm link --auto` | unchanged | — |
| `llm ls` | `dotllm list` (`ls` still works) | Marks the current project |
| `llm archive` (fzf, then move) | `dotllm archive` → `--plan-out` → `--apply --plan` | **Never moves without `--apply`** |
| `llm archive --list` | unchanged | — |
| — | `dotllm status`, `path`, `config`, `restore` | New |

Nothing to migrate: `~/llm`, `~/llm/.projects` and every existing `.llm` link are used as they are. `llm` is the same binary under its old name, so scripts and Grove hooks that call `llm link --auto` keep working. Its help shows the `llm` name and adds no rename notice.

## Grove integration

Add the link step to your Grove repo setup so new worktrees share the store:

```yaml
repos:
  - path: ~/code/my-project
    name: my-project
    setup:
      - dotllm link --auto
```

---

> Built to pair with [grove](https://github.com/shadowfax92/grove) and a workflow where AI agents write design context into `.llm/` folders.
