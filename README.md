<div align="center">

# 🧠 LLM

**Persistent `.llm` directories that survive git worktrees.**

*Centralize project context. Symlink everywhere.*

</div>

You use `.llm/` folders to store design docs, PRDs, and AI context for your projects. But they're gitignored — so when a git worktree is deleted, the `.llm/` folder goes with it. LLM fixes this by centralizing `.llm` content in `~/llm/` and symlinking into projects.

- 📂 **Centralized store** — all `.llm` content lives under `~/llm/`, mirroring your project paths
- 🔗 **Symlink management** — `.llm` in each project points to the central store
- 🔄 **Worktree-safe** — worktrees come and go, your context stays
- 📦 **Auto-migration** — existing `.llm/` directories are moved on init
- 🌳 **Multi-checkout** — multiple checkouts can share the same `.llm` store
- 🔍 **Interactive linking** — pick existing projects via [fzf](https://github.com/junegunn/fzf)

---

## Install

Requires Go 1.21+ and optionally [fzf](https://github.com/junegunn/fzf) for interactive project selection.

```sh
git clone https://github.com/shadowfax92/llm.git
cd llm
make install    # builds and copies to $GOBIN
```

## Quick Start

```sh
# 1. Initialize a project (migrates existing .llm if present)
cd ~/code/my-project
llm init

# 2. Check status
llm
# .llm → ~/llm/code/my-project
#   6 files, 3 dirs

# 3. In another checkout, link to the same store
cd ~/code/my-project-worktree
llm link code/my-project

# 4. List all managed projects
llm ls
```

## How It Works

```
~/code/my-project/.llm  →  ~/llm/code/my-project/
~/code/my-project-v2/.llm  →  ~/llm/code/my-project/    (shared)
```

`llm init` takes the current directory's path relative to `$HOME` and creates a matching directory under `~/llm/`. If a real `.llm/` folder already exists, its content is moved to the central store. A symlink replaces it.

`llm link` points `.llm` at an existing project in the store — useful for multiple checkouts of the same repo, or git worktrees that should share context.

A registry at `~/llm/.projects` tracks all managed projects.

## CLI

```sh
llm                    # show .llm status for current directory
llm init               # centralize .llm and create symlink
llm link               # pick existing project via fzf, link to it
llm link <project>     # link directly (e.g. in grove setup commands)
llm ls                 # list all managed projects
```

## Grove Integration

Add `llm link` to your grove repo setup commands so worktrees automatically get the symlink:

```yaml
repos:
  - path: ~/code/my-project
    name: my-project
    setup:
      - llm link code/my-project
```

---

> Built to pair with [grove](https://github.com/shadowfax92/grove) and a workflow where AI agents generate design context in `.llm/` folders.
