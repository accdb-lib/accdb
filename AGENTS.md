# Development Guide & Codebase Memory

See [docs/architecture.md](docs/architecture.md) for full architectural specifications, binary layout details, and format constants.

## Key Rules & Conventions
1. **Pure Go**: Standard library only. No CGO, zero external runtime dependencies.
2. **Fail Fast & Explicit**: Unimplemented features must return `ErrNotImplemented` rather than silently succeeding or returning partial mock states.
3. **Defense Against Corrupted Input**: Binary decoders must return errors or safe values instead of panicking on out-of-bounds offsets.
4. **Isolated Tests**: Tests must use `t.TempDir()` rather than creating files in the repository root.
5. **Git Hygiene**: Keep binary `.accdb`, `.mdb`, and OS metadata out of Git tracking.

## Core Storage Invariants
- **Multi-Page Iteration**: `RowIterator` walks all entries in `table.DataPages` and resets `rowIndex = 0` between pages.
- **Row Flags**: Slotted offsets use `rowOffsetMask = 0x1FFF`, `rowOverflowMask = 0x4000`, `rowDeletedMask = 0x8000`. Deleted rows have both masks set (`0xC000`). Overflow target rows have `rowDeletedMask` set so table scans ignore them, while the original slot has `rowOverflowMask` pointing to `[rowNum, page0, page1, page2]`.
- **Atomic Transactions (`withWriteTransaction`)**: Snapshot copy-on-write with `Validate()` check before `replaceState`. Ensures zero database corruption on mutation errors.
- **Index Lifecycle**: All mutations use two-phase index updates (`PrepareInsert`, `PrepareDelete`, `PrepareUpdate` returning `IndexChange`) before any page data is touched.
- **ReadOnly Enforcement**: Guarded by `ensureWritable()` and `PageStore.readOnly`.

