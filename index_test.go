package accdb

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPrimaryKeyAndUniqueIndex(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "pk_test.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// 1. Create table with Primary Key and Unique index
	tableDef := TableDef{
		Name: "Employees",
		Columns: []ColumnDef{
			{Name: "EmpID", Type: ColTypeLongInt, Nullable: false},
			{Name: "Email", Type: ColTypeText, Length: 100, Nullable: false},
			{Name: "Name", Type: ColTypeText, Length: 50, Nullable: false},
			{Name: "Salary", Type: ColTypeDouble, Nullable: true},
		},
		Indexes: []IndexDef{
			{Name: "PK_Employees", Columns: []string{"EmpID"}, Primary: true, Unique: true},
			{Name: "IX_Email", Columns: []string{"Email"}, Unique: true},
		},
	}

	empTable, err := db.CreateTable(tableDef)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	pk := empTable.PrimaryKey()
	if pk == nil {
		t.Fatalf("Expected primary key to exist on Employees table")
	}
	if pk.Name != "PK_Employees" {
		t.Errorf("Expected PK name 'PK_Employees', got '%s'", pk.Name)
	}
	if pk.RootPage == 0 {
		t.Errorf("Expected PK RootPage to be allocated, got 0")
	}

	// 2. Insert valid rows
	rows := []map[string]interface{}{
		{"EmpID": int32(101), "Email": "alice@example.com", "Name": "Alice", "Salary": 75000.0},
		{"EmpID": int32(102), "Email": "bob@example.com", "Name": "Bob", "Salary": 82000.0},
		{"EmpID": int32(103), "Email": "charlie@example.com", "Name": "Charlie", "Salary": 64000.0},
	}

	for _, r := range rows {
		if err := empTable.Insert(r); err != nil {
			t.Fatalf("Failed to insert row %+v: %v", r, err)
		}
	}

	if empTable.RowCount != 3 {
		t.Errorf("Expected RowCount 3, got %d", empTable.RowCount)
	}

	// 3. Fast primary key lookup
	foundRow, err := empTable.FindByPrimaryKey(int32(102))
	if err != nil {
		t.Fatalf("FindByPrimaryKey failed: %v", err)
	}
	if foundRow.GetString("Name") != "Bob" {
		t.Errorf("Expected Name 'Bob', got '%s'", foundRow.GetString("Name"))
	}
	if foundRow.GetString("Email") != "bob@example.com" {
		t.Errorf("Expected Email 'bob@example.com', got '%s'", foundRow.GetString("Email"))
	}

	// Fast secondary index lookup
	foundByEmail, err := empTable.FindByIndex("IX_Email", "alice@example.com")
	if err != nil {
		t.Fatalf("FindByIndex failed: %v", err)
	}
	if foundByEmail.GetString("Name") != "Alice" {
		t.Errorf("Expected Name 'Alice', got '%s'", foundByEmail.GetString("Name"))
	}

	// 4. Duplicate Primary Key must be rejected with ErrDuplicateKey
	dupPKRow := map[string]interface{}{
		"EmpID":  int32(101),
		"Email":  "new_alice@example.com",
		"Name":   "Imposter Alice",
		"Salary": 50000.0,
	}
	err = empTable.Insert(dupPKRow)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("Expected ErrDuplicateKey for duplicate EmpID 101, got: %v", err)
	}

	// 5. Duplicate Unique Index must be rejected with ErrDuplicateKey
	dupEmailRow := map[string]interface{}{
		"EmpID":  int32(104),
		"Email":  "bob@example.com",
		"Name":   "Bob Clone",
		"Salary": 50000.0,
	}
	err = empTable.Insert(dupEmailRow)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("Expected ErrDuplicateKey for duplicate Email 'bob@example.com', got: %v", err)
	}

	// Row count should remain 3
	if empTable.RowCount != 3 {
		t.Errorf("Expected RowCount to remain 3 after rejected inserts, got %d", empTable.RowCount)
	}

	// 6. Save and reopen to verify persistence of schema, indexes, and lookups
	if err := db.Save(); err != nil {
		t.Fatalf("Failed to save database: %v", err)
	}
	db.Close()

	reopenedDB, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to reopen database: %v", err)
	}
	defer reopenedDB.Close()

	reopenedTable, err := reopenedDB.Table("Employees")
	if err != nil {
		t.Fatalf("Failed to find Employees table after reopen: %v", err)
	}

	if reopenedTable.RowCount != 3 {
		t.Errorf("Expected reopened RowCount 3, got %d", reopenedTable.RowCount)
	}

	reopenedPK := reopenedTable.PrimaryKey()
	if reopenedPK == nil {
		t.Fatalf("Expected PrimaryKey to be restored after reopen")
	}
	if reopenedPK.Name != "PK_Employees" {
		t.Errorf("Expected PK name 'PK_Employees', got '%s'", reopenedPK.Name)
	}

	// Verify primary key lookup on reopened table
	row103, err := reopenedTable.FindByPrimaryKey(int32(103))
	if err != nil {
		t.Fatalf("Failed FindByPrimaryKey after reopen: %v", err)
	}
	if row103.GetString("Name") != "Charlie" {
		t.Errorf("Expected 'Charlie', got '%s'", row103.GetString("Name"))
	}

	// Verify duplicate rejection on reopened table
	err = reopenedTable.Insert(map[string]interface{}{
		"EmpID":  int32(102),
		"Email":  "someone@example.com",
		"Name":   "Another Bob",
		"Salary": 40000.0,
	})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("Expected ErrDuplicateKey on reopened table for EmpID 102, got: %v", err)
	}
}

func TestFormatProfileAndPageStore(t *testing.T) {
	p3 := GetFormatProfile(JetVersion3)
	if p3.PageSize != 2048 {
		t.Errorf("Expected Jet3 PageSize 2048, got %d", p3.PageSize)
	}

	p5 := GetFormatProfile(JetVersion5)
	if p5.PageSize != 4096 {
		t.Errorf("Expected Jet5 PageSize 4096, got %d", p5.PageSize)
	}

	memStore, err := NewMemoryPageStore(4096, make([]byte, 8192))
	if err != nil {
		t.Fatalf("Failed to create memory page store: %v", err)
	}

	if memStore.PageCount() != 2 {
		t.Errorf("Expected PageCount 2, got %d", memStore.PageCount())
	}

	// Read valid page
	p0, err := memStore.ReadPage(0)
	if err != nil {
		t.Fatalf("ReadPage(0) failed: %v", err)
	}
	if len(p0) != 4096 {
		t.Errorf("Expected page length 4096, got %d", len(p0))
	}

	// Read out-of-bounds page
	_, err = memStore.ReadPage(99)
	if !errors.Is(err, ErrPageOutOfBounds) {
		t.Errorf("Expected ErrPageOutOfBounds for page 99, got: %v", err)
	}

	// Allocate page
	newPageNum, newPage, err := memStore.AllocatePage(PageTypeData)
	if err != nil {
		t.Fatalf("AllocatePage failed: %v", err)
	}
	if newPageNum != 2 {
		t.Errorf("Expected allocated page 2, got %d", newPageNum)
	}
	if newPage[0] != byte(PageTypeData) {
		t.Errorf("Expected allocated page type %d, got %d", PageTypeData, newPage[0])
	}
	if memStore.PageCount() != 3 {
		t.Errorf("Expected PageCount 3, got %d", memStore.PageCount())
	}
}

func TestColumnCodecs(t *testing.T) {
	// Test Int32Codec Key ordering
	intCodec := Int32Codec{}
	kNeg, _ := intCodec.EncodeKey(int32(-100), nil)
	kZero, _ := intCodec.EncodeKey(int32(0), nil)
	kPos, _ := intCodec.EncodeKey(int32(100), nil)

	if string(kNeg) >= string(kZero) {
		t.Errorf("Expected kNeg < kZero in lexicographical ordering")
	}
	if string(kZero) >= string(kPos) {
		t.Errorf("Expected kZero < kPos in lexicographical ordering")
	}

	// Test TextCodec Key ordering
	textCodec := TextCodec{}
	kApple, _ := textCodec.EncodeKey("Apple", nil)
	kBanana, _ := textCodec.EncodeKey("banana", nil)
	kCherry, _ := textCodec.EncodeKey("CHERRY", nil)

	if string(kApple) >= string(kBanana) {
		t.Errorf("Expected kApple < kBanana")
	}
	if string(kBanana) >= string(kCherry) {
		t.Errorf("Expected kBanana < kCherry")
	}

	// Test DateTimeCodec
	dtCodec := DateTimeCodec{}
	now := time.Now().Truncate(time.Second)
	enc, err := dtCodec.Encode(now, nil)
	if err != nil {
		t.Fatalf("Encode DateTime failed: %v", err)
	}
	dec, err := dtCodec.Decode(enc, nil)
	if err != nil {
		t.Fatalf("Decode DateTime failed: %v", err)
	}
	decTime, ok := dec.(time.Time)
	if !ok {
		t.Fatalf("Decoded value is not time.Time")
	}
	diff := decTime.Sub(now)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Errorf("Decoded time %v deviates from original %v", decTime, now)
	}
}

func TestCreateDatabaseWithPrimaryKey(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "employees_pk.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	tableDef := TableDef{
		Name: "Employees",
		Columns: []ColumnDef{
			{Name: "EmpID", Type: ColTypeLongInt, Nullable: false},
			{Name: "Name", Type: ColTypeText, Length: 50, Nullable: false},
			{Name: "Salary", Type: ColTypeDouble, Nullable: true},
		},
		Indexes: []IndexDef{
			{Name: "PK_Employees", Columns: []string{"EmpID"}, Primary: true, Unique: true},
		},
	}

	empTable, err := db.CreateTable(tableDef)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	if err := empTable.Insert(map[string]interface{}{
		"EmpID": int32(1), "Name": "Alice", "Salary": 80000.0,
	}); err != nil {
		t.Fatalf("Insert row 1 failed: %v", err)
	}

	if err := empTable.Insert(map[string]interface{}{
		"EmpID": int32(2), "Name": "Bob", "Salary": 75000.0,
	}); err != nil {
		t.Fatalf("Insert row 2 failed: %v", err)
	}

	if err := db.Save(); err != nil {
		t.Fatalf("Failed to save database: %v", err)
	}
	db.Close()
}
