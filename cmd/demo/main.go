package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/accdb-lib/accdb"
)

func main() {
	dbPath := "demo.accdb"
	os.Remove(dbPath)

	fmt.Println("=== 1. Creating demo.accdb ===")
	db, err := accdb.Create(dbPath, accdb.JetVersion5)
	if err != nil {
		log.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// ----------------------------------------------------
	// Table 1: Customers
	// ----------------------------------------------------
	fmt.Println("Creating table: Customers...")
	customersTable, err := db.CreateTable(accdb.TableDef{
		Name: "Customers",
		Columns: []accdb.ColumnDef{
			{Name: "ID", Type: accdb.ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "CustomerCode", Type: accdb.ColTypeText, Length: 20, Nullable: false},
			{Name: "CompanyName", Type: accdb.ColTypeText, Length: 100, Nullable: false},
			{Name: "ContactPerson", Type: accdb.ColTypeText, Length: 100, Nullable: true},
			{Name: "CreditLimit", Type: accdb.ColTypeDouble, Nullable: true},
			{Name: "RegisterDate", Type: accdb.ColTypeDateTime, Nullable: true},
			{Name: "Active", Type: accdb.ColTypeBoolean, Nullable: false},
		},
		Indexes: []accdb.IndexDef{
			{Name: "PK_Customers", Columns: []string{"ID"}, Primary: true, Unique: true},
			{Name: "IX_CustomerCode", Columns: []string{"CustomerCode"}, Unique: true},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create Customers: %v", err)
	}

	customerData := []map[string]interface{}{
		{
			"CustomerCode":  "CUST-001",
			"CompanyName":   "บริษัท สยามพานิชย์ จำกัด",
			"ContactPerson": "สมชาย ใจดี",
			"CreditLimit":   500000.0,
			"RegisterDate":  time.Date(2023, 1, 15, 9, 30, 0, 0, time.UTC),
			"Active":        true,
		},
		{
			"CustomerCode":  "CUST-002",
			"CompanyName":   "ห้างหุ้นส่วน วัฒนายนต์",
			"ContactPerson": "สมหญิง รักงาน",
			"CreditLimit":   250000.0,
			"RegisterDate":  time.Date(2023, 3, 20, 14, 0, 0, 0, time.UTC),
			"Active":        true,
		},
		{
			"CustomerCode":  "CUST-003",
			"CompanyName":   "Bangkok Tech Solution Ltd.",
			"ContactPerson": "John Anderson",
			"CreditLimit":   1200000.0,
			"RegisterDate":  time.Date(2023, 7, 10, 11, 15, 0, 0, time.UTC),
			"Active":        true,
		},
		{
			"CustomerCode":  "CUST-004",
			"CompanyName":   "ร้านกิจเจริญ ค้าส่ง",
			"ContactPerson": "กิตติพงษ์ แซ่ตั้ง",
			"CreditLimit":   80000.0,
			"RegisterDate":  time.Date(2024, 2, 1, 10, 0, 0, 0, time.UTC),
			"Active":        false,
		},
		{
			"CustomerCode":  "CUST-005",
			"CompanyName":   "Chiang Mai Organic Farm Co.",
			"ContactPerson": "นรินทร์ ชัยชนะ",
			"CreditLimit":   350000.0,
			"RegisterDate":  time.Date(2024, 5, 12, 16, 45, 0, 0, time.UTC),
			"Active":        true,
		},
	}

	for _, c := range customerData {
		if err := customersTable.Insert(c); err != nil {
			log.Fatalf("Failed to insert customer: %v", err)
		}
	}
	fmt.Printf("Inserted %d customers.\n", customersTable.RowCount)

	// ----------------------------------------------------
	// Table 2: Products
	// ----------------------------------------------------
	fmt.Println("Creating table: Products...")
	productsTable, err := db.CreateTable(accdb.TableDef{
		Name: "Products",
		Columns: []accdb.ColumnDef{
			{Name: "ProductID", Type: accdb.ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "SKU", Type: accdb.ColTypeText, Length: 30, Nullable: false},
			{Name: "ProductName", Type: accdb.ColTypeText, Length: 100, Nullable: false},
			{Name: "Category", Type: accdb.ColTypeText, Length: 50, Nullable: true},
			{Name: "UnitPrice", Type: accdb.ColTypeDouble, Nullable: false},
			{Name: "StockQty", Type: accdb.ColTypeLongInt, Nullable: false},
			{Name: "InStock", Type: accdb.ColTypeBoolean, Nullable: false},
		},
		Indexes: []accdb.IndexDef{
			{Name: "PK_Products", Columns: []string{"ProductID"}, Primary: true, Unique: true},
			{Name: "IX_SKU", Columns: []string{"SKU"}, Unique: true},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create Products: %v", err)
	}

	productData := []map[string]interface{}{
		{
			"SKU":         "FOOD-RICE-05",
			"ProductName": "ข้าวหอมมะลิแท้ 100% (5 กก.)",
			"Category":    "อาหารและเครื่องดื่ม",
			"UnitPrice":   220.0,
			"StockQty":    int32(150),
			"InStock":     true,
		},
		{
			"SKU":         "BEV-COF-250",
			"ProductName": "กาแฟอาราบิก้าคั่วบด ดอยช้าง (250g)",
			"Category":    "เครื่องดื่ม",
			"UnitPrice":   185.50,
			"StockQty":    int32(80),
			"InStock":     true,
		},
		{
			"SKU":         "ELEC-MON-27",
			"ProductName": "จอมอนิเตอร์ IPS 27 นิ้ว 4K UHD",
			"Category":    "อุปกรณ์ไอที",
			"UnitPrice":   8900.0,
			"StockQty":    int32(25),
			"InStock":     true,
		},
		{
			"SKU":         "OFFC-CHR-01",
			"ProductName": "เก้าอี้เพื่อสุขภาพ Ergonomic Chair Pro",
			"Category":    "เฟอร์นิเจอร์สำนักงาน",
			"UnitPrice":   5400.0,
			"StockQty":    int32(0),
			"InStock":     false,
		},
	}

	for _, p := range productData {
		if err := productsTable.Insert(p); err != nil {
			log.Fatalf("Failed to insert product: %v", err)
		}
	}
	fmt.Printf("Inserted %d products.\n", productsTable.RowCount)

	// ----------------------------------------------------
	// Save database to disk
	// ----------------------------------------------------
	if err := db.Save(); err != nil {
		log.Fatalf("Failed to save database: %v", err)
	}
	db.Close()
	fmt.Println("\nSuccessfully generated demo.accdb!")

	// ----------------------------------------------------
	// 2. Pure Go Reopen & Verification
	// ----------------------------------------------------
	fmt.Println("\n=== 2. Reopening demo.accdb with Pure Go ===")
	dbReopen, err := accdb.Open(dbPath)
	if err != nil {
		log.Fatalf("Failed to reopen database: %v", err)
	}
	defer dbReopen.Close()

	fmt.Printf("Database Version: %d\n", dbReopen.Version())
	fmt.Printf("User Tables: %v\n", dbReopen.Tables())

	custTable, err := dbReopen.Table("Customers")
	if err != nil {
		log.Fatalf("Failed to open Customers table: %v", err)
	}
	fmt.Printf("\nTable: %s (Total Rows: %d)\n", custTable.Name, custTable.RowCount)
	if pk := custTable.PrimaryKey(); pk != nil {
		fmt.Printf("  Primary Key: %s\n", pk.Name)
	}

	custIter, err := custTable.Rows()
	if err != nil {
		log.Fatalf("Failed to iterate rows: %v", err)
	}
	for custIter.Next() {
		row := custIter.Row()
		fmt.Printf("  [%d] %s: %s (Contact: %s, Credit: %.2f, Active: %v)\n",
			row.GetInt("ID"),
			row.GetString("CustomerCode"),
			row.GetString("CompanyName"),
			row.GetString("ContactPerson"),
			row.GetFloat("CreditLimit"),
			row.GetBool("Active"),
		)
	}

	// Indexed Lookup Test
	fmt.Println("\nTesting Indexed Primary Key Seek (ID=3):")
	row3, err := custTable.FindByPrimaryKey(int32(3))
	if err != nil {
		log.Fatalf("FindByPrimaryKey failed: %v", err)
	}
	fmt.Printf("  Found: %s (Credit: %.2f)\n", row3.GetString("CompanyName"), row3.GetFloat("CreditLimit"))

	prodTable, err := dbReopen.Table("Products")
	if err != nil {
		log.Fatalf("Failed to open Products table: %v", err)
	}
	fmt.Printf("\nTable: %s (Total Rows: %d)\n", prodTable.Name, prodTable.RowCount)
	if pk := prodTable.PrimaryKey(); pk != nil {
		fmt.Printf("  Primary Key: %s\n", pk.Name)
	}
	prodIter, err := prodTable.Rows()
	if err != nil {
		log.Fatalf("Failed to iterate rows: %v", err)
	}
	for prodIter.Next() {
		row := prodIter.Row()
		fmt.Printf("  [%d] %s: %s (Category: %s, Price: %.2f, Stock: %d)\n",
			row.GetInt("ProductID"),
			row.GetString("SKU"),
			row.GetString("ProductName"),
			row.GetString("Category"),
			row.GetFloat("UnitPrice"),
			row.GetInt("StockQty"),
		)
	}

	fmt.Println("\n=== Pure Go Verification Complete! File is ready for external software ===")
}
