# Architecture Guide: accdb

## 1. Overview & Vision
`accdb` is an idiomatic, **Pure Go** storage and query engine for Microsoft Access database files (`.accdb`).
- **Zero Runtime Dependencies**: Standard Go library only (`database/sql`, `encoding/binary`, `math`, `os`, etc.). No CGO, no Java, no external ODBC drivers required.
- **Design Philosophy**: Pure Go alternative to UCanAccess/Jackcess for modern microservices and cross-platform environments.

---

## 2. Core Architecture

### A. Storage & Page Layer
- **`PageStore` Interface** (`page_store.go`): Abstracts page-level I/O (`ReadPage`, `WritePage`, `AllocatePage`).
- **`MemoryPageStore`**: In-memory page store with defensive copies and strict boundary checks to prevent slice overflows or unexpected mutations.
- **`FormatProfile`** (`format_profile.go`): Encapsulates version-specific constants and byte offsets for `Jet3` (Access 97, 2048-byte pages), `Jet4` (Access 2000-2003, 4096-byte pages), and `ACE12`/`ACE14` (Access 2007+, 4096-byte pages).

### B. Column Codecs (`codec.go`)
Centralized `ColumnCodec` interface for all Access data types:
- Types: `Boolean`, `Byte`, `Int16`, `Int32`, `BigInt`, `Float32`, `Float64`, `Money`, `DateTime`, `Text` (UTF-16LE), `GUID`, `Binary`.
- Methods: `Encode`, `Decode`, `Compare`, `EncodeKey`.
- **Lexicographical Key Encoding (`EncodeKey`)**: Converts values into byte sequences where `bytes.Compare()` matches value ordering (e.g., flipping sign bit for integers and IEEE 754 floats, case-folding for strings).

### C. Index & Primary Key Engine (`index.go`)
- **Indexing & Leaf Pages**: Supports single-column and composite indexes with `PageTypeLeafIndex` (0x04) storage.
- **TDEF Binary Layout**:
  - Index descriptors: 12 bytes per slot at offset 63.
  - Column headers: 25 bytes per column.
  - Column names (UTF-16LE).
  - `IndexData` blocks: 52 bytes per index (4-byte skip, 10 column slots of 3 bytes each, 4-byte usage map ref, 4-byte root page, 4-byte skip, 1-byte flags, 5-byte skip).
  - `IndexImpl` blocks: 28 bytes per logical index (sets `PRIMARY_KEY_INDEX_TYPE = 1` for primary keys).
  - Logical index names.
- **Constraint Enforcement**:
  - `table.Insert()` performs pre-validation against all unique/PK indexes and returns `ErrDuplicateKey` on duplicate values before writing to data pages.
- **Fast Seeks**:
  - `table.FindByPrimaryKey(val)`
  - `table.FindByIndex(name, val)`

### D. Safety & Defensive Guarantees
- **Checked Binary Decoders**: All byte readers verify boundary checks to prevent slice index panics when reading corrupted or fuzz inputs.
- **Resource Exhaustion Defense**: `OpenWithOptions` allows configuring `MaxFileSize` (defaults to 1 GiB).
- **Atomic File Writes**: `db.Save()` and `db.SaveAs()` write to a temporary file and sync to disk before atomically renaming over the destination path.
