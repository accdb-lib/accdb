package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/accdb-lib/accdb"
)

func main() {
	fileName := "my_pure_test.accdb"

	// ลบไฟล์เดิมทิ้งก่อน (ถ้ามี) เพื่อให้มั่นใจว่าสร้างใหม่ตั้งแต่ byte แรกแน่นอน
	_ = os.Remove(fileName)

	fmt.Printf("1. กำลังสร้างไฟล์ใหม่: %s (เริ่มสร้างจาก byte เปล่าด้วย Go 100%%)\n", fileName)
	db, err := accdb.Create(fileName, accdb.JetVersion5)
	if err != nil {
		log.Fatalf("Create failed: %v", err)
	}

	// สร้างตาราง Members พร้อม Primary Key
	fmt.Println("2. สร้างตาราง Members พร้อม Primary Key และ Unique Index...")
	membersTable, err := db.CreateTable(accdb.TableDef{
		Name: "Members",
		Columns: []accdb.ColumnDef{
			{Name: "MemberID", Type: accdb.ColTypeLongInt, AutoIncrement: true, Nullable: false},
			{Name: "Username", Type: accdb.ColTypeText, Length: 50, Nullable: false},
			{Name: "FullName", Type: accdb.ColTypeText, Length: 100, Nullable: false},
			{Name: "Email", Type: accdb.ColTypeText, Length: 100, Nullable: true},
			{Name: "Points", Type: accdb.ColTypeDouble, Nullable: true},
			{Name: "RegisterDate", Type: accdb.ColTypeDateTime, Nullable: true},
			{Name: "IsVIP", Type: accdb.ColTypeBoolean, Nullable: false},
		},
		Indexes: []accdb.IndexDef{
			{Name: "PK_Members", Columns: []string{"MemberID"}, Primary: true, Unique: true},
			{Name: "IX_Username", Columns: []string{"Username"}, Unique: true},
		},
	})
	if err != nil {
		log.Fatalf("CreateTable failed: %v", err)
	}

	// ใส่ข้อมูลตัวอย่าง
	fmt.Println("3. เพิ่มข้อมูลสมาชิก...")
	members := []map[string]interface{}{
		{
			"Username":     "thanakorn",
			"FullName":     "ธนากร มั่งมี",
			"Email":        "thanakorn@example.com",
			"Points":       1500.50,
			"RegisterDate": time.Now(),
			"IsVIP":        true,
		},
		{
			"Username":     "pimonlada",
			"FullName":     "พิมลดา สุขใจ",
			"Email":        "pimonlada@example.com",
			"Points":       3200.75,
			"RegisterDate": time.Date(2024, 1, 10, 10, 30, 0, 0, time.UTC),
			"IsVIP":        true,
		},
		{
			"Username":     "alanturing",
			"FullName":     "Alan Turing",
			"Email":        "alan.turing@example.com",
			"Points":       9999.00,
			"RegisterDate": time.Date(2023, 6, 23, 8, 0, 0, 0, time.UTC),
			"IsVIP":        true,
		},
		{
			"Username":     "sombat",
			"FullName":     "สมบัติ ผลเจริญ",
			"Email":        "sombat@example.com",
			"Points":       450.00,
			"RegisterDate": time.Date(2024, 8, 15, 14, 20, 0, 0, time.UTC),
			"IsVIP":        false,
		},
	}

	for _, m := range members {
		if err := membersTable.Insert(m); err != nil {
			log.Fatalf("Insert failed: %v", err)
		}
	}
	fmt.Printf("   เพิ่มสำเร็จ %d แถว\n", membersTable.RowCount)

	// บันทึกไฟล์ลง Disk
	fmt.Println("4. กำลังบันทึกไฟล์ลงดิสก์...")
	if err := db.Save(); err != nil {
		log.Fatalf("Save failed: %v", err)
	}
	db.Close()

	fmt.Printf("\n[สำเร็จ] สร้างไฟล์ใหม่เอี่ยม: %s เรียบร้อยแล้วครับ!\n", fileName)
}
