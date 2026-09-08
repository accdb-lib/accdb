package main

import (
	"fmt"
	"log"
	"time"

	"github.com/accdb-lib/accdb"
)

func main() {
	// ==========================================
	// ตัวอย่างที่ 1: สร้าง Database ใหม่
	// ==========================================
	fmt.Println("=== Creating New Database ===")

	db, err := accdb.Create("test.accdb", accdb.JetVersion5)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// ==========================================
	// ตัวอย่างที่ 2: สร้าง Table
	// ==========================================
	fmt.Println("\n=== Creating Table ===")

	// วิธีที่ 1: ใช้ TableDef
	tableDef := accdb.TableDef{
		Name: "Employees",
		Columns: []accdb.ColumnDef{
			{Name: "ID", Type: accdb.ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "FirstName", Type: accdb.ColTypeText, Length: 50, Nullable: false},
			{Name: "LastName", Type: accdb.ColTypeText, Length: 50, Nullable: false},
			{Name: "Email", Type: accdb.ColTypeText, Length: 100, Nullable: true},
			{Name: "Salary", Type: accdb.ColTypeDouble, Nullable: true},
			{Name: "HireDate", Type: accdb.ColTypeDateTime, Nullable: true},
			{Name: "Active", Type: accdb.ColTypeBoolean, Nullable: false},
		},
		Indexes: []accdb.IndexDef{
			{Name: "PK_Employees", Columns: []string{"ID"}, Primary: true, Unique: true},
			{Name: "IX_Email", Columns: []string{"Email"}, Unique: true},
		},
	}

	employees, err := db.CreateTable(tableDef)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Created table: %s\n", employees.Name)

	// วิธีที่ 2: ใช้ SQL
	_, err = db.SQL(`
		CREATE TABLE Departments (
			ID INTEGER PRIMARY KEY AUTOINCREMENT,
			Name VARCHAR(100) NOT NULL,
			Budget DOUBLE,
			CreatedAt DATETIME
		)
	`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Created table: Departments")

	// ==========================================
	// ตัวอย่างที่ 3: Insert ข้อมูล
	// ==========================================
	fmt.Println("\n=== Inserting Data ===")

	// วิธีที่ 1: ใช้ Insert method
	err = employees.Insert(map[string]interface{}{
		"FirstName": "สมชาย",
		"LastName":  "ใจดี",
		"Email":     "somchai@example.com",
		"Salary":    50000.00,
		"HireDate":  time.Now(),
		"Active":    true,
	})
	if err != nil {
		log.Fatal(err)
	}

	err = employees.Insert(map[string]interface{}{
		"FirstName": "สมหญิง",
		"LastName":  "รักงาน",
		"Email":     "somying@example.com",
		"Salary":    55000.00,
		"HireDate":  time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC),
		"Active":    true,
	})
	if err != nil {
		log.Fatal(err)
	}

	err = employees.Insert(map[string]interface{}{
		"FirstName": "วิชัย",
		"LastName":  "เก่งมาก",
		"Email":     "wichai@example.com",
		"Salary":    60000.00,
		"HireDate":  time.Date(2022, 1, 10, 0, 0, 0, 0, time.UTC),
		"Active":    false,
	})
	if err != nil {
		log.Fatal(err)
	}

	// วิธีที่ 2: ใช้ SQL
	_, err = db.SQL(`INSERT INTO Departments (Name, Budget) VALUES ('IT', 1000000)`)
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.SQL(`INSERT INTO Departments (Name, Budget) VALUES ('HR', 500000)`)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Inserted %d employees\n", employees.Count())

	// ==========================================
	// ตัวอย่างที่ 4: Query ข้อมูล
	// ==========================================
	fmt.Println("\n=== Querying Data ===")

	// วิธีที่ 1: ใช้ Query Builder
	result, err := db.Select("Employees", "ID", "FirstName", "LastName", "Salary").
		Where("Active", "=", true).
		Where("Salary", ">", 45000).
		OrderByDesc("Salary").
		Execute()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Active employees with salary > 45000:")
	for _, row := range result.Rows {
		fmt.Printf("  - %s %s: %.2f บาท\n",
			row["FirstName"], row["LastName"], row["Salary"])
	}

	// วิธีที่ 2: ใช้ SQL
	result, err = db.SQL(`SELECT * FROM Employees WHERE Active = true ORDER BY LastName`)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nAll active employees (SQL):")
	for _, row := range result.Rows {
		fmt.Printf("  - ID: %v, Name: %s %s\n",
			row["ID"], row["FirstName"], row["LastName"])
	}

	// Query ด้วย LIKE
	result, err = db.Select("Employees").
		Where("Email", "LIKE", "%@example.com").
		Execute()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nEmployees with @example.com email: %d\n", result.Count)

	// ==========================================
	// ตัวอย่างที่ 5: Iterate ทุก Row
	// ==========================================
	fmt.Println("\n=== Iterating All Rows ===")

	iter, err := employees.Rows()
	if err != nil {
		log.Fatal(err)
	}

	for iter.Next() {
		row := iter.Row()
		fmt.Printf("Employee: %s %s (Salary: %s)\n",
			row.GetString("FirstName"),
			row.GetString("LastName"),
			formatMoney(row.GetFloat("Salary")))
	}
	if err := iter.Err(); err != nil {
		log.Fatal(err)
	}

	// ==========================================
	// ตัวอย่างที่ 6: Update ข้อมูล
	// ==========================================
	fmt.Println("\n=== Updating Data ===")

	// ใช้ SQL
	_, err = db.SQL(`UPDATE Employees SET Salary = 52000 WHERE FirstName = 'สมชาย'`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Updated สมชาย's salary")

	// ใช้ Method (update all matching)
	updated, err := employees.Update(
		map[string]interface{}{"Salary": 58000},
		func(row *accdb.Row) bool {
			return row.GetString("FirstName") == "สมหญิง"
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Updated %d rows\n", updated)

	// ==========================================
	// ตัวอย่างที่ 7: Delete ข้อมูล
	// ==========================================
	fmt.Println("\n=== Deleting Data ===")

	// ใช้ SQL
	_, err = db.SQL(`DELETE FROM Employees WHERE Active = false`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Remaining employees: %d\n", employees.Count())

	// ==========================================
	// ตัวอย่างที่ 8: ดูโครงสร้าง Table
	// ==========================================
	fmt.Println("\n=== Table Structure ===")

	tables := db.Tables()
	fmt.Printf("Tables in database: %v\n", tables)

	for _, tableName := range tables {
		table, _ := db.Table(tableName)
		fmt.Printf("\nTable: %s\n", tableName)
		fmt.Printf("  Columns: %v\n", table.ColumnNames())
		fmt.Printf("  Row count: %d\n", table.Count())

		if pk := table.PrimaryKey(); pk != nil {
			fmt.Printf("  Primary key: %s\n", pk.Name)
		}
	}

	// ==========================================
	// ตัวอย่างที่ 9: บันทึกไฟล์
	// ==========================================
	fmt.Println("\n=== Saving Database ===")

	err = db.Save()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Database saved to: %s\n", db.Path())

	// บันทึกเป็นชื่อใหม่
	err = db.SaveAs("backup.accdb")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Backup saved to: backup.accdb")

	// ==========================================
	// ตัวอย่างที่ 10: เปิดไฟล์ที่มีอยู่และอ่านข้อมูล
	// ==========================================
	fmt.Println("\n=== Opening Existing Database ===")

	db2, err := accdb.Open("test.accdb")
	if err != nil {
		log.Fatal(err)
	}
	defer db2.Close()

	fmt.Printf("Opened database version: %d\n", db2.Version())
	fmt.Printf("Tables: %v\n", db2.Tables())

	// ทดสอบ Primary Key lookup
	empTbl, err := db2.Table("Employees")
	if err == nil {
		row, err := empTbl.FindByPrimaryKey(int32(1))
		if err == nil {
			fmt.Printf("Found employee 1 by PK: %s %s (%s)\n",
				row.Get("FirstName"), row.Get("LastName"), row.Get("Email"))
		}
	}

	fmt.Println("\n=== Done! (Alpha WIP) ===")
}

// Helper function
func formatMoney(v float64) string {
	return fmt.Sprintf("%.2f บาท", v)
}

// Custom type for the example
type QueryResult = accdb.QueryResult
