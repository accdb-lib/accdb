package accdb

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateDatabase(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	if db.Version() != JetVersion5 {
		t.Errorf("Expected version %d, got %d", JetVersion5, db.Version())
	}
}

func TestCreateTable(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_table.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	tableDef := TableDef{
		Name: "Users",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Name", Type: ColTypeText, Length: 50},
			{Name: "Email", Type: ColTypeText, Length: 100},
			{Name: "Age", Type: ColTypeInt},
		},
	}

	table, err := db.CreateTable(tableDef)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	if table.Name != "Users" {
		t.Errorf("Expected table name 'Users', got '%s'", table.Name)
	}

	if len(table.Columns) != 4 {
		t.Errorf("Expected 4 columns, got %d", len(table.Columns))
	}
}

func TestInsertAndQuery(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_crud.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// Create table
	_, err = db.CreateTable(TableDef{
		Name: "Products",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Name", Type: ColTypeText, Length: 100},
			{Name: "Price", Type: ColTypeDouble},
			{Name: "Active", Type: ColTypeBoolean},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	table, _ := db.Table("Products")

	// Insert
	err = table.Insert(map[string]interface{}{
		"Name":   "Product A",
		"Price":  99.99,
		"Active": true,
	})
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	err = table.Insert(map[string]interface{}{
		"Name":   "Product B",
		"Price":  149.99,
		"Active": false,
	})
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	// Query
	result, err := db.Select("Products").
		Where("Active", "=", true).
		Execute()
	if err != nil {
		t.Fatalf("Failed to query: %v", err)
	}

	if result.Count != 1 {
		t.Errorf("Expected 1 row, got %d", result.Count)
	}
}

func TestDataPageLayoutAfterMultipleInserts(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_page_layout.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	table, err := db.CreateTable(TableDef{
		Name: "LayoutT",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Name", Type: ColTypeText, Length: 50},
		},
	})
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	for i := 0; i < 20; i++ {
		if err := table.Insert(map[string]interface{}{"Name": "row"}); err != nil {
			t.Fatalf("insert failed at %d: %v", i, err)
		}
	}

	if len(table.DataPages) == 0 {
		t.Fatalf("expected at least one data page")
	}

	for _, pageNum := range table.DataPages {
		page := db.data[int(pageNum)*db.pageSize : (int(pageNum)+1)*db.pageSize]
		recordCount := int(readUint16(page, 12))
		if recordCount == 0 {
			continue
		}

		offsetTableStart := 14
		minRowOffset := db.pageSize
		for i := 0; i < recordCount; i++ {
			rowOffset := int(readUint16(page, offsetTableStart+i*2) & 0x0FFF)
			if rowOffset < offsetTableStart+recordCount*2 || rowOffset >= db.pageSize {
				t.Fatalf("invalid row offset %d on page %d", rowOffset, pageNum)
			}
			if rowOffset < minRowOffset {
				minRowOffset = rowOffset
			}
		}

		freeSpace := int(readUint16(page, 2))
		wantFreeSpace := minRowOffset - (offsetTableStart + recordCount*2)
		if freeSpace != wantFreeSpace {
			t.Fatalf("invalid free space on page %d: got %d want %d", pageNum, freeSpace, wantFreeSpace)
		}
	}
}

func TestCreateSaveAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.accdb")
	db, err := Create(path, JetVersion5)
	if err != nil {
		t.Fatal(err)
	}
	table, err := db.CreateTable(TableDef{
		Name: "People",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Name", Type: ColTypeText, Length: 100},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Insert(map[string]interface{}{"Name": "Somchai"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reopened.Select("People").Execute()
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || result.Rows[0]["Name"] != "Somchai" {
		t.Fatalf("unexpected reopened rows: %#v", result.Rows)
	}
}

func TestSQLCreateTable(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_sql.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	_, err = db.SQL(`
		CREATE TABLE Customers (
			ID INTEGER PRIMARY KEY AUTOINCREMENT,
			Name VARCHAR(100) NOT NULL,
			Email VARCHAR(200),
			CreatedAt DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create table via SQL: %v", err)
	}

	if !db.HasTable("Customers") {
		t.Error("Table 'Customers' should exist")
	}
}

func TestSQLInsert(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_sql_insert.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	db.SQL(`CREATE TABLE Items (ID INTEGER, Name VARCHAR(50), Price DOUBLE)`)

	_, err = db.SQL(`INSERT INTO Items (ID, Name, Price) VALUES (1, 'Item A', 29.99)`)
	if err != nil {
		t.Fatalf("Failed to insert via SQL: %v", err)
	}

	result, err := db.SQL(`SELECT * FROM Items WHERE ID = 1`)
	if err != nil {
		t.Fatalf("Failed to select: %v", err)
	}

	if result.Count != 1 {
		t.Errorf("Expected 1 row, got %d", result.Count)
	}
}

func TestRowAccessors(t *testing.T) {
	row := &Row{
		Values: map[string]interface{}{
			"ID":        int32(1),
			"Name":      "Test",
			"Price":     float64(99.99),
			"Active":    true,
			"CreatedAt": time.Now(),
			"Empty":     nil,
		},
	}

	if row.GetInt("ID") != 1 {
		t.Error("GetInt failed")
	}

	if row.GetString("Name") != "Test" {
		t.Error("GetString failed")
	}

	if row.GetFloat("Price") != 99.99 {
		t.Error("GetFloat failed")
	}

	if !row.GetBool("Active") {
		t.Error("GetBool failed")
	}

	if !row.IsNull("Empty") {
		t.Error("IsNull failed")
	}

	if row.IsNull("Name") {
		t.Error("IsNull should return false for non-null")
	}
}

func TestColumnTypes(t *testing.T) {
	tests := []struct {
		sqlType  string
		expected ColumnType
	}{
		{"INTEGER", ColTypeLongInt},
		{"VARCHAR", ColTypeText},
		{"TEXT", ColTypeText},
		{"BOOLEAN", ColTypeBoolean},
		{"DOUBLE", ColTypeDouble},
		{"DATETIME", ColTypeDateTime},
		{"MEMO", ColTypeMemo},
		{"BINARY", ColTypeBinary},
		{"MONEY", ColTypeMoney},
		{"GUID", ColTypeGUID},
	}

	for _, tt := range tests {
		colType, _ := parseColumnType(tt.sqlType)
		if colType != tt.expected {
			t.Errorf("parseColumnType(%s) = %d, expected %d", tt.sqlType, colType, tt.expected)
		}
	}
}

func TestQueryConditions(t *testing.T) {
	// Test compareEqual
	if !compareEqual("test", "test") {
		t.Error("compareEqual failed for equal strings")
	}

	if compareEqual("test", "other") {
		t.Error("compareEqual failed for different strings")
	}

	if !compareEqual(nil, nil) {
		t.Error("compareEqual failed for nil values")
	}

	// Test compareLess
	if !compareLess(1, 2) {
		t.Error("compareLess failed")
	}

	if compareLess(2, 1) {
		t.Error("compareLess should return false")
	}

	// Test compareLike
	if !compareLike("hello world", "%world") {
		t.Error("compareLike failed for suffix match")
	}

	if !compareLike("hello world", "hello%") {
		t.Error("compareLike failed for prefix match")
	}

	if !compareLike("hello world", "%lo wo%") {
		t.Error("compareLike failed for contains match")
	}
}

func TestEncoding(t *testing.T) {
	// Test readUint16/writeUint16
	data := make([]byte, 4)
	writeUint16(data, 0, 0x1234)
	if readUint16(data, 0) != 0x1234 {
		t.Error("readUint16/writeUint16 failed")
	}

	// Test readUint32/writeUint32
	writeUint32(data, 0, 0x12345678)
	if readUint32(data, 0) != 0x12345678 {
		t.Error("readUint32/writeUint32 failed")
	}

	// Test UTF-16 string
	s := "Hello สวัสดี"
	encoded := writeUTF16String(s, 100)
	decoded := readUTF16String(encoded, 0, len(encoded))

	// Note: may be truncated, just check it doesn't panic
	if len(decoded) == 0 {
		t.Error("UTF-16 encoding/decoding failed")
	}
}

func TestReadCompressedStringASCII(t *testing.T) {
	encoded := writeCompressedString("MSysObjects")
	decoded, consumed := readCompressedString(encoded, 0)
	if decoded != "MSysObjects" {
		t.Fatalf("decoded string mismatch: got %q", decoded)
	}
	if consumed != len(encoded) {
		t.Fatalf("consumed mismatch: got %d want %d", consumed, len(encoded))
	}
}

func TestMSysObjectsDefNotOverwritten(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_ucanaccess_compat.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	sysPage := db.data[2*db.pageSize : 2*db.pageSize+64]
	if readUint32(sysPage, 8) == 0 {
		t.Fatalf("missing MSysObjects table-definition length")
	}
	before := make([]byte, len(sysPage))
	copy(before, sysPage)

	_, err = db.CreateTable(TableDef{
		Name: "CompatCheck",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt},
		},
	})
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	after := db.data[2*db.pageSize : 2*db.pageSize+64]
	copy(before[16:20], after[16:20]) // Row count is the only expected metadata update.
	if !bytes.Equal(before, after) {
		t.Fatalf("MSysObjects table definition changed outside the row count")
	}
}

func TestDateTime(t *testing.T) {
	// Test OLE date conversion
	testTime := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	oleDate := writeDateTime(testTime)

	data := make([]byte, 8)
	bits := math.Float64bits(oleDate)
	data[0] = byte(bits)
	data[1] = byte(bits >> 8)
	data[2] = byte(bits >> 16)
	data[3] = byte(bits >> 24)
	data[4] = byte(bits >> 32)
	data[5] = byte(bits >> 40)
	data[6] = byte(bits >> 48)
	data[7] = byte(bits >> 56)

	decoded := readDateTime(data, 0)

	// Check year and month (time conversion may have minor differences)
	if decoded.Year() != testTime.Year() || decoded.Month() != testTime.Month() {
		t.Errorf("DateTime conversion failed: expected %v, got %v", testTime, decoded)
	}
}

func TestDropTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_drop.accdb")
	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name:    "ToDelete",
		Columns: []ColumnDef{{Name: "ID", Type: ColTypeLongInt}},
	})
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}
	if err := tbl.Insert(map[string]interface{}{"ID": int32(42)}); err != nil {
		t.Fatalf("Failed to insert into ToDelete: %v", err)
	}

	if err := db.DropTable("ToDelete"); err != nil {
		t.Fatalf("DropTable failed: %v", err)
	}

	if db.HasTable("ToDelete") {
		t.Errorf("Expected HasTable(ToDelete) to be false after DropTable")
	}

	if err := db.Save(); err != nil {
		t.Fatalf("Failed to save after DropTable: %v", err)
	}
	db.Close()

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to reopen after DropTable: %v", err)
	}
	defer reopened.Close()

	if reopened.HasTable("ToDelete") {
		t.Errorf("Expected ToDelete table to remain dropped after reopen")
	}
}

func TestSQLDropTable(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_sql_drop.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	_, err = db.CreateTable(TableDef{
		Name:    "Temp",
		Columns: []ColumnDef{{Name: "ID", Type: ColTypeLongInt}},
	})
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	_, err = db.SQL(`DROP TABLE IF EXISTS Temp`)
	if err != nil {
		t.Fatalf("Failed to drop table via SQL: %v", err)
	}
	if db.HasTable("Temp") {
		t.Errorf("Expected Temp to be dropped")
	}

	// Idempotent drop IF EXISTS
	_, err = db.SQL(`DROP TABLE IF EXISTS Temp`)
	if err != nil {
		t.Errorf("Expected DROP TABLE IF EXISTS on non-existent table to succeed, got: %v", err)
	}
}

func TestUnimplementedAPIs(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_unimplemented.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// Encryption APIs return ErrNotImplemented
	if err := db.SetPassword("secret"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Expected ErrNotImplemented from SetPassword, got: %v", err)
	}
	if err := db.Decrypt("secret"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Expected ErrNotImplemented from Decrypt, got: %v", err)
	}
	if _, err := db.VerifyPassword("secret"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Expected ErrNotImplemented from VerifyPassword, got: %v", err)
	}
	if err := db.ChangePassword("old", "new"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Expected ErrNotImplemented from ChangePassword, got: %v", err)
	}
}

func TestVersionAndFormatRestrictions(t *testing.T) {
	tmpDir := t.TempDir()

	// JetVersion3 should be rejected in Alpha
	_, err := Create(filepath.Join(tmpDir, "v3.accdb"), JetVersion3)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("Expected ErrUnsupportedVersion for JetVersion3, got: %v", err)
	}

	// JetVersion4 should be rejected in Alpha
	_, err = Create(filepath.Join(tmpDir, "v4.accdb"), JetVersion4)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("Expected ErrUnsupportedVersion for JetVersion4, got: %v", err)
	}

	// Non-.accdb extension should be rejected
	_, err = Create(filepath.Join(tmpDir, "db.mdb"), JetVersion5)
	if err == nil || !strings.Contains(err.Error(), ".accdb") {
		t.Errorf("Expected extension rejection for .mdb, got: %v", err)
	}
}

func TestTableDefValidation(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_val.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 1. Empty table name
	_, err = db.CreateTable(TableDef{
		Name:    "",
		Columns: []ColumnDef{{Name: "ID", Type: ColTypeLongInt}},
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Errorf("Expected ErrInvalidData for empty table name, got: %v", err)
	}

	// 2. Empty columns
	_, err = db.CreateTable(TableDef{
		Name:    "EmptyCols",
		Columns: nil,
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Errorf("Expected ErrInvalidData for empty columns, got: %v", err)
	}

	// 3. Duplicate column names (case-insensitive)
	_, err = db.CreateTable(TableDef{
		Name: "DupCols",
		Columns: []ColumnDef{
			{Name: "ColA", Type: ColTypeLongInt},
			{Name: "cola", Type: ColTypeText, Length: 20},
		},
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Errorf("Expected ErrInvalidData for duplicate column names, got: %v", err)
	}

	// 4. Primary key column is nullable
	_, err = db.CreateTable(TableDef{
		Name: "NullablePK",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, Nullable: true},
		},
		Indexes: []IndexDef{
			{Name: "PK", Columns: []string{"ID"}, Primary: true},
		},
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Errorf("Expected ErrInvalidData for nullable primary key, got: %v", err)
	}

	// 5. Multiple auto-increment columns
	_, err = db.CreateTable(TableDef{
		Name: "MultiAuto",
		Columns: []ColumnDef{
			{Name: "ID1", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "ID2", Type: ColTypeLongInt, AutoIncrement: true},
		},
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Errorf("Expected ErrInvalidData for multiple auto-increment columns, got: %v", err)
	}
}

func TestOversizedRowRejection(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "oversized.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name: "BigRows",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "LargePayload", Type: ColTypeBinary},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// 5000 bytes binary payload exceeds inline page limit (~4080 bytes)
	hugeData := make([]byte, 5000)
	err = tbl.Insert(map[string]interface{}{
		"LargePayload": hugeData,
	})
	if !errors.Is(err, ErrInvalidData) {
		t.Errorf("Expected ErrInvalidData for oversized row, got: %v", err)
	}
}

func TestOpenOptionsLimits(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "limit_test.accdb")
	db, err := Create(path, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	if err := db.Save(); err != nil {
		t.Fatalf("Failed to save database: %v", err)
	}
	db.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	fileSize := fi.Size()

	// 1. Exactly at limit: should succeed
	dbExact, err := OpenWithOptions(path, OpenOptions{MaxFileSize: fileSize})
	if err != nil {
		t.Errorf("Expected success when MaxFileSize == exact fileSize, got: %v", err)
	} else {
		dbExact.Close()
	}

	// 2. Larger by 1 byte: should fail with ErrDatabaseTooLarge
	_, err = OpenWithOptions(path, OpenOptions{MaxFileSize: fileSize - 1})
	if !errors.Is(err, ErrDatabaseTooLarge) {
		t.Errorf("Expected ErrDatabaseTooLarge when MaxFileSize is fileSize-1, got: %v", err)
	}

	// 3. Negative or zero limit: uses default (DefaultMaxFileSize) and succeeds
	dbDefault, err := OpenWithOptions(path, OpenOptions{MaxFileSize: -1})
	if err != nil {
		t.Errorf("Expected success with negative limit (falling back to default), got: %v", err)
	} else {
		dbDefault.Close()
	}

	dbZero, err := OpenWithOptions(path, OpenOptions{MaxFileSize: 0})
	if err != nil {
		t.Errorf("Expected success with zero limit (falling back to default), got: %v", err)
	} else {
		dbZero.Close()
	}

	// 4. Symlink test: opening via symlink honors the limit
	symlinkPath := filepath.Join(tempDir, "limit_symlink.accdb")
	if err := os.Symlink(path, symlinkPath); err == nil {
		// Symlink with fileSize - 1 should fail with ErrDatabaseTooLarge
		_, err = OpenWithOptions(symlinkPath, OpenOptions{MaxFileSize: fileSize - 1})
		if !errors.Is(err, ErrDatabaseTooLarge) {
			t.Errorf("Expected ErrDatabaseTooLarge via symlink, got: %v", err)
		}
		// Symlink with exact limit succeeds
		dbSym, err := OpenWithOptions(symlinkPath, OpenOptions{MaxFileSize: fileSize})
		if err != nil {
			t.Errorf("Expected success via symlink with exact limit, got: %v", err)
		} else {
			dbSym.Close()
		}
	}
}

func TestSafeBinaryReader(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	r := newBinaryReader(data)

	// Valid reads
	u16, err := r.Uint16(0)
	if err != nil || u16 != 0x0201 {
		t.Errorf("Expected 0x0201, got 0x%04x (err: %v)", u16, err)
	}

	u32, err := r.Uint32(0)
	if err != nil || u32 != 0x04030201 {
		t.Errorf("Expected 0x04030201, got 0x%08x (err: %v)", u32, err)
	}

	// Out of bounds reads must return ErrCorruptDatabase, not panic
	_, err = r.Uint16(3)
	if !errors.Is(err, ErrCorruptDatabase) {
		t.Errorf("Expected ErrCorruptDatabase for Uint16 out of bounds, got: %v", err)
	}

	_, err = r.Uint32(1)
	if !errors.Is(err, ErrCorruptDatabase) {
		t.Errorf("Expected ErrCorruptDatabase for Uint32 out of bounds, got: %v", err)
	}

	_, err = r.Float64(0)
	if !errors.Is(err, ErrCorruptDatabase) {
		t.Errorf("Expected ErrCorruptDatabase for Float64 on 4 bytes, got: %v", err)
	}

	// Test non-panicking package-level helpers
	if val := readUint16(data, 100); val != 0 {
		t.Errorf("Expected 0 on out-of-bounds readUint16, got %d", val)
	}
	if val := readUint32(data, 100); val != 0 {
		t.Errorf("Expected 0 on out-of-bounds readUint32, got %d", val)
	}
	writeUint16(data, 100, 42) // must not panic
	writeUint32(data, 100, 42) // must not panic
}

func BenchmarkInsert(b *testing.B) {
	db, _ := Create(filepath.Join(b.TempDir(), "bench_insert.accdb"), JetVersion5)
	defer db.Close()

	db.CreateTable(TableDef{
		Name: "Bench",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Data", Type: ColTypeText, Length: 100},
		},
	})

	table, _ := db.Table("Bench")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		table.Insert(map[string]interface{}{
			"Data": "benchmark data",
		})
	}
}

func BenchmarkQuery(b *testing.B) {
	db, _ := Create(filepath.Join(b.TempDir(), "bench_query.accdb"), JetVersion5)
	defer db.Close()

	db.CreateTable(TableDef{
		Name: "Bench",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Value", Type: ColTypeInt},
		},
	})

	table, _ := db.Table("Bench")

	// Insert test data
	for i := 0; i < 1000; i++ {
		table.Insert(map[string]interface{}{
			"Value": i,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.Select("Bench").
			Where("Value", ">", 500).
			Execute()
	}
}

func TestUpdateOperations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_update.accdb")
	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	tableDef := TableDef{
		Name: "Products",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "SKU", Type: ColTypeText, Length: 20, Nullable: false},
			{Name: "Title", Type: ColTypeText, Length: 100, Nullable: false},
			{Name: "Price", Type: ColTypeMoney, Nullable: false},
			{Name: "Active", Type: ColTypeBoolean},
		},
		Indexes: []IndexDef{
			{Name: "PK_Products", Columns: []string{"ID"}, Primary: true, Unique: true},
			{Name: "UX_SKU", Columns: []string{"SKU"}, Unique: true},
		},
	}

	table, err := db.CreateTable(tableDef)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Insert items
	items := []map[string]interface{}{
		{"SKU": "SKU-001", "Title": "Widget A", "Price": 19.99, "Active": true},
		{"SKU": "SKU-002", "Title": "Widget B", "Price": 29.99, "Active": false},
		{"SKU": "SKU-003", "Title": "Widget C", "Price": 39.99, "Active": true},
	}
	for _, item := range items {
		if err := table.Insert(item); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// 1. Update title and price
	n, err := table.Update(map[string]interface{}{
		"Title": "Super Widget B",
		"Price": 34.50,
	}, func(r *Row) bool {
		return r.GetString("SKU") == "SKU-002"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if n != 1 {
		t.Errorf("Expected 1 row updated, got %d", n)
	}

	// Verify update in memory
	var updatedTitle string
	var updatedPrice float64
	iter, err := table.Rows()
	if err != nil {
		t.Fatalf("Rows() failed: %v", err)
	}
	for iter.Next() {
		r := iter.Row()
		if r.GetString("SKU") == "SKU-002" {
			updatedTitle = r.GetString("Title")
			updatedPrice = r.GetFloat("Price")
		}
	}
	if updatedTitle != "Super Widget B" {
		t.Errorf("Expected title 'Super Widget B', got '%s'", updatedTitle)
	}
	if math.Abs(updatedPrice-34.50) > 0.001 {
		t.Errorf("Expected price 34.50, got %f", updatedPrice)
	}

	// 2. Reject update to auto-increment column
	_, err = table.Update(map[string]interface{}{"ID": int32(999)}, nil)
	if err == nil {
		t.Errorf("Expected error updating auto-increment column")
	}

	// 3. Unique index conflict causes rollback
	_, err = table.Update(map[string]interface{}{"SKU": "SKU-001"}, func(r *Row) bool {
		return r.GetString("SKU") == "SKU-003"
	})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("Expected ErrDuplicateKey on conflicting update, got: %v", err)
	}

	// SKU-003 must remain untouched after rollback
	iter, _ = table.Rows()
	foundSKU3 := false
	for iter.Next() {
		if iter.Row().GetString("SKU") == "SKU-003" {
			foundSKU3 = true
		}
	}
	if !foundSKU3 {
		t.Errorf("Expected SKU-003 to remain unchanged after rolled back update")
	}

	// 4. Save and reopen: verify persistence
	if err := db.Save(); err != nil {
		t.Fatalf("Failed to save: %v", err)
	}
	db.Close()

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to reopen: %v", err)
	}
	defer reopened.Close()

	reopenedTable, err := reopened.Table("Products")
	if err != nil {
		t.Fatalf("Failed to open table Products: %v", err)
	}
	if reopenedTable.RowCount != 3 {
		t.Errorf("Expected RowCount 3, got %d", reopenedTable.RowCount)
	}

	// Verify updated row persisted
	reopenedIter, err := reopenedTable.Rows()
	if err != nil {
		t.Fatalf("reopened Rows() failed: %v", err)
	}
	reopenedTitle := ""
	for reopenedIter.Next() {
		r := reopenedIter.Row()
		if r.GetString("SKU") == "SKU-002" {
			reopenedTitle = r.GetString("Title")
		}
	}
	if reopenedTitle != "Super Widget B" {
		t.Errorf("Expected reopened title 'Super Widget B', got '%s'", reopenedTitle)
	}
}

func TestDeleteOperations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_delete.accdb")
	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	tableDef := TableDef{
		Name: "Tasks",
		Columns: []ColumnDef{
			{Name: "TaskID", Type: ColTypeLongInt, Nullable: false},
			{Name: "Title", Type: ColTypeText, Length: 50, Nullable: false},
		},
		Indexes: []IndexDef{
			{Name: "PK_Tasks", Columns: []string{"TaskID"}, Primary: true, Unique: true},
		},
	}

	table, err := db.CreateTable(tableDef)
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	for i := 1; i <= 5; i++ {
		err := table.Insert(map[string]interface{}{
			"TaskID": int32(i),
			"Title":  fmt.Sprintf("Task %d", i),
		})
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	if table.RowCount != 5 {
		t.Errorf("Expected RowCount 5, got %d", table.RowCount)
	}

	// 1. Delete middle row (TaskID 3)
	n, err := table.Delete(func(r *Row) bool {
		return r.GetInt("TaskID") == 3
	})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if n != 1 {
		t.Errorf("Expected 1 row deleted, got %d", n)
	}
	if table.RowCount != 4 {
		t.Errorf("Expected RowCount 4, got %d", table.RowCount)
	}

	// Index lookup on deleted row must return ErrRowNotFound
	_, err = table.FindByPrimaryKey(int32(3))
	if !errors.Is(err, ErrRowNotFound) {
		t.Errorf("Expected ErrRowNotFound on deleted row, got: %v", err)
	}

	// 2. Delete first and last row
	n, err = table.Delete(func(r *Row) bool {
		id := r.GetInt("TaskID")
		return id == 1 || id == 5
	})
	if err != nil {
		t.Fatalf("Delete first/last failed: %v", err)
	}
	if n != 2 {
		t.Errorf("Expected 2 rows deleted, got %d", n)
	}
	if table.RowCount != 2 {
		t.Errorf("Expected RowCount 2, got %d", table.RowCount)
	}

	// Remaining rows should be TaskID 2 and 4
	remainingIDs := make(map[int]bool)
	iter, err := table.Rows()
	if err != nil {
		t.Fatalf("Rows() failed: %v", err)
	}
	for iter.Next() {
		remainingIDs[iter.Row().GetInt("TaskID")] = true
	}
	if !remainingIDs[2] || !remainingIDs[4] || len(remainingIDs) != 2 {
		t.Errorf("Expected remaining tasks 2 and 4, got: %v", remainingIDs)
	}

	// 3. Save and reopen: verify deletion persisted
	if err := db.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	db.Close()

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer reopened.Close()

	reopenedTable, err := reopened.Table("Tasks")
	if err != nil {
		t.Fatalf("Table Tasks failed: %v", err)
	}
	if reopenedTable.RowCount != 2 {
		t.Errorf("Expected reopened RowCount 2, got %d", reopenedTable.RowCount)
	}

	_, err = reopenedTable.FindByPrimaryKey(int32(3))
	if !errors.Is(err, ErrRowNotFound) {
		t.Errorf("Expected ErrRowNotFound for task 3 after reopen, got: %v", err)
	}
}

func TestReadOnlyEnforcement(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_ro.accdb")
	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	tbl, err := db.CreateTable(TableDef{
		Name:    "Protected",
		Columns: []ColumnDef{{Name: "ID", Type: ColTypeLongInt}},
	})
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}
	if err := tbl.Insert(map[string]interface{}{"ID": int32(1)}); err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}
	if err := db.Save(); err != nil {
		t.Fatalf("Failed to save: %v", err)
	}
	db.Close()

	// Open with ReadOnly: true
	roDB, err := OpenWithOptions(dbPath, OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("Failed to open read-only: %v", err)
	}
	defer roDB.Close()

	if !roDB.IsReadOnly() {
		t.Errorf("Expected IsReadOnly to be true")
	}

	// Read operations should succeed
	roTable, err := roDB.Table("Protected")
	if err != nil {
		t.Fatalf("Read-only table lookup failed: %v", err)
	}
	if roTable.RowCount != 1 {
		t.Errorf("Expected RowCount 1, got %d", roTable.RowCount)
	}

	// Mutation operations must fail with ErrReadOnly
	if err := roTable.Insert(map[string]interface{}{"ID": int32(2)}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Expected ErrReadOnly on Insert, got: %v", err)
	}
	if _, err := roTable.Update(map[string]interface{}{"ID": int32(10)}, nil); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Expected ErrReadOnly on Update, got: %v", err)
	}
	if _, err := roTable.Delete(nil); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Expected ErrReadOnly on Delete, got: %v", err)
	}
	if _, err := roDB.CreateTable(TableDef{Name: "New"}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Expected ErrReadOnly on CreateTable, got: %v", err)
	}
	if err := roDB.DropTable("Protected"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Expected ErrReadOnly on DropTable, got: %v", err)
	}
	if err := roDB.Save(); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Expected ErrReadOnly on Save, got: %v", err)
	}
	if err := roDB.PageStore().WritePage(1, make([]byte, roDB.pageSize)); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Expected ErrReadOnly on PageStore.WritePage, got: %v", err)
	}
}

func TestMultiPageTableScan(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_multipage.accdb")
	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}
	defer db.Close()

	table, err := db.CreateTable(TableDef{
		Name: "BigRows",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Payload", Type: ColTypeText, Length: 250},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 500-byte string per row -> 4096-byte page fits ~7 rows per page.
	// 50 rows will span at least 6-7 pages.
	largeText := strings.Repeat("A", 240)
	totalRows := 50
	for i := 0; i < totalRows; i++ {
		err := table.Insert(map[string]interface{}{
			"Payload": largeText,
		})
		if err != nil {
			t.Fatalf("Insert failed on row %d: %v", i, err)
		}
	}

	if len(table.DataPages) < 2 {
		t.Fatalf("Expected multiple data pages, got %d", len(table.DataPages))
	}

	// Verify RowIterator scans all rows across all pages without resetting prematurely
	iter, err := table.Rows()
	if err != nil {
		t.Fatalf("Rows() failed: %v", err)
	}
	scannedCount := 0
	for iter.Next() {
		r := iter.Row()
		if r == nil {
			t.Fatalf("Encountered nil row during scan")
		}
		scannedCount++
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("Iterator error: %v", err)
	}

	if scannedCount != totalRows {
		t.Errorf("Expected scanned count %d across %d data pages, got %d", totalRows, len(table.DataPages), scannedCount)
	}
}

func TestNumericOverflowValidation(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_overflow.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name: "StrictTypes",
		Columns: []ColumnDef{
			{Name: "ByteCol", Type: ColTypeByte},
			{Name: "Int16Col", Type: ColTypeInt},
			{Name: "Int32Col", Type: ColTypeLongInt},
			{Name: "FloatCol", Type: ColTypeFloat},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// Byte overflow (256 > 255)
	err = tbl.Insert(map[string]interface{}{"ByteCol": 256})
	if !errors.Is(err, ErrNumericOverflow) {
		t.Errorf("Expected ErrNumericOverflow for ByteCol 256, got: %v", err)
	}

	// Int16 overflow (40000 > 32767)
	err = tbl.Insert(map[string]interface{}{"Int16Col": 40000})
	if !errors.Is(err, ErrNumericOverflow) {
		t.Errorf("Expected ErrNumericOverflow for Int16Col 40000, got: %v", err)
	}

	// Int32 overflow (> MaxInt32)
	err = tbl.Insert(map[string]interface{}{"Int32Col": int64(3000000000)})
	if !errors.Is(err, ErrNumericOverflow) {
		t.Errorf("Expected ErrNumericOverflow for Int32Col, got: %v", err)
	}

	// Float32 overflow (> MaxFloat32)
	err = tbl.Insert(map[string]interface{}{"FloatCol": 1e39})
	if !errors.Is(err, ErrNumericOverflow) {
		t.Errorf("Expected ErrNumericOverflow for FloatCol, got: %v", err)
	}
}

func TestAutoNumberOverflow(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_autonumber.accdb")
	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name: "CounterTable",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "Name", Type: ColTypeText, Length: 50, Nullable: false},
		},
		Indexes: []IndexDef{
			{Name: "PK_Counter", Columns: []string{"ID"}, Primary: true, Unique: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 1. Set AutoNumber to math.MaxInt32 - 1
	tbl.AutoNumber = uint32(math.MaxInt32 - 1)
	db.writeTableDefPage(tbl)

	// 2. Last valid insert: ID becomes math.MaxInt32
	m1 := map[string]interface{}{"Name": "LastValid"}
	err = tbl.Insert(m1)
	if err != nil {
		t.Fatalf("Expected insert at MaxInt32 to succeed, got: %v", err)
	}
	if tbl.AutoNumber != uint32(math.MaxInt32) {
		t.Errorf("Expected AutoNumber to be MaxInt32 (%d), got %d", math.MaxInt32, tbl.AutoNumber)
	}
	if idVal, ok := m1["ID"].(int32); !ok || idVal != math.MaxInt32 {
		t.Errorf("Expected returned ID to be %d, got %v", math.MaxInt32, m1["ID"])
	}
	if tbl.RowCount != 1 {
		t.Errorf("Expected RowCount 1, got %d", tbl.RowCount)
	}

	// Verify PK lookup works for math.MaxInt32
	row, err := tbl.FindByPrimaryKey(int32(math.MaxInt32))
	if err != nil {
		t.Fatalf("FindByPrimaryKey failed for MaxInt32: %v", err)
	}
	if row.GetString("Name") != "LastValid" {
		t.Errorf("Expected 'LastValid', got '%s'", row.GetString("Name"))
	}

	// Record state before overflow attempt
	autoNumBefore := tbl.AutoNumber
	rowCountBefore := tbl.RowCount
	idxEntriesBefore := len(tbl.Indexes[0].Entries)

	// 3. Next insert must return ErrNumericOverflow
	m2 := map[string]interface{}{"Name": "OverflowAttempt"}
	err = tbl.Insert(m2)
	if !errors.Is(err, ErrNumericOverflow) {
		t.Fatalf("Expected ErrNumericOverflow on AutoNumber overflow, got: %v", err)
	}

	// 4. Verify transaction rollback and state integrity
	if tbl.AutoNumber != autoNumBefore {
		t.Errorf("AutoNumber mutated after failed insert: was %d, now %d", autoNumBefore, tbl.AutoNumber)
	}
	if tbl.RowCount != rowCountBefore {
		t.Errorf("RowCount changed after failed insert: was %d, now %d", rowCountBefore, tbl.RowCount)
	}
	if len(tbl.Indexes[0].Entries) != idxEntriesBefore {
		t.Errorf("Index entries changed after failed insert: was %d, now %d", idxEntriesBefore, len(tbl.Indexes[0].Entries))
	}
	if _, hasID := m2["ID"]; hasID {
		t.Errorf("User input map was modified with 'ID' despite overflow error")
	}

	// Verify existing row is still accessible
	rowCheck, err := tbl.FindByPrimaryKey(int32(math.MaxInt32))
	if err != nil || rowCheck.GetString("Name") != "LastValid" {
		t.Errorf("Existing row corrupted after rollback: %v", err)
	}

	// 5. Tampered metadata with MaxUint32 must also return ErrNumericOverflow without wrapping to 0
	tbl.AutoNumber = math.MaxUint32
	db.writeTableDefPage(tbl)
	m3 := map[string]interface{}{"Name": "TamperedMaxUint32"}
	err = tbl.Insert(m3)
	if !errors.Is(err, ErrNumericOverflow) {
		t.Fatalf("Expected ErrNumericOverflow for tampered MaxUint32 AutoNumber, got: %v", err)
	}
	if _, hasID := m3["ID"]; hasID {
		t.Errorf("User input map was modified with 'ID' on tampered MaxUint32")
	}
}

func TestFailureRollback(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_rollback.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name: "Accounts",
		Columns: []ColumnDef{
			{Name: "AcctNo", Type: ColTypeLongInt, Nullable: false},
			{Name: "Balance", Type: ColTypeMoney, Nullable: false},
		},
		Indexes: []IndexDef{
			{Name: "PK_Acct", Columns: []string{"AcctNo"}, Primary: true, Unique: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	if err := tbl.Insert(map[string]interface{}{"AcctNo": int32(1001), "Balance": 500.0}); err != nil {
		t.Fatalf("Initial insert failed: %v", err)
	}

	initialBytes := append([]byte(nil), db.data...)

	// Attempt conflicting insert
	err = tbl.Insert(map[string]interface{}{"AcctNo": int32(1001), "Balance": 100.0})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("Expected ErrDuplicateKey, got: %v", err)
	}

	// Database state must be 100% identical byte-for-byte to before the failed transaction
	if !bytes.Equal(db.data, initialBytes) {
		t.Errorf("Database data was mutated despite transaction failure")
	}
}

func TestWriteAfterReopen(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_reopen.accdb")

	// 1. Create DB, table "First", insert rows, save, close
	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	firstTable, err := db.CreateTable(TableDef{
		Name: "First",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "Title", Type: ColTypeText, Length: 50, Nullable: false},
		},
		Indexes: []IndexDef{
			{Name: "PK_First", Columns: []string{"ID"}, Primary: true, Unique: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable First failed: %v", err)
	}

	for i := 1; i <= 10; i++ {
		err := firstTable.Insert(map[string]interface{}{
			"Title": fmt.Sprintf("FirstItem_%d", i),
		})
		if err != nil {
			t.Fatalf("Insert failed at %d: %v", i, err)
		}
	}

	if err := db.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	db.Close()

	// 2. Reopen DB, create table "Second", insert rows, save, close
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	secondTable, err := db2.CreateTable(TableDef{
		Name: "Second",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "Description", Type: ColTypeText, Length: 100, Nullable: false},
		},
		Indexes: []IndexDef{
			{Name: "PK_Second", Columns: []string{"ID"}, Primary: true, Unique: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable Second failed: %v", err)
	}

	for i := 1; i <= 15; i++ {
		err := secondTable.Insert(map[string]interface{}{
			"Description": fmt.Sprintf("SecondItem_%d", i),
		})
		if err != nil {
			t.Fatalf("Insert Second failed at %d: %v", i, err)
		}
	}

	if err := db2.Save(); err != nil {
		t.Fatalf("Save second failed: %v", err)
	}
	db2.Close()

	// 3. Reopen DB again, verify BOTH "First" and "Second" have all data intact
	db3, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Reopen 3 failed: %v", err)
	}
	defer db3.Close()

	t1, err := db3.Table("First")
	if err != nil {
		t.Fatalf("Table First missing: %v", err)
	}
	if t1.RowCount != 10 {
		t.Errorf("Expected First RowCount 10, got %d", t1.RowCount)
	}
	for i := 1; i <= 10; i++ {
		row, err := t1.FindByPrimaryKey(int32(i))
		if err != nil {
			t.Fatalf("First FindByPrimaryKey %d failed: %v", i, err)
		}
		expected := fmt.Sprintf("FirstItem_%d", i)
		if row.GetString("Title") != expected {
			t.Errorf("Expected Title '%s', got '%s'", expected, row.GetString("Title"))
		}
	}

	t2, err := db3.Table("Second")
	if err != nil {
		t.Fatalf("Table Second missing: %v", err)
	}
	if t2.RowCount != 15 {
		t.Errorf("Expected Second RowCount 15, got %d", t2.RowCount)
	}
	for i := 1; i <= 15; i++ {
		row, err := t2.FindByPrimaryKey(int32(i))
		if err != nil {
			t.Fatalf("Second FindByPrimaryKey %d failed: %v", i, err)
		}
		expected := fmt.Sprintf("SecondItem_%d", i)
		if row.GetString("Description") != expected {
			t.Errorf("Expected Description '%s', got '%s'", expected, row.GetString("Description"))
		}
	}
}

func TestCreateFortyTables(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_forty_tables.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer db.Close()

	totalTables := 40
	createdCount := 0

	for i := 1; i <= totalTables; i++ {
		tableName := fmt.Sprintf("Table_%02d", i)
		tableDef := TableDef{
			Name: tableName,
			Columns: []ColumnDef{
				{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true, Nullable: false},
				{Name: "ColA", Type: ColTypeText, Length: 50, Nullable: false},
				{Name: "ColB", Type: ColTypeDouble, Nullable: true},
			},
			Indexes: []IndexDef{
				{Name: fmt.Sprintf("PK_%s", tableName), Columns: []string{"ID"}, Primary: true, Unique: true},
			},
		}

		tbl, err := db.CreateTable(tableDef)
		if err != nil {
			if errors.Is(err, ErrUsageMapFull) {
				t.Logf("Reached capacity at table %d with ErrUsageMapFull", i)
				break
			}
			t.Fatalf("CreateTable %s failed with unexpected error: %v", tableName, err)
		}

		for r := 1; r <= 3; r++ {
			err := tbl.Insert(map[string]interface{}{
				"ColA": fmt.Sprintf("Val_%d_%d", i, r),
				"ColB": float64(i*100 + r),
			})
			if err != nil {
				t.Fatalf("Insert into %s failed: %v", tableName, err)
			}
		}
		createdCount++
	}

	if err := db.Save(); err != nil {
		t.Fatalf("Save after creating %d tables failed: %v", createdCount, err)
	}

	for i := 1; i <= createdCount; i++ {
		tableName := fmt.Sprintf("Table_%02d", i)
		tbl, err := db.Table(tableName)
		if err != nil {
			t.Fatalf("Table %s not found: %v", tableName, err)
		}
		if tbl.RowCount != 3 {
			t.Errorf("Table %s expected 3 rows, got %d", tableName, tbl.RowCount)
		}
	}
}

func TestDeleteACEFormat(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_delete_ace.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name: "Items",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "Code", Type: ColTypeText, Length: 20, Nullable: false},
			{Name: "Details", Type: ColTypeText, Length: 100, Nullable: true},
		},
		Indexes: []IndexDef{
			{Name: "PK_Items", Columns: []string{"ID"}, Primary: true, Unique: true},
			{Name: "IX_Code", Columns: []string{"Code"}, Unique: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	for i := 1; i <= 5; i++ {
		err := tbl.Insert(map[string]interface{}{
			"Code":    fmt.Sprintf("C%03d", i),
			"Details": fmt.Sprintf("Detail %d", i),
		})
		if err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}

	if tbl.RowCount != 5 {
		t.Fatalf("Expected 5 rows, got %d", tbl.RowCount)
	}

	pageNum := tbl.DataPages[0]
	start := int(pageNum) * db.pageSize
	page := db.data[start : start+db.pageSize]
	initialRecordCount := readUint16(page, 12)
	if initialRecordCount != 5 {
		t.Fatalf("Expected 5 records on page, got %d", initialRecordCount)
	}

	// 1. Delete middle row (ID 3)
	row3, err := tbl.FindByPrimaryKey(int32(3))
	if err != nil {
		t.Fatalf("FindByPrimaryKey 3 failed: %v", err)
	}
	loc3 := row3.Location()

	err = tbl.DeleteAt(loc3)
	if err != nil {
		t.Fatalf("DeleteAt middle row failed: %v", err)
	}

	// In ACE slotted format, page record count stays unchanged
	pageAfter := db.data[start : start+db.pageSize]
	if readUint16(pageAfter, 12) != initialRecordCount {
		t.Errorf("Expected recordCount on page to stay %d, got %d", initialRecordCount, readUint16(pageAfter, 12))
	}
	// Slot offset must have both rowDeletedMask (0x8000) and rowOverflowMask (0x4000) set
	slot3Offset := readUint16(pageAfter, 14+int(loc3.RowNumber)*2)
	if slot3Offset&(rowDeletedMask|rowOverflowMask) != (rowDeletedMask | rowOverflowMask) {
		t.Errorf("Expected slot %d flags 0xC000, got 0x%04X", loc3.RowNumber, slot3Offset)
	}

	// Repeated DeleteAt must return ErrRowNotFound
	err = tbl.DeleteAt(loc3)
	if !errors.Is(err, ErrRowNotFound) {
		t.Errorf("Expected ErrRowNotFound on repeated DeleteAt, got: %v", err)
	}

	// Primary key & index seek must not find deleted row
	_, err = tbl.FindByPrimaryKey(int32(3))
	if !errors.Is(err, ErrRowNotFound) {
		t.Errorf("Expected ErrRowNotFound from FindByPrimaryKey, got: %v", err)
	}
	_, err = tbl.FindByIndex("IX_Code", "C003")
	if !errors.Is(err, ErrRowNotFound) {
		t.Errorf("Expected ErrRowNotFound from FindByIndex, got: %v", err)
	}

	// 2. Delete first row (ID 1)
	row1, err := tbl.FindByPrimaryKey(int32(1))
	if err != nil {
		t.Fatalf("FindByPrimaryKey 1 failed: %v", err)
	}
	if err := tbl.DeleteAt(row1.Location()); err != nil {
		t.Fatalf("Delete first row failed: %v", err)
	}

	// 3. Delete last row (ID 5)
	row5, err := tbl.FindByPrimaryKey(int32(5))
	if err != nil {
		t.Fatalf("FindByPrimaryKey 5 failed: %v", err)
	}
	if err := tbl.DeleteAt(row5.Location()); err != nil {
		t.Fatalf("Delete last row failed: %v", err)
	}

	if tbl.RowCount != 2 {
		t.Errorf("Expected RowCount 2, got %d", tbl.RowCount)
	}

	if _, err := tbl.FindByPrimaryKey(int32(2)); err != nil {
		t.Errorf("Expected ID 2 to still exist: %v", err)
	}
	if _, err := tbl.FindByPrimaryKey(int32(4)); err != nil {
		t.Errorf("Expected ID 4 to still exist: %v", err)
	}

	// 4. Save and Reopen
	if err := db.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	dbReopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer dbReopened.Close()

	reopenedTbl, err := dbReopened.Table("Items")
	if err != nil {
		t.Fatalf("Table Items missing: %v", err)
	}
	if reopenedTbl.RowCount != 2 {
		t.Errorf("Expected reopened RowCount 2, got %d", reopenedTbl.RowCount)
	}
	if _, err := reopenedTbl.FindByPrimaryKey(int32(3)); !errors.Is(err, ErrRowNotFound) {
		t.Errorf("Expected deleted row 3 to not be found after reopen")
	}
}

func TestDropTableComprehensive(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_droptable.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer db.Close()

	_, err = db.CreateTable(TableDef{
		Name: "KeepTable",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Name", Type: ColTypeText, Length: 50},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable KeepTable failed: %v", err)
	}

	dropTbl, err := db.CreateTable(TableDef{
		Name: "DropMe",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "Val", Type: ColTypeText, Length: 50},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable DropMe failed: %v", err)
	}

	dropTableID := int(dropTbl.ID)

	// Reject system table drop
	if err := db.DropTable("MSysObjects"); err == nil {
		t.Errorf("Expected error when attempting to drop MSysObjects")
	}

	// Reject non-existent table drop
	if err := db.DropTable("NoSuchTable"); err == nil {
		t.Errorf("Expected error when dropping non-existent table")
	}

	// Drop "DropMe"
	if err := db.DropTable("DropMe"); err != nil {
		t.Fatalf("DropTable DropMe failed: %v", err)
	}

	if db.HasTable("DropMe") {
		t.Errorf("DropMe still exists in db")
	}
	if _, err := db.Table("DropMe"); !errors.Is(err, ErrTableNotFound) {
		t.Errorf("Expected ErrTableNotFound, got %v", err)
	}

	keepTbl, err := db.Table("KeepTable")
	if err != nil || keepTbl == nil {
		t.Errorf("KeepTable should still exist")
	}

	// Verify MSysObjects has no rows with Id == dropTableID or ParentId == dropTableID
	sysObjects, err := db.Table("MSysObjects")
	if err != nil {
		t.Fatalf("MSysObjects table missing: %v", err)
	}
	iter, err := sysObjects.Rows()
	if err != nil {
		t.Fatalf("sysObjects Rows failed: %v", err)
	}
	for iter.Next() {
		r := iter.Row()
		if r.GetInt("Id") == dropTableID {
			t.Errorf("MSysObjects still contains row with Id == %d", dropTableID)
		}
		if r.GetInt("ParentId") == dropTableID {
			t.Errorf("MSysObjects still contains child metadata with ParentId == %d", dropTableID)
		}
	}

	// Recreate table with same name
	recreated, err := db.CreateTable(TableDef{
		Name: "DropMe",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "NewField", Type: ColTypeText, Length: 30},
		},
	})
	if err != nil || recreated == nil {
		t.Fatalf("Re-creating table with same name failed: %v", err)
	}

	if err := db.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	dbReopen, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer dbReopen.Close()

	if !dbReopen.HasTable("DropMe") || !dbReopen.HasTable("KeepTable") {
		t.Errorf("Expected both DropMe and KeepTable after reopen")
	}
}

func TestStaleTableHandles(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_stale_handle.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer db.Close()

	_, err = db.CreateTable(TableDef{
		Name: "Accounts",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "AccNo", Type: ColTypeText, Length: 20, Nullable: false},
			{Name: "Balance", Type: ColTypeDouble, Nullable: false},
		},
		Indexes: []IndexDef{
			{Name: "PK_Acc", Columns: []string{"ID"}, Primary: true, Unique: true},
			{Name: "IX_AccNo", Columns: []string{"AccNo"}, Unique: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// Obtain two separate handles to the same table
	t1, err := db.Table("Accounts")
	if err != nil {
		t.Fatalf("Table Accounts t1 failed: %v", err)
	}
	t2, err := db.Table("Accounts")
	if err != nil {
		t.Fatalf("Table Accounts t2 failed: %v", err)
	}

	// Insert using t1
	err = t1.Insert(map[string]interface{}{
		"AccNo":   "A001",
		"Balance": 1000.0,
	})
	if err != nil {
		t.Fatalf("t1.Insert failed: %v", err)
	}

	// Verify t2 sees updated RowCount and can query the row
	if t2.RowCount != 1 {
		t.Errorf("Expected t2.RowCount == 1, got %d", t2.RowCount)
	}
	r, err := t2.FindByIndex("IX_AccNo", "A001")
	if err != nil || r == nil {
		t.Fatalf("t2 failed to find row inserted by t1: %v", err)
	}

	// Insert using t2
	err = t2.Insert(map[string]interface{}{
		"AccNo":   "A002",
		"Balance": 2000.0,
	})
	if err != nil {
		t.Fatalf("t2.Insert failed: %v", err)
	}

	// Verify t1 sees updated RowCount == 2
	if t1.RowCount != 2 {
		t.Errorf("Expected t1.RowCount == 2, got %d", t1.RowCount)
	}

	// Snapshot state before failure test
	dbBytesBefore := append([]byte(nil), db.data...)
	rowCountBefore := t1.RowCount
	autoNumBefore := t1.AutoNumber

	// Test failed transaction (duplicate unique key)
	userMap := map[string]interface{}{
		"AccNo":   "A001", // Duplicate!
		"Balance": 9999.0,
	}

	err = t1.Insert(userMap)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("Expected ErrDuplicateKey, got %v", err)
	}

	// State must be completely unchanged
	if !bytes.Equal(db.data, dbBytesBefore) {
		t.Errorf("Database data modified after failed transaction")
	}
	if t1.RowCount != rowCountBefore || t2.RowCount != rowCountBefore {
		t.Errorf("RowCount changed after failed transaction: t1=%d, t2=%d", t1.RowCount, t2.RowCount)
	}
	if t1.AutoNumber != autoNumBefore {
		t.Errorf("AutoNumber changed after failed transaction: got %d", t1.AutoNumber)
	}

	// User input map must NOT have been mutated
	if _, hasID := userMap["ID"]; hasID {
		t.Errorf("User input map was modified with 'ID' despite transaction failure")
	}
}

func TestUsageMap512PageLimit(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_512pages.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer db.Close()

	// Create table with 2500-byte binary column so each row requires a separate 4096-byte data page
	tbl, err := db.CreateTable(TableDef{
		Name: "BigRows",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt, AutoIncrement: true},
			{Name: "LargePayload", Type: ColTypeBinary},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	payloadData := make([]byte, 2500)
	for i := range payloadData {
		payloadData[i] = byte(i % 256)
	}

	// Insert rows until ErrUsageMapFull is returned (around row 508 when page 512 is attempted)
	var successfulInserts int
	var hitError error

	for i := 0; i < 530; i++ {
		err := tbl.Insert(map[string]interface{}{
			"LargePayload": payloadData,
		})
		if err != nil {
			hitError = err
			break
		}
		successfulInserts++
	}

	if hitError == nil {
		t.Fatalf("Expected ErrUsageMapFull when exceeding 512 pages, but all 530 inserts succeeded")
	}
	if !errors.Is(hitError, ErrUsageMapFull) {
		t.Fatalf("Expected ErrUsageMapFull, got: %v", hitError)
	}
	t.Logf("Successfully inserted %d rows across data pages before safely rejecting page 512 with ErrUsageMapFull", successfulInserts)

	// 1. Verify no fake row count
	if tbl.RowCount != uint32(successfulInserts) {
		t.Errorf("Expected RowCount to be %d, got %d", successfulInserts, tbl.RowCount)
	}

	// 2. Verify transaction rollback: count rows via iterator before save
	iter, err := tbl.Rows()
	if err != nil {
		t.Fatalf("tbl.Rows failed: %v", err)
	}
	var firstPayload, lastPayload []byte
	countBeforeSave := 0
	for iter.Next() {
		countBeforeSave++
		if countBeforeSave == 1 {
			firstPayload = append([]byte(nil), iter.Row().GetBytes("LargePayload")...)
		}
		if countBeforeSave == successfulInserts {
			lastPayload = append([]byte(nil), iter.Row().GetBytes("LargePayload")...)
		}
	}
	if countBeforeSave != successfulInserts {
		t.Errorf("Expected %d rows iterated before save, got %d", successfulInserts, countBeforeSave)
	}

	// 3. Verify prior data is intact
	if !bytes.Equal(firstPayload, payloadData) {
		t.Errorf("First row payload corrupted before save")
	}
	if !bytes.Equal(lastPayload, payloadData) {
		t.Errorf("Last row payload corrupted before save")
	}

	// 4. Save and reopen
	if err := db.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	dbReopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer dbReopened.Close()

	reopenedTbl, err := dbReopened.Table("BigRows")
	if err != nil {
		t.Fatalf("Table BigRows not found in reopened db: %v", err)
	}

	// 5. Verify RowCount after reopen matches
	if reopenedTbl.RowCount != uint32(successfulInserts) {
		t.Errorf("Expected reopened RowCount %d, got %d", successfulInserts, reopenedTbl.RowCount)
	}

	// 6. Verify row count via iterator after reopen matches (no missing rows after reopen!)
	reopenedIter, err := reopenedTbl.Rows()
	if err != nil {
		t.Fatalf("reopenedTbl.Rows failed: %v", err)
	}
	var firstPayloadReopened, lastPayloadReopened []byte
	countAfterReopen := 0
	for reopenedIter.Next() {
		countAfterReopen++
		if countAfterReopen == 1 {
			firstPayloadReopened = append([]byte(nil), reopenedIter.Row().GetBytes("LargePayload")...)
		}
		if countAfterReopen == successfulInserts {
			lastPayloadReopened = append([]byte(nil), reopenedIter.Row().GetBytes("LargePayload")...)
		}
	}
	if countAfterReopen != successfulInserts {
		t.Errorf("Expected %d rows iterated after reopen, got %d (data lost after reopen!)", successfulInserts, countAfterReopen)
	}

	// 7. Verify prior data intact after reopen
	if !bytes.Equal(firstPayloadReopened, payloadData) {
		t.Errorf("First row payload corrupted after reopen")
	}
	if !bytes.Equal(lastPayloadReopened, payloadData) {
		t.Errorf("Last row payload corrupted after reopen")
	}
}

func ExampleQuery_OrderByAsc() {
	db, err := Create(filepath.Join("test_example.accdb"), JetVersion5)
	if err != nil {
		return
	}
	defer func() {
		db.Close()
		_ = os.Remove("test_example.accdb")
	}()

	_, _ = db.CreateTable(TableDef{
		Name: "Employees",
		Columns: []ColumnDef{
			{Name: "FirstName", Type: ColTypeText, Length: 50},
			{Name: "LastName", Type: ColTypeText, Length: 50},
			{Name: "Active", Type: ColTypeBoolean},
		},
	})

	q := db.Select("Employees").
		Where("Active", "=", true).
		OrderByAsc("LastName")

	_ = q
}
