package accdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLongValueDescriptorParseAndEncode(t *testing.T) {
	// 1. Inline descriptor
	inlinePayload := []byte("Hello, World!")
	descIn := longValueDescriptor{
		Flags:         lvalFlagInline,
		Length:        uint32(len(inlinePayload)),
		InlinePayload: inlinePayload,
	}
	encoded := encodeLongValueDescriptor(descIn)
	if len(encoded) != lvalDescriptorLen+len(inlinePayload) {
		t.Fatalf("expected encoded length %d, got %d", lvalDescriptorLen+len(inlinePayload), len(encoded))
	}
	parsed, err := parseLongValueDescriptor(encoded)
	if err != nil {
		t.Fatalf("failed to parse inline descriptor: %v", err)
	}
	if parsed.Flags != lvalFlagInline || parsed.Length != uint32(len(inlinePayload)) {
		t.Fatalf("parsed inline mismatch: flags=0x%X, len=%d", parsed.Flags, parsed.Length)
	}
	if !bytes.Equal(parsed.InlinePayload, inlinePayload) {
		t.Fatalf("inline payload mismatch: expected %q, got %q", inlinePayload, parsed.InlinePayload)
	}

	// 2. Single descriptor
	descSingle := longValueDescriptor{
		Flags:  lvalFlagSingle,
		Length: 2000,
		Ref:    longValueRef{Row: 5, Page: 89},
	}
	encodedSingle := encodeLongValueDescriptor(descSingle)
	if len(encodedSingle) != lvalDescriptorLen {
		t.Fatalf("expected single descriptor length 12, got %d", len(encodedSingle))
	}
	parsedSingle, err := parseLongValueDescriptor(encodedSingle)
	if err != nil {
		t.Fatalf("failed to parse single descriptor: %v", err)
	}
	if parsedSingle.Flags != lvalFlagSingle || parsedSingle.Length != 2000 {
		t.Fatalf("parsed single mismatch: flags=0x%X, len=%d", parsedSingle.Flags, parsedSingle.Length)
	}
	if parsedSingle.Ref.Row != 5 || parsedSingle.Ref.Page != 89 {
		t.Fatalf("parsed single ref mismatch: row=%d, page=%d", parsedSingle.Ref.Row, parsedSingle.Ref.Page)
	}

	// 3. Multi descriptor
	descMulti := longValueDescriptor{
		Flags:  lvalFlagMulti,
		Length: 65536,
		Ref:    longValueRef{Row: 0, Page: 104},
	}
	encodedMulti := encodeLongValueDescriptor(descMulti)
	if len(encodedMulti) != lvalDescriptorLen {
		t.Fatalf("expected multi descriptor length 12, got %d", len(encodedMulti))
	}
	parsedMulti, err := parseLongValueDescriptor(encodedMulti)
	if err != nil {
		t.Fatalf("failed to parse multi descriptor: %v", err)
	}
	if parsedMulti.Flags != lvalFlagMulti || parsedMulti.Length != 65536 {
		t.Fatalf("parsed multi mismatch: flags=0x%X, len=%d", parsedMulti.Flags, parsedMulti.Length)
	}
	if parsedMulti.Ref.Row != 0 || parsedMulti.Ref.Page != 104 {
		t.Fatalf("parsed multi ref mismatch: row=%d, page=%d", parsedMulti.Ref.Row, parsedMulti.Ref.Page)
	}

	// 4. Corrupted descriptors
	if _, err := parseLongValueDescriptor([]byte{0x01, 0x02}); err == nil {
		t.Errorf("expected error for descriptor < 12 bytes")
	}
	// Truncated inline payload
	truncInline := make([]byte, 14)
	binary.LittleEndian.PutUint32(truncInline[0:4], lvalFlagInline|10) // declared 10 bytes inline, only 2 provided
	if _, err := parseLongValueDescriptor(truncInline); err == nil {
		t.Errorf("expected error for truncated inline descriptor")
	}
}

func TestLongValueStorageModesRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lval_test.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name: "TestLVal",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt},
			{Name: "MemoCol", Type: ColTypeMemo},
			{Name: "OLECol", Type: ColTypeOLE},
			{Name: "CaseDesc", Type: ColTypeText, Length: 50},
		},
	})
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	testCases := []struct {
		id   int32
		desc string
		memo string
		ole  []byte
	}{
		{id: 1, desc: "NULL", memo: "", ole: nil},
		{id: 2, desc: "Empty", memo: "", ole: []byte{}},
		{id: 3, desc: "1B", memo: "A", ole: []byte{0x42}},
		{id: 4, desc: "10B", memo: "0123456789", ole: bytes.Repeat([]byte{0x10}, 10)},
		{id: 5, desc: "64B_Boundary", memo: strings.Repeat("M", 32), ole: bytes.Repeat([]byte{0x64}, 64)}, // 32 runes = 64 bytes UTF-16
		{id: 6, desc: "100B_Single", memo: strings.Repeat("X", 50), ole: bytes.Repeat([]byte{0xAA}, 100)},
		{id: 7, desc: "1000B_Single", memo: strings.Repeat("Y", 500), ole: bytes.Repeat([]byte{0xBB}, 1000)},
		{id: 8, desc: "4076B_SingleMax", memo: strings.Repeat("Z", 2038), ole: bytes.Repeat([]byte{0xCC}, 4076)},
		{id: 9, desc: "8KB_Multi", memo: strings.Repeat("K", 4096), ole: bytes.Repeat([]byte{0xDD}, 8192)},
		{id: 10, desc: "64KB_Multi", memo: strings.Repeat("W", 32768), ole: bytes.Repeat([]byte{0xEE}, 65536)},
		{id: 11, desc: "Thai_Unicode", memo: "สวัสดีชาวโลก ทดสอบ Long Value Memo ภาษาไทย UTF-16", ole: []byte{0x01, 0x02, 0x03}},
		{id: 12, desc: "Chinese_Emoji", memo: "你好世界 🚀🎉 Access Memo Long Value Test 12345", ole: []byte{0xFF, 0xFE, 0x00, 0x01}},
	}

	for _, tc := range testCases {
		rowMap := map[string]interface{}{
			"ID":       tc.id,
			"CaseDesc": tc.desc,
		}
		if tc.id != 1 {
			rowMap["MemoCol"] = tc.memo
			rowMap["OLECol"] = tc.ole
		}
		if err := tbl.Insert(rowMap); err != nil {
			t.Fatalf("failed to insert case %s (ID=%d): %v", tc.desc, tc.id, err)
		}
	}

	// Verify DataPages contains NO LVAL pages
	for _, dp := range tbl.DataPages {
		page := db.data[int(dp)*db.pageSize : int(dp+1)*db.pageSize]
		ownerStr := string(page[4:8])
		if ownerStr == "LVAL" {
			t.Fatalf("table.DataPages contains LVAL page %d! Invariant violated", dp)
		}
	}

	// Query and verify all rows
	iter, err := tbl.Rows()
	if err != nil {
		t.Fatalf("failed to query rows: %v", err)
	}

	count := 0
	for iter.Next() {
		r := iter.Row()
		id := int32(r.GetInt("ID"))
		tc := testCases[count]
		if id != tc.id {
			t.Fatalf("expected ID %d, got %d", tc.id, id)
		}
		desc := r.GetString("CaseDesc")
		if desc != tc.desc {
			t.Fatalf("expected desc %s, got %s", tc.desc, desc)
		}

		if tc.id == 1 {
			if r.Get("MemoCol") != nil && r.GetString("MemoCol") != "" {
				t.Fatalf("expected NULL memo for case 1, got %v", r.Get("MemoCol"))
			}
			if r.Get("OLECol") != nil && len(r.GetBytes("OLECol")) != 0 {
				t.Fatalf("expected NULL OLE for case 1, got %v", r.Get("OLECol"))
			}
		} else {
			memoGot := r.GetString("MemoCol")
			if memoGot != tc.memo {
				t.Fatalf("case %s (ID=%d) Memo mismatch: expected len %d, got len %d",
					tc.desc, tc.id, len(tc.memo), len(memoGot))
			}
			oleGot := r.GetBytes("OLECol")
			if !bytes.Equal(oleGot, tc.ole) {
				t.Fatalf("case %s (ID=%d) OLE mismatch: expected len %d, got len %d",
					tc.desc, tc.id, len(tc.ole), len(oleGot))
			}
		}
		count++
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}
	if count != len(testCases) {
		t.Fatalf("expected %d rows, got %d", len(testCases), count)
	}

	// Save database to disk before reopening
	if err := db.Save(); err != nil {
		t.Fatalf("failed to save db: %v", err)
	}

	// Reopen database and verify persistence
	db.Close()
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen db: %v", err)
	}
	defer reopened.Close()

	rTbl, err := reopened.Table("TestLVal")
	if err != nil {
		t.Fatalf("failed to get reopened table: %v", err)
	}
	if rTbl.RowCount != uint32(len(testCases)) {
		t.Fatalf("reopened rowCount mismatch: expected %d, got %d", len(testCases), rTbl.RowCount)
	}

	rIter, err := rTbl.Rows()
	if err != nil {
		t.Fatalf("failed to query reopened rows: %v", err)
	}
	rCount := 0
	for rIter.Next() {
		r := rIter.Row()
		tc := testCases[rCount]
		if tc.id != 1 {
			memo := r.GetString("MemoCol")
			if memo != tc.memo {
				t.Fatalf("reopened case %s Memo mismatch", tc.desc)
			}
			ole := r.GetBytes("OLECol")
			if !bytes.Equal(ole, tc.ole) {
				t.Fatalf("reopened case %s OLE mismatch", tc.desc)
			}
		}
		rCount++
	}
	if rCount != len(testCases) {
		t.Fatalf("reopened expected %d rows, got %d", len(testCases), rCount)
	}
}

func TestLongValueUpdateAndReclaim(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lval_update.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name: "UpdateTest",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt},
			{Name: "MemoCol", Type: ColTypeMemo},
		},
	})
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// Insert small inline memo
	if err := tbl.Insert(map[string]interface{}{
		"ID":      int32(1),
		"MemoCol": "Initial Short Memo",
	}); err != nil {
		t.Fatalf("insert error: %v", err)
	}

	// Update to large 64KB multi-chunk memo
	largeMemo := strings.Repeat("BIG_MEMO_", 4000) // 36,000 chars = 72,000 bytes
	updated, err := tbl.Update(map[string]interface{}{
		"MemoCol": largeMemo,
	}, func(r *Row) bool {
		return r.GetInt("ID") == 1
	})
	if err != nil {
		t.Fatalf("update to large memo error: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 updated row, got %d", updated)
	}

	// Verify large memo
	iter, err := tbl.Rows()
	if err != nil {
		t.Fatalf("rows error: %v", err)
	}
	if !iter.Next() {
		t.Fatalf("expected row")
	}
	if iter.Row().GetString("MemoCol") != largeMemo {
		t.Fatalf("updated memo mismatch")
	}

	// Update back to small inline memo
	smallMemo := "Back to small memo"
	updated, err = tbl.Update(map[string]interface{}{
		"MemoCol": smallMemo,
	}, func(r *Row) bool {
		return r.GetInt("ID") == 1
	})
	if err != nil {
		t.Fatalf("update to small memo error: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 updated row, got %d", updated)
	}

	iter, _ = tbl.Rows()
	if !iter.Next() {
		t.Fatalf("expected row")
	}
	if iter.Row().GetString("MemoCol") != smallMemo {
		t.Fatalf("updated small memo mismatch: expected %q, got %q", smallMemo, iter.Row().GetString("MemoCol"))
	}
}

func TestLongValueDeleteAndDropTable(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lval_delete.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	tbl, err := db.CreateTable(TableDef{
		Name: "DeleteTest",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt},
			{Name: "MemoCol", Type: ColTypeMemo},
			{Name: "OLECol", Type: ColTypeOLE},
		},
	})
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	for i := 1; i <= 5; i++ {
		err := tbl.Insert(map[string]interface{}{
			"ID":      int32(i),
			"MemoCol": strings.Repeat(fmt.Sprintf("Row_%d_", i), 1000), // ~7000 chars = 14KB multi chunk
			"OLECol":  bytes.Repeat([]byte{byte(i)}, 5000),             // 5KB multi chunk
		})
		if err != nil {
			t.Fatalf("insert row %d error: %v", i, err)
		}
	}

	// Delete 2 rows
	deleted, err := tbl.Delete(func(r *Row) bool {
		id := r.GetInt("ID")
		return id == 2 || id == 4
	})
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted rows, got %d", deleted)
	}

	// Verify remaining rows
	iter, err := tbl.Rows()
	if err != nil {
		t.Fatalf("rows error: %v", err)
	}
	remainingIDs := []int{}
	for iter.Next() {
		remainingIDs = append(remainingIDs, iter.Row().GetInt("ID"))
	}
	expectedRemaining := []int{1, 3, 5}
	if len(remainingIDs) != len(expectedRemaining) {
		t.Fatalf("expected %v remaining, got %v", expectedRemaining, remainingIDs)
	}

	// Drop table
	if err := db.DropTable("DeleteTest"); err != nil {
		t.Fatalf("drop table error: %v", err)
	}
	if db.HasTable("DeleteTest") {
		t.Fatalf("expected DeleteTest to be dropped")
	}
}

func TestLongValueDefensiveLimitsAndCorruption(t *testing.T) {
	// 1. ErrLongValueTooLarge check
	dummyDB := &Database{
		pageSize:         4096,
		maxLongValueSize: 1024, // limit to 1 KB
	}
	largeDesc := longValueDescriptor{
		Flags:  lvalFlagSingle,
		Length: 2048,
		Ref:    longValueRef{Row: 0, Page: 5},
	}
	if _, err := readLongValue(dummyDB, largeDesc); err != ErrLongValueTooLarge {
		t.Fatalf("expected ErrLongValueTooLarge, got %v", err)
	}

	// 2. Circular chain detection
	// Create simulated DB with two LVAL pages that point to each other
	pageSize := 4096
	mockData := make([]byte, pageSize*6)
	// Page 4: LVAL pointing to Page 5
	p4 := mockData[4*pageSize : 5*pageSize]
	p4[0] = byte(PageTypeData)
	binary.LittleEndian.PutUint32(p4[4:8], lvalOwner)
	binary.LittleEndian.PutUint16(p4[12:14], 1)
	binary.LittleEndian.PutUint16(p4[14:16], 4000) // slot 0 offset
	p4[4000] = 0                                   // next row 0
	p4[4001] = 5                                   // next page 5
	p4[4002] = 0
	p4[4003] = 0

	// Page 5: LVAL pointing back to Page 4
	p5 := mockData[5*pageSize : 6*pageSize]
	p5[0] = byte(PageTypeData)
	binary.LittleEndian.PutUint32(p5[4:8], lvalOwner)
	binary.LittleEndian.PutUint16(p5[12:14], 1)
	binary.LittleEndian.PutUint16(p5[14:16], 4000)
	p5[4000] = 0 // next row 0
	p5[4001] = 4 // next page 4 (CYCLE!)
	p5[4002] = 0
	p5[4003] = 0

	cycleDB := &Database{
		pageSize:         pageSize,
		data:             mockData,
		maxLongValueSize: 64 << 20,
	}
	cycleDesc := longValueDescriptor{
		Flags:  lvalFlagMulti,
		Length: 10000,
		Ref:    longValueRef{Row: 0, Page: 4},
	}
	_, err := readLongValue(cycleDB, cycleDesc)
	if err == nil || !strings.Contains(err.Error(), "circular chain detected") {
		t.Fatalf("expected circular chain error, got %v", err)
	}
}

func TestReadUCanAccessLValFixture(t *testing.T) {
	fixturePath := os.Getenv("LVAL_FIXTURE_PATH")
	if fixturePath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			fixturePath = filepath.Join(home, ".gemini", "antigravity", "brain", "952bb394-b885-4782-911f-c4272ba1d108", "scratch", "lval_fixture.accdb")
		}
	}
	if fixturePath == "" || func() bool { _, err := os.Stat(fixturePath); return os.IsNotExist(err) }() {
		t.Skip("lval_fixture.accdb not found, skipping fixture test")
	}

	db, err := Open(fixturePath)
	if err != nil {
		t.Fatalf("failed to open fixture: %v", err)
	}
	defer db.Close()

	tbl, err := db.Table("LongValueCases")
	if err != nil {
		t.Fatalf("failed to get LongValueCases table: %v", err)
	}
	if tbl.RowCount != 12 {
		t.Fatalf("expected 12 rows, got %d", tbl.RowCount)
	}

	iter, err := tbl.Rows()
	if err != nil {
		t.Fatalf("failed to get rows: %v", err)
	}

	casesRead := 0
	for iter.Next() {
		row := iter.Row()
		id := row.GetInt("ID")
		desc := row.GetString("CaseDesc")
		memo := row.GetString("MemoValue")
		ole := row.GetBytes("OLEValue")

		switch id {
		case 1: // NULL
			if memo != "" || len(ole) != 0 {
				t.Fatalf("case 1 NULL failed: memo=%q, ole=%v", memo, ole)
			}
		case 2: // Empty
			if memo != "" || len(ole) != 0 {
				t.Fatalf("case 2 Empty failed: memo=%q, ole=%v", memo, ole)
			}
		case 3: // 1B
			if memo != "A" || len(ole) != 1 || ole[0] != 0x42 {
				t.Fatalf("case 3 1B failed: memo=%q, ole=%v", memo, ole)
			}
		case 4: // 10B
			if memo != "0123456789" || len(ole) != 10 {
				t.Fatalf("case 4 10B failed: memo=%q, oleLen=%d", memo, len(ole))
			}
		case 5: // 100B
			if len(memo) != 100 || len(ole) != 100 {
				t.Fatalf("case 5 100B failed: memoLen=%d, oleLen=%d", len(memo), len(ole))
			}
		case 6: // 1000B
			if len(memo) != 1000 || len(ole) != 1000 {
				t.Fatalf("case 6 1000B failed: memoLen=%d, oleLen=%d", len(memo), len(ole))
			}
		case 7: // 3900B
			if len(memo) != 3900 || len(ole) != 3900 {
				t.Fatalf("case 7 3900B failed: memoLen=%d, oleLen=%d", len(memo), len(ole))
			}
		case 8: // 4076B
			if len(memo) != 4076 || len(ole) != 4076 {
				t.Fatalf("case 8 4076B failed: memoLen=%d, oleLen=%d", len(memo), len(ole))
			}
		case 9: // 8KB
			if len(memo) != 8192 || len(ole) != 8192 {
				t.Fatalf("case 9 8KB failed: memoLen=%d, oleLen=%d", len(memo), len(ole))
			}
		case 10: // 64KB
			if len(memo) != 65536 || len(ole) != 65536 {
				t.Fatalf("case 10 64KB failed: memoLen=%d, oleLen=%d", len(memo), len(ole))
			}
		case 11: // Unicode Thai
			if !strings.HasPrefix(memo, "สวัสดีชาวโลก") {
				t.Fatalf("case 11 Thai failed: memo=%q", memo)
			}
		case 12: // Unicode Chinese & Emoji
			if !strings.HasPrefix(memo, "你好世界 🚀🎉") {
				t.Fatalf("case 12 Chinese/Emoji failed: memo=%q", memo)
			}
		}
		_ = desc
		casesRead++
	}
	if casesRead != 12 {
		t.Fatalf("expected 12 cases read, got %d", casesRead)
	}
}

func TestCrossCompatibilityGoWritesUCanAccessReads(t *testing.T) {
	// Verify Java is installed before attempting UCanAccess invocation
	javaPath, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java binary not in PATH, skipping UCanAccess verification")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("unable to get user home dir, skipping UCanAccess verification")
	}

	m2Repo := filepath.Join(homeDir, ".m2", "repository")
	ucanJar := filepath.Join(m2Repo, "net", "sf", "ucanaccess", "ucanaccess", "5.0.1", "ucanaccess-5.0.1.jar")
	if _, err := os.Stat(ucanJar); os.IsNotExist(err) {
		t.Skip("UCanAccess jar not found in local m2 cache, skipping cross verification")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "gowrite.accdb")

	db, err := Create(dbPath, JetVersion5)
	if err != nil {
		t.Fatalf("create db failed: %v", err)
	}

	tbl, err := db.CreateTable(TableDef{
		Name: "GoCreatedTable",
		Columns: []ColumnDef{
			{Name: "ID", Type: ColTypeLongInt},
			{Name: "MemoVal", Type: ColTypeMemo},
			{Name: "OLEVal", Type: ColTypeOLE},
			{Name: "Info", Type: ColTypeText, Length: 50},
		},
	})
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	// Insert Inline, Single, Multi
	err = tbl.Insert(map[string]interface{}{
		"ID":      int32(1),
		"MemoVal": "Inline Memo From Go",
		"OLEVal":  []byte{0xAA, 0xBB, 0xCC},
		"Info":    "Inline",
	})
	if err != nil {
		t.Fatalf("insert row 1 failed: %v", err)
	}

	err = tbl.Insert(map[string]interface{}{
		"ID":      int32(2),
		"MemoVal": strings.Repeat("SinglePageMemo_", 50), // ~750 chars = 1500B
		"OLEVal":  bytes.Repeat([]byte{0x55}, 1500),
		"Info":    "Single",
	})
	if err != nil {
		t.Fatalf("insert row 2 failed: %v", err)
	}

	err = tbl.Insert(map[string]interface{}{
		"ID":      int32(3),
		"MemoVal": strings.Repeat("MultiPageMemo_", 1000), // ~14,000 chars = 28,000B
		"OLEVal":  bytes.Repeat([]byte{0x77}, 10000),
		"Info":    "Multi",
	})
	if err != nil {
		t.Fatalf("insert row 3 failed: %v", err)
	}

	if err := db.Save(); err != nil {
		t.Fatalf("save db failed: %v", err)
	}
	db.Close()

	// Build java snippet to read with UCanAccess
	cp := fmt.Sprintf("%s:%s:%s:%s:%s",
		ucanJar,
		filepath.Join(m2Repo, "com", "healthmarketscience", "jackcess", "jackcess", "3.0.1", "jackcess-3.0.1.jar"),
		filepath.Join(m2Repo, "org", "hsqldb", "hsqldb", "2.5.0", "hsqldb-2.5.0.jar"),
		filepath.Join(m2Repo, "commons-logging", "commons-logging", "1.2", "commons-logging-1.2.jar"),
		filepath.Join(m2Repo, "org", "apache", "commons", "commons-lang3", "3.8.1", "commons-lang3-3.8.1.jar"),
	)

	javaSource := fmt.Sprintf(`
import java.sql.*;
public class VerifyGoWrite {
    public static void main(String[] args) throws Exception {
        String url = "jdbc:ucanaccess://" + args[0];
        try (Connection c = DriverManager.getConnection(url);
             Statement s = c.createStatement();
             ResultSet rs = s.executeQuery("SELECT ID, MemoVal, OLEVal, Info FROM GoCreatedTable ORDER BY ID")) {
            int count = 0;
            while (rs.next()) {
                count++;
                int id = rs.getInt(1);
                String memo = rs.getString(2);
                byte[] ole = rs.getBytes(3);
                String info = rs.getString(4);
                if (id == 1) {
                    if (!"Inline Memo From Go".equals(memo) || ole.length != 3) {
                        throw new RuntimeException("Row 1 mismatch: memo=" + memo + ", oleLen=" + ole.length);
                    }
                } else if (id == 2) {
                    if (memo == null || memo.length() != 750 || ole.length != 1500) {
                        throw new RuntimeException("Row 2 mismatch: memoLen=" + (memo != null ? memo.length() : 0) + ", oleLen=" + ole.length);
                    }
                } else if (id == 3) {
                    if (memo == null || memo.length() != 14000 || ole.length != 10000) {
                        throw new RuntimeException("Row 3 mismatch: memoLen=" + (memo != null ? memo.length() : 0) + ", oleLen=" + ole.length);
                    }
                }
            }
            if (count != 3) {
                throw new RuntimeException("Expected 3 rows, found " + count);
            }
            System.out.println("VERIFY_SUCCESS");
        }
    }
}
`)
	srcFile := filepath.Join(tmpDir, "VerifyGoWrite.java")
	if err := os.WriteFile(srcFile, []byte(javaSource), 0600); err != nil {
		t.Fatalf("failed to write java source: %v", err)
	}

	// Compile Java code
	javacCmd := exec.Command("javac", "-cp", cp, srcFile)
	if out, err := javacCmd.CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\nOutput: %s", err, string(out))
	}

	// Run Java code
	runCmd := exec.Command(javaPath, "-cp", cp+":"+tmpDir, "VerifyGoWrite", dbPath)
	out, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("java execution failed: %v\nOutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "VERIFY_SUCCESS") {
		t.Fatalf("expected VERIFY_SUCCESS, got: %s", string(out))
	}
}
