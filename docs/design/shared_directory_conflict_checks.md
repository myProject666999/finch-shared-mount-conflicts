# Shared directory conflict checks

## Goal

Before a VM starts, every shared-directory declaration must pass a conflict
check. A combination that leaves a directory's read/write permission ambiguous
is rejected **before** startup, rather than discovered after boot when a
directory is unexpectedly unwritable, or a directory that should be read-only
has been modified.

The implementation lives in `pkg/mountconflict` (platform-independent core) and
is wired into Finch through `pkg/config` and the `vm init` / `vm start`
commands.

## What relationships are compared

Two declarations can be related in four ways. All of them are checked on **both
the host path and the guest mount point**.

1. **Identical paths** — the exact same string after normalization.
2. **Ancestor/descendant** — one directory contains the other.
3. **Same entity, different spelling** — paths that look different but point at
   the same file system object because of:
   - symbolic links,
   - trailing separators,
   - `.` and `..` components,
   - repeated separators,
   - case-only differences on a case-insensitive file system.
4. **Different host directories mapped to one guest mount point**.

### Normalization and resolution order

`mountconflict.Resolver` applies, in order:

1. Expand a leading `~` using the host home directory.
2. Lexically normalize with the injected `PathSemantics` (separator handling,
   `.`/`..` collapse, drive-letter casing).
3. Evaluate symbolic links against the real file system, component by component.
4. Fold case on case-insensitive file systems.

Symbolic links are resolved before paths are compared, because a lexical
comparison alone cannot tell that `~/home-alias` and `~/home` are the same
directory.

### Non-existent paths and symlink loops

A path whose tail does not exist is **not** a resolution error: the existing
prefix is resolved and the missing tail is kept in its lexically normalized
form. This lets the checker still compare, for example, two spellings of a
directory that will be created later. Whether a missing directory *blocks
startup* is a separate, explicit `ExistencePolicy` decision (see below), so the
two concerns stay independently testable.

A symbolic link loop (a link visited twice, or a chain longer than 40 links,
matching the kernel's `MAX_NESTED_LINKS`) returns `ErrSymlinkLoop` and fails the
check with a clear error.

### Time-of-check to time-of-use

Resolution inspects the file system at check time. A symlink that is changed
after the check, or a directory replaced between the check and the actual
mount, is not retroactively detected. **The result only describes the file
system at the moment the check ran.** Finch re-runs the check on every
`vm start`, so the window is bounded by a single boot, and the snapshot
records raw paths (not frozen symlink targets) so the next check re-resolves
them against the current file system.

## What the underlying stack actually does

Finch hands mounts to Lima, which turns them into virtiofs (VZ) or reverse-sshfs
(QEMU) mounts inside the Linux guest. The relevant, verified behaviors in
`github.com/lima-vm/lima/v2` are:

- `pkg/limayaml/defaults.go` merges mount entries whose *mount point* matches and
  documents that **"the highest priority Writable setting wins"**. Mounts are
  appended in default, user, override order, so the override file's entry
  overrides the base file's entry for the same mount point.
- `pkg/limayaml/validate.go` has `overlappingMountWarnings`, which only *warns*
  about overlapping host locations or guest mount points ("overlapping mounts
  are not supported and may result in an unexpected error") and otherwise
  accepts them.
- `pkg/cidata/cidata.go` emits each mount's fstab options as `ro` or `rw` based
  solely on that entry's `Writable` flag; the guest then mounts entries in
  order, so a nested mount stacks on top of its parent.

Consequences that drive the decision table:

- **Read-only ancestor + read-write descendant.** The descendant is a separate,
  later mount stacked on top of the read-only parent, so the subtree stays
  writable in practice on virtiofs and reverse-sshfs. This is expressible but
  subtle, and Lima itself only warns about it, so Finch emits a **warning** that
  states the effective behavior rather than silently depending on mount order.
- **Read-write ancestor + read-only descendant.** The later read-only mount
  covers the writable parent for that subtree. This is the standard, supported
  way to make part of a writable share read-only, so it is **allowed with a
  warning** explaining the narrowing.
- **Same entity, conflicting modes.** There is no stable single permission for
  the directory, and which `Writable` wins depends on file ordering. This is a
  hard **reject** so declaration order can never silently change permissions.

## Decision table

`RO` = read-only, `RW` = read-write. "Same entity" covers identical strings and
different spellings that resolve to one object (symlinks, `.`/`..`, trailing
slashes, repeated separators, case folding).

| Relationship | Modes | Verdict | Reason |
|---|---|---|---|
| Same entity | RO + RO | **Dedup** | Redundant; keep the first entry, drop the later. |
| Same entity | RW + RW | **Dedup** | Redundant; keep the first entry, drop the later. |
| Same entity | RO + RW (either order) | **Reject** | No single effective permission; order-dependent outcome. |
| Nested host paths | RO ancestor, RW descendant | **Warn** | Descendant is mounted over the parent and stays writable; surprising but expressible. |
| Nested host paths | RW ancestor, RO descendant | **Warn (allowed)** | Supported way to narrow part of a share to read-only. |
| Nested host paths | RO + RO | **Warn** | Redundant overlap; the narrower entry adds nothing. |
| Nested host paths | RW + RW | **Warn** | Redundant overlap; the narrower entry adds nothing. |
| Different host paths, same guest mount point | any | **Reject** | One mount hides the other; visible content depends on order. |
| Different host paths, nested guest mount points | any | **Allow** | Normal Linux mount stacking. |
| Unrelated paths | any | **Allow** | No interaction. |

Dedup keeps the declaration with the smaller original index and records a
finding so the user is told which entry was redundant. Warnings never block
startup; rejects always do.

### Order independence

Every pair is evaluated from the resolved identities, never from the slice
position, and each rule is symmetric. The table's RO/RW outcomes do not change
when the two declarations are swapped; tests assert this explicitly. Findings
are emitted in the deterministic order of the combined declaration slice
(existence findings first, then pair findings), so the reported item numbers
match the configuration the user wrote and are never the result of sorting
paths. An entry's `Index` is always its position within its own configuration
list, so when a snapshot is combined with new declarations both sides keep
their original (possibly identical) indices; the `Source` field distinguishes
"already in effect" from "newly added", and ordering is carried by slice
position rather than by index.

## Mount point conflicts

The guest side is checked with the same relation logic using Linux guest
semantics (case sensitive, `/` separator). Two **different** host directories
pointed at one guest path are a reject because the later mount would mask the
earlier one. When `mountPoint` is omitted it defaults to the resolved host path,
which is expanded before comparison so a host path and an explicit mount point
that coincide are detected.

## Existing VMs, the snapshot, and new vs. existing entries

`SnapshotStore` persists the raw declarations to
`<lima-config-dir>/shared-directory-snapshot.yaml` only after a successful
start. On the next check:

- Snapshot entries are marked `SourceExisting` ("already in effect").
- Current config entries are marked `SourceNew` ("newly added"). Each side
  keeps its own original index, so a new entry's item number matches its
  position in the user's finch.yaml; the snapshot entries are placed first in
  the combined slice, which determines deterministic finding ordering.
- When a new declaration conflicts with an existing one, the report names both
  sides, their item numbers and their status. The hard error is attributed to
  the **new** entry; the previously working configuration is not retroactively
  declared illegal.
- If no snapshot exists (first run, deleted file, or an upgrade from a version
  without snapshots), every entry is treated as new and a notice explains the
  consequence: conflicts that an older, working configuration already
  accommodated can be over-reported. The check never invents an "existing"
  state it cannot prove.

## Missing host directories

The `ExistencePolicy` is explicit and injectable:

- `ExistenceRejectStartup` (**default**): refuse to start. Silently skipping a
  share can make in-guest programs believe their data is present and write to an
  unexpected local path instead.
- `ExistenceSkipWarn`: drop the declaration and warn, so a VM still boots when
  an external disk is occasionally unplugged.
- `ExistenceAutoCreate`: create the directory before starting.

The default rejects startup because the failure mode of silent skipping (data
written to the wrong place) is more damaging than failing loudly. The trade-off
is that an unplugged external disk now blocks boot until the drive is
reconnected or the entry is removed; the skip-warn policy is available for users
who prefer availability over that guarantee.

## Where the checks run

- `vm init` and every `vm start` call `ValidateSharedMounts` **before** any Lima
  config is regenerated, then persist a fresh snapshot after a successful start.
- When the VM is already running, a conflicting config edit is only logged as a
  warning (the running guest is not interrupted); mounts cannot change under a
  running guest, and the hard check is enforced on the next init/start.
- All conflicts in one configuration are collected in a single pass and reported
  together, in deterministic order, each citing the 1-based item numbers of the
  two declarations and a concrete remediation.

## Platform semantics and injection

Path behavior is modeled by `PathSemantics`, injected rather than read from the
runtime OS, so every platform rule is testable on any machine:

- `POSIXSemantics` treats only `/` as a separator; a backslash is a valid
  file-name character and is never treated as a separator. Case sensitivity is a
  field, allowing a case-insensitive volume to be emulated anywhere.
- `WindowsSemantics` accepts both `/` and `\`, lower-cases drive letters and
  (on case-insensitive NTFS) the whole path, and preserves UNC roots
  (`\\server\share`).
- `GuestSemantics` is always case-sensitive Linux semantics for mount points.

Equivalent spellings are only treated as equal on the platform where they are
actually equivalent. In particular, a POSIX path containing a backslash does not
compare as nested with the same prefix without the backslash; tests pin this so
Windows separator rules cannot leak into POSIX comparisons.

## Known limitations

- **Home expansion.** Only a leading `~` (or `~/...`) is expanded, using the host
  home directory. `~user` style expansion is not supported and the token is left
  untouched.
- **macOS case sensitivity.** HFS+/APFS are case-insensitive by default but can
  be formatted case-sensitive. The default assumes case-insensitive; a
  case-sensitive volume must be supplied explicitly via `POSIXSemantics`.
- **Windows long/short names.** Drive-letter case and separator variants are
  handled, but 8.3 short names (`C:\PROGRA~1`) and `\\?\` long-path prefixes are
  not canonicalized to their long form; two such spellings of one directory may
  not be recognized as the same entity.
- **Windows shares.** Finch on Windows currently mounts the WSL C: drive by
  default and does not wire `additional_directories` into the WSL mount model.
  The Windows `ValidateSharedMounts` is therefore a no-op until user-defined WSL
  shares are introduced; the decision-table machinery is already in place.
- **External file systems.** A network or removable mount that changes identity
  between checks is only caught on the next `vm start`; checks cannot observe a
  swap that happens after validation (see time-of-check above).
- **Non-directory targets.** The existence check confirms presence; confirming
  that a path is actually a directory is delegated to Lima's own validation,
  which already rejects non-directory mount locations.
