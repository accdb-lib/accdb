package accdb

import (
	"bytes"
	"strings"
	"testing"
)

func TestCreateInMemoryAndBytes(t *testing.T) {
	// 1. Create purely in memory (no disk file)
	db, err := CreateInMemory(JetVersion5)
	if err != nil {
		t.Fatalf("CreateInMemory failed: %v", err)
	}
	defer db.Close()

	if !db.IsInMemory() {
		t.Errorf("expected IsInMemory() to be true")
	}
	if db.Path() != "" {
		t.Errorf("expected Path() to be empty, got %q", db.Path())
	}

	// 2. Save() on in-memory db without path should return error
	if err := db.Save(); err == nil {
		t.Errorf("expected Save() on in-memory database to return error")
	} else if !strings.Contains(err.Error(), "cannot save in-memory database without destination path") {
		t.Errorf("unexpected error message: %v", err)
	}

	// 3. Create table and insert data (all in-memory)
	tbl, err := db.CreateTable(TableDef{
		Name: "MemoryTable",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "Title", Type: ColTypeText, Length: 50, Nullable: false},
			{Name: "Notes", Type: ColTypeMemo, Nullable: true},
			{Name: "Payload", Type: ColTypeOLE, Nullable: true},
		},
		Indexes: []IndexDef{
			{Name: "PK_MemoryTable", Columns: []string{"ID"}, Primary: true, Unique: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	longMemo := strings.Repeat("InMemoryLongMemoChunk_", 300) // ~6600 chars multi-chunk
	oleBlob := bytes.Repeat([]byte{0x42, 0x43, 0x44}, 1000)

	err = tbl.Insert(map[string]interface{}{
		"Title":   "Pure Memory Row",
		"Notes":   longMemo,
		"Payload": oleBlob,
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// 4. Export database to byte slice
	rawBytes, err := db.Bytes()
	if err != nil {
		t.Fatalf("db.Bytes() failed: %v", err)
	}
	if len(rawBytes) < 2048 {
		t.Fatalf("expected rawBytes to be valid accdb database, got len %d", len(rawBytes))
	}

	// 5. Test WriteTo (io.WriterTo)
	var buf bytes.Buffer
	n, err := db.WriteTo(&buf)
	if err != nil {
		t.Fatalf("db.WriteTo() failed: %v", err)
	}
	if n != int64(len(rawBytes)) {
		t.Errorf("WriteTo byte count mismatch: %d vs %d", n, len(rawBytes))
	}
	if !bytes.Equal(buf.Bytes(), rawBytes) {
		t.Errorf("WriteTo output differs from Bytes()")
	}

	// 6. Open database directly from bytes (OpenBytes)
	reopened, err := OpenBytes(rawBytes)
	if err != nil {
		t.Fatalf("OpenBytes failed: %v", err)
	}
	defer reopened.Close()

	if !reopened.IsInMemory() {
		t.Errorf("expected reopened from bytes to be in-memory")
	}

	rTbl, err := reopened.Table("MemoryTable")
	if err != nil {
		t.Fatalf("Table('MemoryTable') failed: %v", err)
	}
	if rTbl.RowCount != 1 {
		t.Fatalf("expected RowCount 1, got %d", rTbl.RowCount)
	}

	iter, err := rTbl.Rows()
	if err != nil {
		t.Fatalf("Rows() failed: %v", err)
	}
	if !iter.Next() {
		t.Fatalf("expected 1 row")
	}
	row := iter.Row()
	if row.GetString("Title") != "Pure Memory Row" {
		t.Errorf("Title mismatch: %s", row.GetString("Title"))
	}
	if row.GetString("Notes") != longMemo {
		t.Errorf("Notes length mismatch: got %d, expected %d", len(row.GetString("Notes")), len(longMemo))
	}
	if !bytes.Equal(row.GetBytes("Payload"), oleBlob) {
		t.Errorf("Payload mismatch")
	}

	// 7. Test SaveAs from in-memory database to disk
	tmpPath := t.TempDir() + "/saved_from_memory.accdb"
	if err := reopened.SaveAs(tmpPath); err != nil {
		t.Fatalf("SaveAs failed: %v", err)
	}
	if reopened.IsInMemory() {
		t.Errorf("expected IsInMemory() to be false after SaveAs")
	}
	if reopened.Path() != tmpPath {
		t.Errorf("expected Path() %s, got %s", tmpPath, reopened.Path())
	}

	// Verify disk file can be opened with standard Open()
	diskDB, err := Open(tmpPath)
	if err != nil {
		t.Fatalf("Open from disk failed: %v", err)
	}
	defer diskDB.Close()

	if diskDB.IsInMemory() {
		t.Errorf("diskDB should not be in-memory")
	}
	if !diskDB.HasTable("MemoryTable") {
		t.Errorf("expected diskDB to have MemoryTable")
	}
}

func TestCreateWithEmptyPathIsInMemory(t *testing.T) {
	db, err := Create("", JetVersion5)
	if err != nil {
		t.Fatalf("Create(\"\") failed: %v", err)
	}
	defer db.Close()

	if !db.IsInMemory() {
		t.Errorf("expected Create(\"\") to be in-memory")
	}
}
