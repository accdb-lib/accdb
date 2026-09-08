# accdb - Pure Go Microsoft Access Database Engine

[![Go Reference](https://pkg.go.dev/badge/github.com/accdb-lib/accdb.svg)](https://pkg.go.dev/github.com/accdb-lib/accdb)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

An idiomatic, **Pure Go** storage and query engine for Microsoft Access database files (`.accdb`).

> [!WARNING]
> **Status: Experimental Alpha (WIP)**
>
> Do not use this library as the sole copy of critical production data.
> Currently, creation and writes are strictly restricted to verified **ACE/ACCDB (Access 2007+)** formats.
> Access Encryption, Long Values (OLE/Memo > 4KB), and multi-page B-tree index splits are **not implemented** and explicitly return `ErrNotImplemented`.

---

## Feature Status Matrix

| Feature | Read | Write | Verification | Status / Notes |
|---|:---:|:---:|:---:|---|
| **ACE Database Header** | :white_check_mark: Yes | :white_check_mark: Yes | Unit & Fuzz Tests | Compatible XOR mask & layout |
| **Table Definition (TDEF)** | :white_check_mark: Yes | :white_check_mark: Yes | Unit & Regression Tests | Slotted columns & metadata |
| **Insert Rows** | :white_check_mark: Yes | :white_check_mark: Yes | Unit & Regression Tests | Inlined data pages (< 4080 bytes, pages 0..511) |
| **Update Rows** | :white_check_mark: Yes | :white_check_mark: Yes | Unit & Regression Tests | In-place / slotted updates, index sync |
| **Delete Rows** | :white_check_mark: Yes | :white_check_mark: Yes | Unit & Regression Tests | ACE slotted delete (`0xC000` flags), index sync |
| **Drop Table** | :white_check_mark: Yes | :white_check_mark: Yes | Unit & Regression Tests | Catalog removal & cascade child cleanup |
| **Primary Key & Index Seek** | :white_check_mark: Yes | :white_check_mark: Yes | Unit & Regression Tests | Root leaf-page binary seek |
| **Multi-page Index (B-Tree)** | :x: No | :x: No | Spec Reference | Returns `ErrIndexPageFull` if page full |
| **Long Values (> 4KB OLE/Memo)** | :x: No | :x: No | Spec Reference | Returns `ErrNotImplemented` |
| **Access Encryption** | :x: No | :x: No | Unit Tests | Returns `ErrNotImplemented` |
| **Jet 3 / Jet 4 (.mdb)** | :warning: Partial | :x: No | Spec Reference | `Create()` rejects legacy versions |

---

## 📦 Installation

```bash
go get github.com/accdb-lib/accdb
```

Zero external runtime dependencies. Requires Go 1.21+.

---

## 🚀 Quick Start

### 1. Create a Database & Table

```go
package main

import (
	"fmt"
	"log"

	"github.com/accdb-lib/accdb"
)

func main() {
	// Create an Access 2007+ database (.accdb)
	db, err := accdb.Create("sample.accdb", accdb.JetVersion5)
	if err != nil {
		log.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// Define table schema with Primary Key and Unique Index
	tableDef := accdb.TableDef{
		Name: "Employees",
		Columns: []accdb.ColumnDef{
			{Name: "ID", Type: accdb.ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "FirstName", Type: accdb.ColTypeText, Length: 50, Nullable: false},
			{Name: "LastName", Type: accdb.ColTypeText, Length: 50, Nullable: false},
			{Name: "Email", Type: accdb.ColTypeText, Length: 100, Nullable: true},
			{Name: "Active", Type: accdb.ColTypeBoolean, Nullable: false},
		},
		Indexes: []accdb.IndexDef{
			{Name: "PK_Employees", Columns: []string{"ID"}, Primary: true, Unique: true},
			{Name: "IX_Email", Columns: []string{"Email"}, Unique: true},
		},
	}

	employees, err := db.CreateTable(tableDef)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}
	fmt.Printf("Created table: %s\n", employees.Name)

	// Insert rows
	err = employees.Insert(map[string]interface{}{
		"FirstName": "John",
		"LastName":  "Doe",
		"Email":     "john.doe@example.com",
		"Active":    true,
	})
	if err != nil {
		log.Fatalf("Failed to insert row: %v", err)
	}

	// Atomic save to disk
	if err := db.Save(); err != nil {
		log.Fatalf("Failed to save: %v", err)
	}
	fmt.Println("Saved to sample.accdb successfully!")
}
```

### 2. Fast Seek by Primary Key or Index

```go
db, err := accdb.Open("sample.accdb")
if err != nil {
	log.Fatal(err)
}
defer db.Close()

table, err := db.Table("Employees")
if err != nil {
	log.Fatal(err)
}

// Fast O(log N) lookup by primary key
row, err := table.FindByPrimaryKey(int32(1))
if err != nil {
	log.Fatal(err)
}
fmt.Printf("Found: %s %s\n", row.Get("FirstName"), row.Get("LastName"))

// Seek by named unique index
row, err = table.FindByIndex("IX_Email", "john.doe@example.com")
if err != nil {
	log.Fatal(err)
}
fmt.Printf("Found by email: ID = %v\n", row.Get("ID"))
```

### 3. Querying with Conditions

```go
result, err := db.Select("Employees").
	Where("Active", "=", true).
	OrderByAsc("LastName").
	Execute()
if err != nil {
	log.Fatal(err)
}

for _, row := range result.Rows {
	fmt.Println(row["FirstName"], row["LastName"])
}
```

---

## 🔒 Safety & Resource Limits

`accdb` is built defensively to protect against malformed files and memory exhaustion:
- **Bounds-Checked Binary Reader**: File parsers check every byte offset before indexing slices, preventing crashes on corrupt inputs.
- **Resource Limits via `OpenWithOptions`**:
  ```go
  db, err := accdb.OpenWithOptions("untrusted.accdb", accdb.OpenOptions{
      MaxFileSize: 256 << 20, // Limit memory footprint to 256 MB
      ReadOnly:    true,
  })
  ```
- **Data Page Capacity Limits**:
  ```text
  Maximum addressable data page: 511
  Operations exceeding this limit return ErrUsageMapFull without modifying the database.
  ```
- **Atomic Writes**: Database saves write to a temporary file in the target directory and sync to disk before atomically renaming over the target destination.

---

## 🔗 Attribution & Third-Party Notices

This project references file format specifications, constant definitions, and binary layouts documented by the open-source community:
- [Jackcess](https://github.com/jahlborn/jackcess) (Apache-2.0 License)
- [MDB Tools](https://github.com/mdbtools/mdbtools) (LGPL License)

See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for detailed notices.

---

## 📄 License

Distributed under the [MIT License](LICENSE).

---

## ⚠️ Disclaimer & Trademark Notice

Microsoft, Microsoft Access, and Windows are registered trademarks of Microsoft Corporation in the United States and/or other countries. This project is an independent open-source library and is not affiliated with, endorsed, sponsored, or supported by Microsoft Corporation.
