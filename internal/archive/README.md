# Reversible archives

`Manager` is the archive module's interface. Contextstore owns project identity;
archive never reads the registry or runs Git. The CLI only selects registered
projects, parses exact JSON plans and renders typed results. Filesystem and libc
are local-substitutable dependencies, exercised with disposable real stores.

```
Plan (read-only) -> export exact plan -> Apply
  validate digest/identity -> advisory lock -> full fingerprint preflight
  -> synced intent journal -> atomic no-replace rename -> synced outcome
Recover -> reconcile only recorded paths; no data moves
PlanRestore -> recorded archive origins -> Apply (only absent destinations)
```

Each entry is a top-level tree. Its fingerprint binds the original project root,
relative tree paths, types/modes, size/mtime and streamed SHA-256 file content.
Newest recursive lstat time includes directory mtimes; the age comparison is
strict. Protected names (`archive`, `.dotllm`, `internal`, `runs`, `templates`,
`.projects`), nested registered project stores, symlinks and special files exclude
the whole containing candidate. Individual scans cap at 100,000 nodes, file reads
use 64 KiB buffers, and journal/plan files cap at 32 MiB. Scans accept cancellation;
a cancelled post-move verification is explicitly unverified until recovery.

Plans use content-derived IDs; restore IDs also encode their parent archive ID
because the frozen Plan DTO has no parent field. Warnings aren't authority.
Journals live at `store/.dotllm/archive/ID.json` (0600); payloads live at
`project/archive/ID/items/name`. Exact recorded origins must still agree with
contextstore on apply/recovery/restore. No archive is pruned or automatically
removed, including empty restored archive directories. Loose legacy archive
entries have missing provenance; their origin is never inferred from names.

macOS uses libc `renameatx_np(RENAME_EXCL)` with open parent descriptors and needs
cgo. Linux amd64/arm64 uses `renameat2(RENAME_NOREPLACE)`; its adapter is compiled
here but macOS is the exercised runtime. Unsupported builds fail apply explicitly.
EXDEV and unsupported-filesystem errors have no copy/delete fallback. The platform
adapter tests occupied files, empty directories and dangling symlinks directly;
Manager tests exercise plans, collisions, interrupted intent/outcome journals,
cancellation, retries and byte-for-byte restores through its public interface.

Directory handles, no-follow file opens and fingerprint checks preserve containment
and detect replacements. Advisory locks coordinate archive writers only: editors,
context writers and other noncooperating processes aren't frozen. There is no
snapshot-isolation claim. Post-move changes are reported with actual retained paths;
recovery refuses both-present/both-missing/mismatched states. Per-entry moved
history and terminal success survive subsequent recovery observations; applying
a partial restore persists its source move history before returning bytes. Retrying a completed
operation returns its recorded result, rather than rearchiving restored data.

References: [Go 1.25.7 os.Root](https://pkg.go.dev/os@go1.25.7#Root),
[Darwin rename implementation](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/vfs/vfs_syscalls.c),
[Linux renameat2](https://man7.org/linux/man-pages/man2/rename.2.html).
