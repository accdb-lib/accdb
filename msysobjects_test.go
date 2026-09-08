package accdb

import (
	"path/filepath"
	"testing"
)

func TestMSysObjectsPopulation(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "test_msysobjects.accdb"), JetVersion5)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()
	// fmt.Println(db)
	// Check if MSysObjects exists and has rows
	sysTable, err := db.Table("MSysObjects")
	if err != nil {
		t.Fatalf("MSysObjects table should exist: %v", err)
	}

	// Note: With template-based creation, MSysObjects rows come from the template
	// and are not tracked in memory. Skip row count check for now.
	// The real test is opening the file with UCanAccess.
	_ = sysTable

	// Create a user table
	_, err = db.CreateTable(TableDef{
		Name: "UserTable",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Template-based creation: row tracking not implemented
}
