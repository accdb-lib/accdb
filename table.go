package accdb

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrUsageMapFull = errors.New("usage map capacity exceeded")

// Tables returns a list of all table names
func (db *Database) Tables() []string {
	names := make([]string, 0, len(db.tables))
	for name := range db.tables {
		// Skip system tables for user listing
		if !strings.HasPrefix(name, "MSys") {
			names = append(names, name)
		}
	}
	return names
}

// AllTables returns all tables including system tables
func (db *Database) AllTables() []string {
	names := make([]string, 0, len(db.tables))
	for name := range db.tables {
		names = append(names, name)
	}
	return names
}

// Table returns a table by name
func (db *Database) Table(name string) (*Table, error) {
	table, ok := db.tables[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotFound, name)
	}
	return table, nil
}

// HasTable checks if a table exists
func (db *Database) HasTable(name string) bool {
	_, ok := db.tables[name]
	return ok
}

// isSupportedColumnType checks if the given type is recognized
func isSupportedColumnType(t ColumnType) bool {
	switch t {
	case ColTypeBoolean, ColTypeByte, ColTypeInt, ColTypeLongInt,
		ColTypeMoney, ColTypeFloat, ColTypeDouble, ColTypeDateTime,
		ColTypeBinary, ColTypeText, ColTypeOLE, ColTypeMemo,
		ColTypeGUID, ColTypeBigInt, ColTypeComplex:
		return true
	default:
		return false
	}
}

// calculateTableDefSize calculates the exact binary page size needed for a table definition
func calculateTableDefSize(def TableDef) (int, error) {
	indexCount := len(def.Indexes)
	offset := 63 + indexCount*12
	offset += len(def.Columns) * 25
	for _, col := range def.Columns {
		nameBytes := writeUTF16String(col.Name, len([]rune(col.Name))*2)
		offset += 2 + len(nameBytes)
	}
	if indexCount > 0 {
		offset += indexCount * 52
		offset += indexCount * 28
		for _, idx := range def.Indexes {
			nameBytes := writeUTF16String(idx.Name, len([]rune(idx.Name))*2)
			offset += 2 + len(nameBytes)
		}
	}
	return offset, nil
}

// validateTableDef validates the table definition before touching disk or allocating pages
func validateTableDef(profile FormatProfile, def TableDef) error {
	if strings.TrimSpace(def.Name) == "" {
		return fmt.Errorf("%w: table name cannot be empty", ErrInvalidData)
	}

	if len(def.Columns) == 0 {
		return fmt.Errorf("%w: table must have at least one column", ErrInvalidData)
	}

	maxCols := profile.MaxColumns
	if maxCols <= 0 {
		maxCols = 255
	}
	if len(def.Columns) > maxCols {
		return fmt.Errorf("%w: column count %d exceeds maximum %d", ErrInvalidData, len(def.Columns), maxCols)
	}

	colMap := make(map[string]ColumnDef, len(def.Columns))
	autoIncCount := 0
	fixedSize := 0

	for _, col := range def.Columns {
		if strings.TrimSpace(col.Name) == "" {
			return fmt.Errorf("%w: column name cannot be empty", ErrInvalidData)
		}

		lowerName := strings.ToLower(col.Name)
		if _, exists := colMap[lowerName]; exists {
			return fmt.Errorf("%w: duplicate column name '%s'", ErrInvalidData, col.Name)
		}
		colMap[lowerName] = col

		if !isSupportedColumnType(col.Type) {
			return fmt.Errorf("%w: unsupported column type 0x%02x for column '%s'", ErrInvalidData, byte(col.Type), col.Name)
		}

		if col.Type == ColTypeText {
			if col.Length < 0 || col.Length > 255 {
				return fmt.Errorf("%w: text column '%s' length %d out of valid range (1..255)", ErrInvalidData, col.Name, col.Length)
			}
		}

		if col.AutoIncrement {
			autoIncCount++
			if autoIncCount > 1 {
				return fmt.Errorf("%w: table can have at most one auto-increment column", ErrInvalidData)
			}
			if col.Type != ColTypeLongInt && col.Type != ColTypeInt && col.Type != ColTypeBigInt {
				return fmt.Errorf("%w: auto-increment column '%s' must be an integer type", ErrInvalidData, col.Name)
			}
		}

		if !isVariableLength(col.Type) {
			fixedSize += getFixedColumnSize(col.Type)
		}
	}

	pageSize := profile.PageSize
	if pageSize <= 0 {
		pageSize = PageSizeJet5
	}
	maxRowSize := pageSize - 14 - 2
	if fixedSize > maxRowSize {
		return fmt.Errorf("%w: total fixed columns size %d exceeds page inline limit %d", ErrInvalidData, fixedSize, maxRowSize)
	}

	// Validate indexes
	idxNames := make(map[string]bool, len(def.Indexes))
	for _, idx := range def.Indexes {
		if strings.TrimSpace(idx.Name) == "" {
			return fmt.Errorf("%w: index name cannot be empty", ErrInvalidData)
		}
		lowerIdxName := strings.ToLower(idx.Name)
		if idxNames[lowerIdxName] {
			return fmt.Errorf("%w: duplicate index name '%s'", ErrInvalidData, idx.Name)
		}
		idxNames[lowerIdxName] = true

		if len(idx.Columns) == 0 {
			return fmt.Errorf("%w: index '%s' must have at least one column", ErrInvalidData, idx.Name)
		}
		if len(idx.Columns) > 10 {
			return fmt.Errorf("%w: index '%s' has %d columns, maximum allowed is 10", ErrInvalidData, idx.Name, len(idx.Columns))
		}

		for _, colName := range idx.Columns {
			targetCol, found := colMap[strings.ToLower(colName)]
			if !found {
				return fmt.Errorf("%w: index '%s' references unknown column '%s'", ErrColumnNotFound, idx.Name, colName)
			}
			if idx.Primary && targetCol.Nullable {
				return fmt.Errorf("%w: primary key column '%s' cannot be nullable", ErrInvalidData, colName)
			}
		}
	}

	return nil
}

// CreateTable creates a new table within an atomic write transaction.
func (db *Database) CreateTable(def TableDef) (*Table, error) {
	if err := db.ensureWritable(); err != nil {
		return nil, err
	}
	if db.HasTable(def.Name) {
		return nil, fmt.Errorf("table already exists: %s", def.Name)
	}

	if err := validateTableDef(db.profile, def); err != nil {
		return nil, err
	}

	size, err := calculateTableDefSize(def)
	if err != nil {
		return nil, err
	}
	if size > db.pageSize {
		return nil, fmt.Errorf("%w: table definition requires %d bytes, exceeds page size %d", ErrInvalidData, size, db.pageSize)
	}

	err = db.withWriteTransaction(func(working *Database) error {
		_, err := working.createTableInternal(def)
		return err
	})
	if err != nil {
		return nil, err
	}

	return db.Table(def.Name)
}

func (db *Database) createTableInternal(def TableDef) (*Table, error) {
	// Allocate a new page for table definition
	defPageNum, err := db.allocatePage()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate page for table: %w", err)
	}

	usagePage, ownedRow, freeSpaceRow, err := db.allocateUsageMapRows()
	if err != nil {
		return nil, err
	}

	// Create table structure
	table := &Table{
		Name:         def.Name,
		ID:           uint32(defPageNum),
		defPage:      uint32(defPageNum),
		usageMapPage: usagePage,
		ownedMapRow:  ownedRow,
		freeSpaceRow: freeSpaceRow,
		db:           db,
	}

	// Convert column definitions
	for i, colDef := range def.Columns {
		col := &Column{
			Name:          colDef.Name,
			Type:          colDef.Type,
			ID:            uint16(i + 1),
			Index:         uint16(i),
			Nullable:      colDef.Nullable,
			AutoIncrement: colDef.AutoIncrement,
			DefaultValue:  colDef.DefaultValue,
		}

		// Set length
		if colDef.Length > 0 {
			col.Length = uint16(colDef.Length)
		} else {
			col.Length = uint16(getFixedColumnSize(colDef.Type))
			if colDef.Type == ColTypeText && col.Length == 0 {
				col.Length = 255 // Default text length
			}
		}

		table.Columns = append(table.Columns, col)
	}

	// Calculate column offsets
	table.calculateOffsets()

	// Create indexes
	for _, idxDef := range def.Indexes {
		idx := &Index{
			Name:    idxDef.Name,
			Unique:  idxDef.Unique,
			Primary: idxDef.Primary,
			Table:   table,
		}

		for _, colName := range idxDef.Columns {
			col := table.Column(colName)
			if col == nil {
				return nil, fmt.Errorf("index column not found: %s", colName)
			}
			idx.Columns = append(idx.Columns, IndexColumn{
				ColumnID:  col.ID,
				Ascending: true,
			})
		}

		// Allocate index leaf root page
		rootPage, err := db.allocatePage()
		if err != nil {
			return nil, fmt.Errorf("failed to allocate index root page: %w", err)
		}
		idx.RootPage = rootPage
		db.writeIndexLeafPage(idx)

		table.Indexes = append(table.Indexes, idx)
	}

	// Write table definition to page
	db.writeTableDefPage(table)

	// Register table
	db.tables[def.Name] = table

	// Add to MSysObjects
	if err := db.addToMSysObjects(table); err != nil {
		return nil, err
	}

	return table, nil
}

// allocatePage finds and allocates a free page
func (db *Database) allocatePage() (uint32, error) {
	if db == nil || db.pageSize <= 0 {
		return 0, ErrInvalidData
	}
	if db.usageMap == nil {
		if err := db.initUsageMapFromLoaded(); err != nil {
			return 0, err
		}
	}
	totalPages := len(db.data) / db.pageSize

	// Find a free page
	for pageNum := 3; pageNum < totalPages; pageNum++ {
		byteIndex := pageNum / 8
		bitIndex := uint(pageNum % 8)

		if byteIndex < len(db.usageMap.Map) {
			if db.usageMap.Map[byteIndex]&(1<<bitIndex) == 0 {
				// Page is free, mark as used
				db.usageMap.Map[byteIndex] |= (1 << bitIndex)
				return uint32(pageNum), nil
			}
		}
	}

	// Need to extend the database
	newPageNum := totalPages
	newSize := (totalPages + 64) * db.pageSize
	newData := make([]byte, newSize)
	copy(newData, db.data)
	db.data = newData

	// Extend usage map
	newMapSize := (len(newData)/db.pageSize + 7) / 8
	if newMapSize > len(db.usageMap.Map) {
		newMap := make([]byte, newMapSize)
		copy(newMap, db.usageMap.Map)
		db.usageMap.Map = newMap
	}

	// Mark the new page as used
	byteIndex := newPageNum / 8
	bitIndex := uint(newPageNum % 8)
	db.usageMap.Map[byteIndex] |= (1 << bitIndex)

	return uint32(newPageNum), nil
}

// writeTableDefPage writes a table definition to its page
func (db *Database) writeTableDefPage(table *Table) {
	pageOffset := int(table.defPage) * db.pageSize
	page := db.data[pageOffset : pageOffset+db.pageSize]

	// Clear page
	for i := range page {
		page[i] = 0
	}

	// ACE table-definition header.
	page[0] = byte(PageTypeTableDef)
	page[1] = 0x01
	writeUint32(page, 4, 0)
	writeUint32(page, 8, 0)
	writeUint32(page, 12, 1625)
	writeUint32(page, 16, table.RowCount)
	writeUint32(page, 20, table.AutoNumber)
	page[24] = 0x01
	page[40] = 0x01
	writeUint16(page, 41, uint16(len(table.Columns)))
	varCount := 0
	for _, col := range table.Columns {
		if isVariableLength(col.Type) {
			varCount++
		}
	}
	writeUint16(page, 43, uint16(varCount))
	writeUint16(page, 45, uint16(len(table.Columns)))
	indexCount := len(table.Indexes)
	writeUint32(page, 47, uint32(indexCount))
	writeUint32(page, 51, uint32(indexCount))
	page[55] = table.ownedMapRow
	page[56] = byte(table.usageMapPage)
	page[57] = byte(table.usageMapPage >> 8)
	page[58] = byte(table.usageMapPage >> 16)
	page[59] = table.freeSpaceRow
	page[60] = byte(table.usageMapPage)
	page[61] = byte(table.usageMapPage >> 8)
	page[62] = byte(table.usageMapPage >> 16)

	// Write index slots (12 bytes each) starting at offset 63
	for i, idx := range table.Indexes {
		slotOffset := 63 + i*12
		writeUint32(page, slotOffset, uint32(i))
		writeUint32(page, slotOffset+4, idx.RootPage)
		flags := byte(0)
		if idx.Unique {
			flags |= 0x01
		}
		if idx.Primary {
			flags |= 0x02
		}
		if idx.IgnoreNulls {
			flags |= 0x04
		}
		page[slotOffset+8] = flags
		page[slotOffset+9] = 0
		page[slotOffset+10] = 0
		page[slotOffset+11] = 0
	}

	offset := 63 + indexCount*12
	varIndex := uint16(0)
	for i, col := range table.Columns {
		def := ColumnDef{Name: col.Name, Type: col.Type, Length: int(col.Length), AutoIncrement: col.AutoIncrement}
		offset = db.writeColumnDef(page, offset, def, uint16(i), col.Offset, varIndex)
		if isVariableLength(col.Type) {
			varIndex++
		}
	}
	for _, col := range table.Columns {
		nameBytes := writeUTF16String(col.Name, len([]rune(col.Name))*2)
		writeUint16(page, offset, uint16(len(nameBytes)))
		offset += 2
		copy(page[offset:], nameBytes)
		offset += len(nameBytes)
	}

	// Write index definitions (IndexData, IndexImpl, and names)
	if indexCount > 0 {
		// 1. Write IndexData blocks (52 bytes each)
		for _, idx := range table.Indexes {
			// 4 bytes SKIP_BEFORE_INDEX
			writeUint32(page, offset, 0)
			offset += 4

			// 10 columns (each is 2 bytes columnNumber + 1 byte colFlags)
			for c := 0; c < 10; c++ {
				if c < len(idx.Columns) {
					col := table.ColumnByID(idx.Columns[c].ColumnID)
					colNum := uint16(0)
					if col != nil {
						colNum = col.Index
					}
					writeUint16(page, offset, colNum)
					offset += 2
					if idx.Columns[c].Ascending {
						page[offset] = 0x01
					} else {
						page[offset] = 0x00
					}
					offset++
				} else {
					writeUint16(page, offset, 0xFFFF)
					offset += 2
					page[offset] = 0x00
					offset++
				}
			}

			// 4 bytes UsageMap reference (row + 3-byte page)
			page[offset] = table.ownedMapRow
			page[offset+1] = byte(table.usageMapPage)
			page[offset+2] = byte(table.usageMapPage >> 8)
			page[offset+3] = byte(table.usageMapPage >> 16)
			offset += 4

			// 4 bytes RootPage
			writeUint32(page, offset, idx.RootPage)
			offset += 4

			// 4 bytes SKIP_BEFORE_INDEX_FLAGS
			writeUint32(page, offset, 0)
			offset += 4

			// 1 byte indexFlags
			flags := byte(0)
			if idx.Unique {
				flags |= 0x01
			}
			if idx.Primary {
				flags |= 0x02
			}
			if idx.IgnoreNulls {
				flags |= 0x04
			}
			page[offset] = flags
			offset++

			// 5 bytes SKIP_AFTER_INDEX_FLAGS
			for b := 0; b < 5; b++ {
				page[offset+b] = 0
			}
			offset += 5
		}

		// 2. Write IndexImpl blocks (28 bytes each)
		for i, idx := range table.Indexes {
			writeUint32(page, offset, 0) // SKIP_BEFORE_INDEX_SLOT
			offset += 4
			writeUint32(page, offset, uint32(i)) // indexNumber
			offset += 4
			writeUint32(page, offset, uint32(i)) // indexDataNumber
			offset += 4
			page[offset] = 0 // relIndexType
			offset++
			writeUint32(page, offset, 0xFFFFFFFF) // relIndexNumber (-1)
			offset += 4
			writeUint32(page, offset, 0) // relTablePageNumber
			offset += 4
			page[offset] = 0 // cascadeUpdates
			offset++
			page[offset] = 0 // cascadeDeletes
			offset++
			if idx.Primary {
				page[offset] = 1 // PRIMARY_KEY_INDEX_TYPE
			} else {
				page[offset] = 0
			}
			offset++
			writeUint32(page, offset, 0) // SKIP_AFTER_INDEX_SLOT
			offset += 4
		}

		// 3. Write Logical Index Names
		for _, idx := range table.Indexes {
			nameBytes := writeUTF16String(idx.Name, len([]rune(idx.Name))*2)
			writeUint16(page, offset, uint16(len(nameBytes)))
			offset += 2
			copy(page[offset:], nameBytes)
			offset += len(nameBytes)
		}
	}

	// Terminate with 0xFFFF for readColumnUsageMaps
	writeUint16(page, offset, 0xFFFF)
	offset += 2

	writeUint32(page, 8, uint32(offset))
}

func (db *Database) canFitUsageMapRows(page []byte) bool {
	if len(page) < db.pageSize {
		return false
	}
	recordCount := int(readUint16(page, 12))
	if recordCount+2 > 255 {
		return false
	}
	headerNeeded := 14 + (recordCount+2)*2
	rowEnd := db.pageSize
	if recordCount > 0 {
		slotPos := 14 + (recordCount-1)*2
		if slotPos+2 > len(page) {
			return false
		}
		rowEnd = int(readUint16(page, slotPos) & 0x0FFF)
	}
	if rowEnd < 0 || rowEnd > db.pageSize {
		return false
	}
	return (rowEnd - 2*69) >= headerNeeded
}

func (db *Database) writeUsageMapRowsToPage(page []byte) (byte, byte) {
	recordCount := int(readUint16(page, 12))
	rowEnd := db.pageSize
	if recordCount > 0 {
		rowEnd = int(readUint16(page, 14+(recordCount-1)*2) & 0x0FFF)
	}
	for i := 0; i < 2; i++ {
		rowStart := rowEnd - 69
		rowNum := recordCount + i
		writeUint16(page, 14+rowNum*2, uint16(rowStart))
		page[rowStart] = 0x00
		writeUint32(page, rowStart+1, 0)
		for b := 5; b < 69; b++ {
			page[rowStart+b] = 0x00
		}
		rowEnd = rowStart
	}
	newCount := recordCount + 2
	writeUint16(page, 12, uint16(newCount))
	writeUint16(page, 2, uint16(rowEnd-(14+newCount*2)))
	return byte(recordCount), byte(recordCount + 1)
}

func (db *Database) allocateUsageMapRows() (pageNum uint32, ownedRow byte, freeSpaceRow byte, err error) {
	// 1. Try page 1 first
	if len(db.data) >= 2*db.pageSize {
		page1 := db.data[db.pageSize : 2*db.pageSize]
		if db.canFitUsageMapRows(page1) {
			o, f := db.writeUsageMapRowsToPage(page1)
			return 1, o, f, nil
		}
	}

	// 2. Try any existing usage-map carrier page
	for _, tbl := range db.tables {
		if tbl.usageMapPage > 1 && int(tbl.usageMapPage+1)*db.pageSize <= len(db.data) {
			cPage := db.data[int(tbl.usageMapPage)*db.pageSize : int(tbl.usageMapPage+1)*db.pageSize]
			if db.canFitUsageMapRows(cPage) {
				o, f := db.writeUsageMapRowsToPage(cPage)
				return tbl.usageMapPage, o, f, nil
			}
		}
	}

	// 3. Allocate a new carrier page
	newPageNum, err := db.allocatePage()
	if err != nil {
		return 0, 0, 0, ErrUsageMapFull
	}
	pageStart := int(newPageNum) * db.pageSize
	if pageStart+db.pageSize > len(db.data) {
		return 0, 0, 0, ErrUsageMapFull
	}
	carrierPage := db.data[pageStart : pageStart+db.pageSize]
	for i := range carrierPage {
		carrierPage[i] = 0
	}
	carrierPage[0] = byte(PageTypeData)
	carrierPage[1] = 0x01
	writeUint16(carrierPage, 2, uint16(db.pageSize-14))
	writeUint32(carrierPage, 4, 0)
	writeUint16(carrierPage, 12, 0)

	o, f := db.writeUsageMapRowsToPage(carrierPage)
	return newPageNum, o, f, nil
}

func (db *Database) markTableDataPage(table *Table, pageNum uint32) error {
	if db == nil || table == nil {
		return ErrInvalidData
	}
	pageStart := int(table.usageMapPage) * db.pageSize
	if pageStart+db.pageSize > len(db.data) {
		return ErrCorruptDatabase
	}
	usagePage := db.data[pageStart : pageStart+db.pageSize]

	type targetRow struct {
		byteOffset int
		bitMask    byte
	}
	var targets []targetRow

	for _, rowNum := range []byte{table.ownedMapRow, table.freeSpaceRow} {
		slotPos := 14 + int(rowNum)*2
		if slotPos+2 > len(usagePage) {
			return ErrCorruptDatabase
		}
		rowStart := int(readUint16(usagePage, slotPos) & 0x0FFF)
		if rowStart+69 > len(usagePage) {
			return ErrCorruptDatabase
		}
		if usagePage[rowStart] != 0x00 { // MAP_TYPE_INLINE
			return fmt.Errorf("%w: reference usage map", ErrNotImplemented)
		}
		startPage := readUint32(usagePage, rowStart+1)
		if pageNum < startPage || pageNum >= startPage+512 {
			return ErrUsageMapFull
		}
		byteIndex := int(pageNum-startPage) / 8
		if byteIndex >= 64 {
			return ErrUsageMapFull
		}
		byteOffset := rowStart + 5 + byteIndex
		if byteOffset >= len(usagePage) || byteOffset >= rowStart+69 {
			return ErrUsageMapFull
		}
		targets = append(targets, targetRow{
			byteOffset: byteOffset,
			bitMask:    1 << ((pageNum - startPage) % 8),
		})
	}

	for _, target := range targets {
		usagePage[target.byteOffset] |= target.bitMask
	}
	return nil
}

// addToMSysObjects adds a table entry to MSysObjects
func (db *Database) addToMSysObjects(table *Table) error {
	sysTable, ok := db.tables["MSysObjects"]
	if !ok {
		return nil
	}

	// Insert record using insertInternal so it operates within the active transaction
	err := sysTable.insertInternal(map[string]interface{}{
		"Id":         int32(table.ID),
		"Name":       table.Name,
		"Type":       int16(1), // Table
		"ParentId":   int32(0x0F000001),
		"DateCreate": time.Now(),
		"DateUpdate": time.Now(),
		"Owner":      "Engine",
		"Flags":      int32(0),
	})
	if err != nil {
		return fmt.Errorf("register table in MSysObjects: %w", err)
	}
	return nil
}

// calculateOffsets calculates column offsets
func (table *Table) calculateOffsets() {
	var fixedOffset uint16 = 0
	var varIndex uint16 = 0

	// First pass: fixed-length columns
	for _, col := range table.Columns {
		if !isVariableLength(col.Type) {
			col.Offset = fixedOffset
			fixedOffset += col.Length
		}
	}

	// Second pass: variable-length columns
	for _, col := range table.Columns {
		if isVariableLength(col.Type) {
			col.Offset = varIndex
			varIndex++
		}
	}
}

// DropTable removes a table and its catalog metadata from the database within an atomic write transaction.
func (db *Database) DropTable(name string) error {
	if err := db.ensureWritable(); err != nil {
		return err
	}
	if strings.HasPrefix(strings.ToLower(name), "msys") {
		return fmt.Errorf("%w: cannot drop system table %s", ErrInvalidData, name)
	}

	return db.withWriteTransaction(func(working *Database) error {
		// 1. Verify table exists
		tbl, err := working.Table(name)
		if err != nil {
			return err
		}

		// 2. Reject dropping system tables
		if tbl.isSystemTable() || strings.HasPrefix(strings.ToLower(tbl.Name), "msys") {
			return fmt.Errorf("%w: cannot drop system table %s", ErrInvalidData, tbl.Name)
		}

		// 3 & 4. Remove child metadata from MSysObjects (ParentId == tbl.ID)
		sysObjects, err := working.Table("MSysObjects")
		if err == nil && sysObjects != nil {
			_, err = sysObjects.deleteInternal(func(r *Row) bool {
				return r.GetInt("ParentId") == int(tbl.ID)
			})
			if err != nil {
				return fmt.Errorf("remove child metadata from MSysObjects: %w", err)
			}

			// 5. Remove table metadata from MSysObjects (Id == tbl.ID && Type == 1)
			_, err = sysObjects.deleteInternal(func(r *Row) bool {
				return r.GetInt("Id") == int(tbl.ID) && r.GetInt("Type") == 1
			})
			if err != nil {
				return fmt.Errorf("remove table from MSysObjects: %w", err)
			}
		}

		// 6. Remove table from database tables map (pages remain as orphan pages for safety)
		delete(working.tables, tbl.Name)
		for k := range working.tables {
			if strings.EqualFold(k, tbl.Name) {
				delete(working.tables, k)
			}
		}

		return nil
	})
}

// freePage marks a page as free
func (db *Database) freePage(pageNum uint32) {
	if db.usageMap == nil {
		return
	}
	byteIndex := int(pageNum) / 8
	bitIndex := uint(pageNum % 8)

	if byteIndex < len(db.usageMap.Map) {
		db.usageMap.Map[byteIndex] &^= (1 << bitIndex)
	}
}

// Column returns a column by name
func (table *Table) Column(name string) *Column {
	for _, col := range table.Columns {
		if strings.EqualFold(col.Name, name) {
			return col
		}
	}
	return nil
}

// ColumnNames returns all column names
func (table *Table) ColumnNames() []string {
	names := make([]string, len(table.Columns))
	for i, col := range table.Columns {
		names[i] = col.Name
	}
	return names
}

// Index returns an index by name
func (table *Table) Index(name string) *Index {
	for _, idx := range table.Indexes {
		if strings.EqualFold(idx.Name, name) {
			return idx
		}
	}
	return nil
}

// PrimaryKey returns the primary key index if any
func (table *Table) PrimaryKey() *Index {
	for _, idx := range table.Indexes {
		if idx.Primary {
			return idx
		}
	}
	return nil
}

func (table *Table) isSystemTable() bool {
	return strings.HasPrefix(table.Name, "MSys")
}
