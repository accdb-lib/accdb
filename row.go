package accdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

// Rows returns an iterator over all rows in the table
func (table *Table) Rows() (*RowIterator, error) {
	return &RowIterator{
		table:     table,
		pageIndex: 0,
		rowIndex:  0,
	}, nil
}

// RowIterator iterates over table rows
type RowIterator struct {
	table     *Table
	pageIndex int
	rowIndex  int
	current   *Row
	err       error
}

// Next advances to the next row across all data pages in table.DataPages
func (iter *RowIterator) Next() bool {
	for iter.pageIndex < len(iter.table.DataPages) {
		pageNum := iter.table.DataPages[iter.pageIndex]
		start, end, err := pageBounds(
			pageNum,
			iter.table.db.pageSize,
			len(iter.table.db.data),
		)
		if err != nil {
			iter.err = err
			return false
		}
		page := iter.table.db.data[start:end]
		recordCount := int(readUint16(page, 12))

		for iter.rowIndex < recordCount {
			rowNumber := iter.rowIndex
			iter.rowIndex++

			row, err := iter.parseRowAt(page, pageNum, rowNumber)
			if err != nil {
				iter.err = err
				return false
			}
			if row == nil {
				continue
			}

			iter.current = row
			return true
		}

		iter.pageIndex++
		iter.rowIndex = 0
	}
	return false
}

// Row returns the current row
func (iter *RowIterator) Row() *Row {
	return iter.current
}

// Err returns any error encountered
func (iter *RowIterator) Err() error {
	return iter.err
}

func (iter *RowIterator) parseRowAt(page []byte, pageNum uint32, rowNum int) (*Row, error) {
	return iter.table.parseRowAt(page, pageNum, rowNum, 0)
}

// parseRowFromPage parses a row from a data page for backward compatibility
func (iter *RowIterator) parseRowFromPage(page []byte) (*Row, error) {
	if iter.table == nil || iter.table.db == nil {
		return nil, nil
	}
	pageNum := uint32(0)
	if iter.pageIndex < len(iter.table.DataPages) {
		pageNum = iter.table.DataPages[iter.pageIndex]
	}
	return iter.table.parseRowAt(page, pageNum, iter.rowIndex, 0)
}

// findRowEnd determines the upper byte boundary for a slotted row payload using len(page)
func findRowEnd(page []byte, recordCount int, targetOffset int, pageSize int) int {
	pageLen := len(page)
	minEnd := pageLen
	headerSize := 14
	if pageLen < headerSize {
		return pageLen
	}
	maxSlots := (pageLen - headerSize) / 2
	if recordCount > maxSlots {
		recordCount = maxSlots
	}
	for i := 0; i < recordCount; i++ {
		offsetPos := headerSize + i*2
		if offsetPos+2 > pageLen {
			break
		}
		raw := readUint16(page, offsetPos)
		off := int(raw & rowOffsetMask)
		if off > targetOffset && off < minEnd {
			minEnd = off
		}
	}
	return minEnd
}

// parseRowAt parses a row at a specific page and row slot, following overflow rows with cycle detection
func (table *Table) parseRowAt(page []byte, pageNum uint32, rowNum int, hops int) (*Row, error) {
	if hops > 64 {
		return nil, fmt.Errorf("%w: overflow pointer cycle detected", ErrCorruptDatabase)
	}

	pageLen := len(page)
	if pageLen < 14 {
		return nil, ErrCorruptDatabase
	}

	recordCount := int(readUint16(page, 12))
	offsetTableEnd := 14 + recordCount*2
	if offsetTableEnd > pageLen {
		return nil, ErrCorruptDatabase
	}

	if recordCount == 0 || rowNum >= recordCount || rowNum < 0 {
		return nil, nil
	}

	offsetTableStart := 14
	slotPos := offsetTableStart + rowNum*2
	if slotPos+2 > pageLen {
		return nil, ErrCorruptDatabase
	}

	rawOffset := readUint16(page, slotPos)
	deleted := (rawOffset&rowDeletedMask != 0) && (rawOffset&rowOverflowMask != 0)
	isOverflowPointer := (rawOffset&rowOverflowMask != 0) && (rawOffset&rowDeletedMask == 0)
	isOverflowPayload := (rawOffset&rowDeletedMask != 0) && (rawOffset&rowOverflowMask == 0)

	if deleted {
		return nil, nil
	}
	if hops == 0 && isOverflowPayload {
		return nil, nil
	}

	rowOffset := int(rawOffset & rowOffsetMask)
	if rowOffset < offsetTableEnd || rowOffset >= pageLen {
		return nil, ErrCorruptDatabase
	}

	// Follow overflow row pointer
	if isOverflowPointer {
		if rowOffset+4 > pageLen {
			return nil, ErrCorruptDatabase
		}
		targetRowNum := int(page[rowOffset])
		targetPageNum := uint32(page[rowOffset+1]) | (uint32(page[rowOffset+2]) << 8) | (uint32(page[rowOffset+3]) << 16)

		if table.db == nil {
			return nil, ErrCorruptDatabase
		}
		start, end, err := pageBounds(targetPageNum, table.db.pageSize, len(table.db.data))
		if err != nil {
			return nil, ErrCorruptDatabase
		}
		targetPage := table.db.data[start:end]

		row, err := table.parseRowAt(targetPage, targetPageNum, targetRowNum, hops+1)
		if err != nil {
			return nil, err
		}
		if row != nil {
			// Keep original logical row location
			row.location = RowLocation{PageNumber: pageNum, RowNumber: uint16(rowNum)}
		}
		return row, nil
	}

	rowEnd := findRowEnd(page, recordCount, rowOffset, pageLen)
	if rowEnd <= rowOffset || rowEnd > pageLen {
		return nil, ErrCorruptDatabase
	}

	rowData := page[rowOffset:rowEnd]
	if len(rowData) < 2 {
		return nil, ErrCorruptDatabase
	}

	storedColumnCount := int(readUint16(rowData, 0))
	nullMaskSize := (storedColumnCount + 7) / 8
	if nullMaskSize == 0 || nullMaskSize > len(rowData)-2 {
		return nil, ErrCorruptDatabase
	}
	nullMask := rowData[len(rowData)-nullMaskSize:]

	row := &Row{
		Values: make(map[string]interface{}),
		table:  table,
		location: RowLocation{
			PageNumber: pageNum,
			RowNumber:  uint16(rowNum),
		},
		deleted: false,
	}

	// Fixed-length columns
	for i, col := range table.Columns {
		if isVariableLength(col.Type) {
			continue
		}

		if i >= storedColumnCount || !nullBit(nullMask, i) {
			row.Values[col.Name] = nil
			continue
		}

		valueOffset := 2 + int(col.Offset)
		if valueOffset+getFixedColumnSize(col.Type) > len(rowData)-nullMaskSize {
			return nil, ErrCorruptDatabase
		}
		value, _ := readColumnValue(rowData[valueOffset:], col)
		row.Values[col.Name] = value
	}

	// Variable-length columns
	varColCount := 0
	for _, col := range table.Columns {
		if isVariableLength(col.Type) {
			varColCount++
		}
	}

	if varColCount > 0 {
		trailerStart := len(rowData) - nullMaskSize
		if trailerStart < 2 || int(readUint16(rowData, trailerStart-2)) < varColCount {
			return nil, ErrCorruptDatabase
		}
		varIndex := 0
		for i, col := range table.Columns {
			if !isVariableLength(col.Type) {
				continue
			}

			if i >= storedColumnCount || !nullBit(nullMask, i) {
				row.Values[col.Name] = nil
				varIndex++
				continue
			}

			offsetPos := trailerStart - 4 - varIndex*2
			if offsetPos < 2 {
				return nil, ErrCorruptDatabase
			}
			start := int(readUint16(rowData, offsetPos))
			end := int(readUint16(rowData, offsetPos-2))
			if start <= end && end <= len(rowData) {
				row.Values[col.Name] = readVariableValue(rowData[start:end], col)
			} else {
				return nil, ErrCorruptDatabase
			}
			varIndex++
		}
	}

	return row, nil
}

// readColumnValue reads a fixed-length column value
func readColumnValue(data []byte, col *Column) (interface{}, int) {
	switch col.Type {
	case ColTypeBoolean:
		return data[0] != 0, 1
	case ColTypeByte:
		return data[0], 1
	case ColTypeInt:
		return readInt16(data, 0), 2
	case ColTypeLongInt:
		return readInt32(data, 0), 4
	case ColTypeMoney:
		// Money is stored as 64-bit integer / 10000
		val := int64(readUint32(data, 0)) | (int64(readUint32(data, 4)) << 32)
		return float64(val) / 10000.0, 8
	case ColTypeFloat:
		return readFloat32(data, 0), 4
	case ColTypeDouble:
		return readFloat64(data, 0), 8
	case ColTypeDateTime:
		return readDateTime(data, 0), 8
	case ColTypeGUID:
		guid := make([]byte, 16)
		copy(guid, data[:16])
		return guid, 16
	case ColTypeBigInt:
		val := int64(readUint32(data, 0)) | (int64(readUint32(data, 4)) << 32)
		return val, 8
	default:
		return nil, int(col.Length)
	}
}

// readVariableValue reads a variable-length column value
func readVariableValue(data []byte, col *Column) interface{} {
	switch col.Type {
	case ColTypeText, ColTypeMemo:
		if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
			return string(data[2:])
		}
		// UTF-16
		return readUTF16String(data, 0, len(data))
	case ColTypeBinary, ColTypeOLE:
		result := make([]byte, len(data))
		copy(result, data)
		return result
	default:
		return data
	}
}

// Insert inserts a new row into the table within an atomic write transaction.
func (table *Table) Insert(values map[string]interface{}) error {
	if table.db == nil {
		return ErrInvalidData
	}
	if err := table.db.ensureWritable(); err != nil {
		return err
	}

	origDB := table.db
	var autoIncCols []string
	for _, col := range table.Columns {
		if col.AutoIncrement {
			autoIncCols = append(autoIncCols, col.Name)
		}
	}

	err := origDB.withWriteTransaction(func(working *Database) error {
		workingTable, err := working.Table(table.Name)
		if err != nil {
			return err
		}

		return workingTable.insertInternal(values)
	})
	if err != nil {
		return err
	}

	for _, colName := range autoIncCols {
		values[colName] = int32(table.AutoNumber)
	}
	return nil
}

// insertInternal inserts a new row into the table without initiating a new transaction.
func (table *Table) insertInternal(values map[string]interface{}) error {
	// Validate values
	for colName := range values {
		col := table.Column(colName)
		if col == nil {
			return fmt.Errorf("%w: %s", ErrColumnNotFound, colName)
		}
	}

	// Check required columns
	for _, col := range table.Columns {
		if !col.Nullable && !col.AutoIncrement {
			if _, ok := values[col.Name]; !ok {
				return fmt.Errorf("required column missing: %s", col.Name)
			}
		}
	}

	// Copy values to avoid mutating caller's map unexpectedly during auto-increment
	valCopy := make(map[string]interface{}, len(values)+1)
	for k, v := range values {
		valCopy[k] = v
	}

	// Handle auto-increment
	var nextAutoNumber uint32
	hasAutoInc := false
	for _, col := range table.Columns {
		if col.AutoIncrement {
			if table.AutoNumber >= math.MaxInt32 {
				return fmt.Errorf("%w: auto-number exhausted for table %s", ErrNumericOverflow, table.Name)
			}
			nextAutoNumber = table.AutoNumber + 1
			valCopy[col.Name] = int32(nextAutoNumber)
			hasAutoInc = true
		}
	}

	// Validate column types, lengths, and bounds
	for _, col := range table.Columns {
		if v, exists := valCopy[col.Name]; exists {
			if err := validateColumnValue(col, v); err != nil {
				return err
			}
		}
	}

	// Pre-encode keys and check unique/primary constraints
	for _, idx := range table.Indexes {
		key, err := idx.EncodeKey(valCopy)
		if err != nil {
			return err
		}
		if idx.Unique || idx.Primary {
			if idx.ContainsKey(key) {
				return ErrDuplicateKey
			}
		}

		// Verify that this index leaf page has capacity for this entry
		if idx.RootPage != 0 && table.db != nil {
			entrySize := 2 + len(key) + 4 + 2
			currentUsage := 14
			for _, e := range idx.Entries {
				currentUsage += 2 + len(e.Key) + 4 + 2
			}
			if currentUsage+entrySize > table.db.pageSize {
				return ErrIndexPageFull
			}
		}
	}

	// Build row data
	rowData, err := table.buildRowData(valCopy)
	if err != nil {
		return err
	}

	// Check oversized row inline limit (page size - page header - slot offset entry)
	maxRowSize := table.db.pageSize - 14 - 2
	if len(rowData) > maxRowSize {
		return fmt.Errorf("%w: row size %d exceeds inline limit %d", ErrInvalidData, len(rowData), maxRowSize)
	}

	// Find or allocate a data page with enough space
	pageNum, err := table.findOrAllocateDataPage(len(rowData))
	if err != nil {
		return err
	}

	// Write row to page
	rowNum, err := table.writeRowToPage(pageNum, rowData)
	if err != nil {
		return err
	}

	// Update indexes
	loc := RowLocation{PageNumber: pageNum, RowNumber: rowNum}
	changes, err := table.prepareIndexInsert(valCopy, loc)
	if err != nil {
		return err
	}
	commitIndexChanges(changes)

	// Commit auto-increment counter now that the row is written
	if hasAutoInc {
		table.AutoNumber = nextAutoNumber
	}

	// Update row count
	table.RowCount++

	if table.isSystemTable() {
		pageOffset := int(table.defPage) * table.db.pageSize
		writeUint32(table.db.data[pageOffset:pageOffset+table.db.pageSize], 16, table.RowCount)
	} else {
		table.db.writeTableDefPage(table)
	}

	return nil
}

// buildRowData builds binary row data from values
func (table *Table) buildRowData(values map[string]interface{}) ([]byte, error) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(len(table.Columns)))

	nullMaskSize := (len(table.Columns) + 7) / 8
	nullMask := make([]byte, nullMaskSize)

	fixedSize := 0
	for _, col := range table.Columns {
		if !isVariableLength(col.Type) {
			end := int(col.Offset) + getFixedColumnSize(col.Type)
			if end > fixedSize {
				fixedSize = end
			}
		}
	}
	fixedData := make([]byte, fixedSize)
	for i, col := range table.Columns {
		v, exists := values[col.Name]
		if exists && v != nil {
			setNullBit(nullMask, i, true)
		}
		if isVariableLength(col.Type) {
			continue
		}
		if v == nil {
			continue
		}
		var value bytes.Buffer
		if err := writeColumnValue(&value, col, v); err != nil {
			return nil, err
		}
		copy(fixedData[int(col.Offset):], value.Bytes())
	}
	buf.Write(fixedData)

	// Collect variable-length data
	var varData [][]byte
	for _, col := range table.Columns {
		if !isVariableLength(col.Type) {
			continue
		}

		v := values[col.Name]
		if v == nil {
			varData = append(varData, nil)
			continue
		}

		data, err := encodeVariableValue(col, v)
		if err != nil {
			return nil, err
		}
		varData = append(varData, data)
	}

	varOffsets := make([]uint16, len(varData))
	currentOffset := uint16(buf.Len())
	for i, data := range varData {
		varOffsets[i] = currentOffset
		buf.Write(data)
		currentOffset += uint16(len(data))
	}

	// EOD and variable offsets form the row trailer. Offsets are reversed.
	binary.Write(&buf, binary.LittleEndian, currentOffset)
	for i := len(varOffsets) - 1; i >= 0; i-- {
		binary.Write(&buf, binary.LittleEndian, varOffsets[i])
	}
	binary.Write(&buf, binary.LittleEndian, uint16(len(varOffsets)))
	buf.Write(nullMask)

	return buf.Bytes(), nil
}

// writeColumnValue writes a fixed-length column value
func writeColumnValue(buf *bytes.Buffer, col *Column, v interface{}) error {
	if err := validateColumnValue(col, v); err != nil {
		return err
	}
	switch col.Type {
	case ColTypeBoolean:
		if b, ok := v.(bool); ok {
			if b {
				buf.WriteByte(0xFF)
			} else {
				buf.WriteByte(0x00)
			}
		} else {
			buf.WriteByte(0x00)
		}
	case ColTypeByte:
		switch val := v.(type) {
		case byte:
			buf.WriteByte(val)
		case int:
			buf.WriteByte(byte(val))
		default:
			buf.WriteByte(0)
		}
	case ColTypeInt:
		var val int16
		switch n := v.(type) {
		case int16:
			val = n
		case int:
			val = int16(n)
		case int32:
			val = int16(n)
		}
		binary.Write(buf, binary.LittleEndian, val)
	case ColTypeLongInt:
		var val int32
		switch n := v.(type) {
		case int32:
			val = n
		case int:
			val = int32(n)
		case int64:
			val = int32(n)
		}
		binary.Write(buf, binary.LittleEndian, val)
	case ColTypeMoney:
		var val float64
		switch n := v.(type) {
		case float64:
			val = n
		case float32:
			val = float64(n)
		case int:
			val = float64(n)
		}
		// Convert to fixed-point
		fixed := int64(val * 10000)
		binary.Write(buf, binary.LittleEndian, fixed)
	case ColTypeFloat:
		var val float32
		switch n := v.(type) {
		case float32:
			val = n
		case float64:
			val = float32(n)
		}
		binary.Write(buf, binary.LittleEndian, val)
	case ColTypeDouble:
		var val float64
		switch n := v.(type) {
		case float64:
			val = n
		case float32:
			val = float64(n)
		}
		binary.Write(buf, binary.LittleEndian, val)
	case ColTypeDateTime:
		var t time.Time
		switch n := v.(type) {
		case time.Time:
			t = n
		}
		oleDate := writeDateTime(t)
		binary.Write(buf, binary.LittleEndian, oleDate)
	case ColTypeGUID:
		switch n := v.(type) {
		case []byte:
			if len(n) == 16 {
				buf.Write(n)
			} else {
				buf.Write(make([]byte, 16))
			}
		default:
			buf.Write(make([]byte, 16))
		}
	case ColTypeBigInt:
		var val int64
		switch n := v.(type) {
		case int64:
			val = n
		case int:
			val = int64(n)
		case int32:
			val = int64(n)
		}
		binary.Write(buf, binary.LittleEndian, val)
	default:
		return fmt.Errorf("unsupported column type: %d", col.Type)
	}
	return nil
}

// encodeVariableValue encodes a variable-length value
func encodeVariableValue(col *Column, v interface{}) ([]byte, error) {
	if err := validateColumnValue(col, v); err != nil {
		return nil, err
	}
	switch col.Type {
	case ColTypeText, ColTypeMemo:
		var s string
		switch val := v.(type) {
		case string:
			s = val
		case []byte:
			s = string(val)
		default:
			s = fmt.Sprint(v)
		}
		canCompress := len(s) > 2
		for _, r := range s {
			if r < 1 || r > 0xFF {
				canCompress = false
				break
			}
		}
		if canCompress {
			return append([]byte{0xFF, 0xFE}, []byte(s)...), nil
		}
		return writeUTF16String(s, len([]rune(s))*2), nil
	case ColTypeBinary, ColTypeOLE:
		switch val := v.(type) {
		case []byte:
			return val, nil
		default:
			return nil, fmt.Errorf("binary column requires []byte")
		}
	default:
		return nil, fmt.Errorf("unsupported variable type: %d", col.Type)
	}
}

// findOrAllocateDataPage finds a data page with space or allocates a new one
func (table *Table) findOrAllocateDataPage(rowSize int) (uint32, error) {
	db := table.db
	if db == nil {
		return 0, errors.New("table is not associated with a database")
	}

	// Look for existing data page with space
	for _, pageNum := range table.DataPages {
		start, end, err := pageBounds(pageNum, db.pageSize, len(db.data))
		if err != nil {
			continue
		}
		page := db.data[start:end]

		freeSpace := int(readUint16(page, 2))
		if freeSpace >= rowSize+2 { // +2 for offset entry
			return pageNum, nil
		}
	}

	// Allocate new data page
	pageNum, err := db.allocatePage()
	if err != nil {
		return 0, err
	}
	start, end, err := pageBounds(pageNum, db.pageSize, len(db.data))
	if err != nil {
		return 0, err
	}
	page := db.data[start:end]

	// Initialize data page header
	page[0] = byte(PageTypeData)
	writeUint16(page, 2, uint16(db.pageSize-14)) // Free space
	writeUint32(page, 4, table.ID)               // Owner table
	writeUint16(page, 12, 0)                     // Record count

	if err := db.markTableDataPage(table, pageNum); err != nil {
		return 0, err
	}
	table.DataPages = append(table.DataPages, pageNum)

	return pageNum, nil
}

// writeRowToPage writes row data to a data page and returns the assigned row number
func (table *Table) writeRowToPage(pageNum uint32, rowData []byte) (uint16, error) {
	db := table.db
	if db == nil {
		return 0, errors.New("table is not associated with a database")
	}
	start, end, err := pageBounds(pageNum, db.pageSize, len(db.data))
	if err != nil {
		return 0, err
	}
	page := db.data[start:end]

	// Get current record count
	recordCount := int(readUint16(page, 12))

	// Jet/ACE slotted-page layout: row offsets follow the page header while
	// row payloads grow backward from the page end.
	headerSize := 14
	offsetEntryPos := headerSize + recordCount*2
	rowEnd := db.pageSize
	if recordCount > 0 {
		rowEnd = db.pageSize
		for i := 0; i < recordCount; i++ {
			offset := int(readUint16(page, headerSize+i*2) & 0x0FFF)
			if offset > 0 && offset < rowEnd {
				rowEnd = offset
			}
		}
	}
	rowOffset := rowEnd - len(rowData)

	if rowOffset < offsetEntryPos+2 {
		return 0, fmt.Errorf("%w: page %d has insufficient space for row", ErrInvalidData, pageNum)
	}

	// Write payload and its offset entry.
	copy(page[rowOffset:rowEnd], rowData)
	writeUint16(page, offsetEntryPos, uint16(rowOffset))

	// Update record count
	writeUint16(page, 12, uint16(recordCount+1))

	// Update free space
	freeSpace := rowOffset - (offsetEntryPos + 2)
	writeUint16(page, 2, uint16(freeSpace))

	return uint16(recordCount), nil
}

func validateColumnValue(col *Column, v interface{}) error {
	if v == nil {
		if !col.Nullable && !col.AutoIncrement {
			return fmt.Errorf("column %s cannot be null", col.Name)
		}
		return nil
	}

	switch col.Type {
	case ColTypeBoolean:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%w: column %s expects bool, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeByte:
		switch n := v.(type) {
		case byte:
		case int:
			if n < 0 || n > 255 {
				return fmt.Errorf("%w: value %d overflows byte for column %s", ErrNumericOverflow, n, col.Name)
			}
		case int64:
			if n < 0 || n > 255 {
				return fmt.Errorf("%w: value %d overflows byte for column %s", ErrNumericOverflow, n, col.Name)
			}
		default:
			return fmt.Errorf("%w: column %s expects byte/int, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeInt:
		switch n := v.(type) {
		case int16:
		case int:
			if n < math.MinInt16 || n > math.MaxInt16 {
				return fmt.Errorf("%w: value %d overflows int16 for column %s", ErrNumericOverflow, n, col.Name)
			}
		case int32:
			if n < math.MinInt16 || n > math.MaxInt16 {
				return fmt.Errorf("%w: value %d overflows int16 for column %s", ErrNumericOverflow, n, col.Name)
			}
		case int64:
			if n < math.MinInt16 || n > math.MaxInt16 {
				return fmt.Errorf("%w: value %d overflows int16 for column %s", ErrNumericOverflow, n, col.Name)
			}
		default:
			return fmt.Errorf("%w: column %s expects integer, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeLongInt:
		switch n := v.(type) {
		case int32:
		case int:
			if int64(n) < math.MinInt32 || int64(n) > math.MaxInt32 {
				return fmt.Errorf("%w: value %d overflows int32 for column %s", ErrNumericOverflow, n, col.Name)
			}
		case int64:
			if n < math.MinInt32 || n > math.MaxInt32 {
				return fmt.Errorf("%w: value %d overflows int32 for column %s", ErrNumericOverflow, n, col.Name)
			}
		default:
			return fmt.Errorf("%w: column %s expects integer, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeBigInt:
		switch v.(type) {
		case int64, int, int32, int16:
		default:
			return fmt.Errorf("%w: column %s expects int64, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeMoney:
		switch n := v.(type) {
		case float64:
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return fmt.Errorf("%w: money cannot be NaN or Inf for column %s", ErrInvalidData, col.Name)
			}
			val := n * 10000.0
			if val < float64(math.MinInt64) || val > float64(math.MaxInt64) {
				return fmt.Errorf("%w: money value %v overflows 64-bit currency for column %s", ErrNumericOverflow, n, col.Name)
			}
		case float32:
			if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
				return fmt.Errorf("%w: money cannot be NaN or Inf for column %s", ErrInvalidData, col.Name)
			}
		case int, int64, int32:
		default:
			return fmt.Errorf("%w: column %s expects float or int currency, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeFloat:
		switch n := v.(type) {
		case float32:
			if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
				return fmt.Errorf("%w: float cannot be NaN or Inf for column %s", ErrInvalidData, col.Name)
			}
		case float64:
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return fmt.Errorf("%w: float cannot be NaN or Inf for column %s", ErrInvalidData, col.Name)
			}
			if math.Abs(n) > math.MaxFloat32 {
				return fmt.Errorf("%w: float64 %v overflows float32 for column %s", ErrNumericOverflow, n, col.Name)
			}
		default:
			return fmt.Errorf("%w: column %s expects float, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeDouble:
		switch n := v.(type) {
		case float64:
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return fmt.Errorf("%w: double cannot be NaN or Inf for column %s", ErrInvalidData, col.Name)
			}
		case float32:
		default:
			return fmt.Errorf("%w: column %s expects float/double, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeDateTime:
		if _, ok := v.(time.Time); !ok {
			return fmt.Errorf("%w: column %s expects time.Time, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeGUID:
		switch g := v.(type) {
		case []byte:
			if len(g) != 16 {
				return fmt.Errorf("%w: GUID must be 16 bytes for column %s", ErrInvalidData, col.Name)
			}
		default:
			return fmt.Errorf("%w: column %s expects 16-byte GUID, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeText:
		var s string
		switch str := v.(type) {
		case string:
			s = str
		case []byte:
			s = string(str)
		default:
			return fmt.Errorf("%w: column %s expects text string, got %T", ErrInvalidData, col.Name, v)
		}
		if col.Length > 0 && len([]rune(s)) > int(col.Length) {
			return fmt.Errorf("%w: text length %d exceeds maximum %d for column %s", ErrInvalidData, len([]rune(s)), col.Length, col.Name)
		}
	case ColTypeMemo:
		switch v.(type) {
		case string, []byte:
		default:
			return fmt.Errorf("%w: column %s expects string/bytes for memo, got %T", ErrInvalidData, col.Name, v)
		}
	case ColTypeBinary, ColTypeOLE:
		b, ok := v.([]byte)
		if !ok {
			return fmt.Errorf("%w: column %s expects []byte, got %T", ErrInvalidData, col.Name, v)
		}
		if col.Length > 0 && len(b) > int(col.Length) {
			return fmt.Errorf("%w: binary length %d exceeds maximum %d for column %s", ErrInvalidData, len(b), col.Length, col.Name)
		}
	}
	return nil
}

func (table *Table) validateUpdateValues(values map[string]interface{}) error {
	for name, value := range values {
		col := table.Column(name)
		if col == nil {
			return fmt.Errorf("%w: %s", ErrColumnNotFound, name)
		}
		if col.AutoIncrement {
			return fmt.Errorf("cannot update auto-increment column %s", name)
		}
		if value == nil && !col.Nullable {
			return fmt.Errorf("column %s cannot be null", name)
		}
		if err := validateColumnValue(col, value); err != nil {
			return err
		}
	}
	return nil
}

func cloneValues(v map[string]interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	res := make(map[string]interface{}, len(v))
	for k, val := range v {
		if b, ok := val.([]byte); ok {
			copied := make([]byte, len(b))
			copy(copied, b)
			res[k] = copied
		} else {
			res[k] = val
		}
	}
	return res
}

func rebuildDataPage(page []byte, replacementRow int, replacementData []byte, pageSize int) ([]byte, error) {
	return rebuildDataPageWithFlags(page, replacementRow, replacementData, 0, pageSize)
}

func rebuildDataPageWithFlags(page []byte, replacementRow int, replacementData []byte, replacementFlags uint16, pageSize int) ([]byte, error) {
	if len(page) < 14 {
		return nil, ErrCorruptDatabase
	}
	recordCount := int(readUint16(page, 12))
	if replacementRow < 0 || replacementRow >= recordCount {
		return nil, fmt.Errorf("%w: invalid replacement row %d", ErrInvalidData, replacementRow)
	}

	type slotRecord struct {
		data  []byte
		flags uint16
	}
	slots := make([]slotRecord, recordCount)

	for i := 0; i < recordCount; i++ {
		rawOffset := readUint16(page, 14+i*2)
		flags := rawOffset & (rowDeletedMask | rowOverflowMask)
		offset := int(rawOffset & rowOffsetMask)

		if i == replacementRow {
			slots[i] = slotRecord{
				data:  replacementData,
				flags: replacementFlags,
			}
			continue
		}

		if (flags&rowDeletedMask != 0 && flags&rowOverflowMask != 0) || offset == 0 || offset >= pageSize {
			// Deleted or empty slot
			slots[i] = slotRecord{data: nil, flags: flags}
			continue
		}

		rowEnd := findRowEnd(page, recordCount, offset, pageSize)
		if rowEnd > offset && rowEnd <= pageSize {
			copied := make([]byte, rowEnd-offset)
			copy(copied, page[offset:rowEnd])
			slots[i] = slotRecord{data: copied, flags: flags}
		} else {
			slots[i] = slotRecord{data: nil, flags: flags}
		}
	}

	newPage := make([]byte, pageSize)
	copy(newPage[:14], page[:14])

	cursor := pageSize
	for i := 0; i < recordCount; i++ {
		rec := slots[i]
		if len(rec.data) > 0 {
			cursor -= len(rec.data)
			copy(newPage[cursor:], rec.data)
			writeUint16(newPage, 14+i*2, uint16(cursor)|rec.flags)
		} else {
			writeUint16(newPage, 14+i*2, rec.flags)
		}
	}

	requiredHeader := 14 + recordCount*2
	if cursor < requiredHeader {
		return nil, fmt.Errorf("%w: repacked rows exceed page capacity", ErrInvalidData)
	}

	freeSpace := cursor - requiredHeader
	writeUint16(newPage, 2, uint16(freeSpace))
	writeUint16(newPage, 12, uint16(recordCount))

	return newPage, nil
}

func (table *Table) deleteAt(loc RowLocation) error {
	db := table.db
	if db == nil {
		return ErrInvalidData
	}

	isPageOwner := false
	for _, p := range table.DataPages {
		if p == loc.PageNumber {
			isPageOwner = true
			break
		}
	}
	if !isPageOwner {
		return fmt.Errorf("%w: page %d does not belong to table %s", ErrInvalidData, loc.PageNumber, table.Name)
	}

	start, end, err := pageBounds(loc.PageNumber, db.pageSize, len(db.data))
	if err != nil {
		return err
	}
	page := db.data[start:end]
	recordCount := int(readUint16(page, 12))
	if int(loc.RowNumber) >= recordCount {
		return ErrRowNotFound
	}

	slotPos := 14 + int(loc.RowNumber)*2
	rawOffset := readUint16(page, slotPos)
	deleted := (rawOffset&rowDeletedMask != 0) && (rawOffset&rowOverflowMask != 0)
	if deleted {
		return ErrRowNotFound
	}

	isOverflowPointer := (rawOffset&rowOverflowMask != 0) && (rawOffset&rowDeletedMask == 0)
	offset := int(rawOffset & rowOffsetMask)

	if isOverflowPointer && offset > 0 && offset+4 <= len(page) {
		targetRowNum := int(page[offset])
		targetPageNum := uint32(page[offset+1]) | (uint32(page[offset+2]) << 8) | (uint32(page[offset+3]) << 16)
		oStart, oEnd, err := pageBounds(targetPageNum, db.pageSize, len(db.data))
		if err == nil {
			oPage := db.data[oStart:oEnd]
			oCount := int(readUint16(oPage, 12))
			if targetRowNum < oCount {
				oSlot := 14 + targetRowNum*2
				oRaw := readUint16(oPage, oSlot)
				writeUint16(oPage, oSlot, oRaw|rowDeletedMask|rowOverflowMask)
			}
		}
	}

	writeUint16(page, slotPos, rawOffset|rowDeletedMask|rowOverflowMask)

	if table.RowCount > 0 {
		table.RowCount--
	}
	if table.isSystemTable() {
		pageOffset := int(table.defPage) * db.pageSize
		writeUint32(db.data[pageOffset:pageOffset+db.pageSize], 16, table.RowCount)
	} else {
		db.writeTableDefPage(table)
	}

	return nil
}

func (table *Table) deleteInternal(where func(*Row) bool) (int, error) {
	type delCandidate struct {
		loc    RowLocation
		values map[string]interface{}
	}
	var candidates []delCandidate

	iter, err := table.Rows()
	if err != nil {
		return 0, err
	}
	for iter.Next() {
		r := iter.Row()
		if where == nil || where(r) {
			candidates = append(candidates, delCandidate{
				loc:    r.Location(),
				values: cloneValues(r.Values),
			})
		}
	}
	if err := iter.Err(); err != nil {
		return 0, err
	}

	if len(candidates) == 0 {
		return 0, nil
	}

	type plannedDel struct {
		changes []*IndexChange
	}
	planned := make([]plannedDel, len(candidates))
	for i, cand := range candidates {
		changes, err := table.prepareIndexDelete(cand.values, cand.loc)
		if err != nil {
			return 0, err
		}
		planned[i] = plannedDel{changes: changes}
	}

	for i, cand := range candidates {
		if err := table.deleteAt(cand.loc); err != nil {
			return 0, err
		}
		commitIndexChanges(planned[i].changes)
	}

	return len(candidates), nil
}

// Delete deletes rows matching the condition within an atomic write transaction.
func (table *Table) Delete(where func(*Row) bool) (int, error) {
	if table.db == nil {
		return 0, ErrInvalidData
	}
	if err := table.db.ensureWritable(); err != nil {
		return 0, err
	}

	origDB := table.db
	var count int
	err := origDB.withWriteTransaction(func(working *Database) error {
		workingTable, err := working.Table(table.Name)
		if err != nil {
			return err
		}
		n, err := workingTable.deleteInternal(where)
		if err != nil {
			return err
		}
		count = n
		return nil
	})
	return count, err
}

// DeleteAt deletes a specific row by location within an atomic write transaction.
func (table *Table) DeleteAt(loc RowLocation) error {
	if table.db == nil {
		return ErrInvalidData
	}
	if err := table.db.ensureWritable(); err != nil {
		return err
	}

	origDB := table.db
	return origDB.withWriteTransaction(func(working *Database) error {
		workingTable, err := working.Table(table.Name)
		if err != nil {
			return err
		}
		row, err := workingTable.RowAt(loc)
		if err != nil {
			return err
		}
		if row == nil {
			return ErrRowNotFound
		}
		changes, err := workingTable.prepareIndexDelete(row.Values, loc)
		if err != nil {
			return err
		}
		if err := workingTable.deleteAt(loc); err != nil {
			return err
		}
		commitIndexChanges(changes)
		return nil
	})
}

func (table *Table) updateInternal(values map[string]interface{}, where func(*Row) bool) (int, error) {
	type updateCandidate struct {
		loc       RowLocation
		oldValues map[string]interface{}
		newValues map[string]interface{}
	}
	var candidates []updateCandidate

	iter, err := table.Rows()
	if err != nil {
		return 0, err
	}
	for iter.Next() {
		r := iter.Row()
		if where == nil || where(r) {
			oldVals := cloneValues(r.Values)
			newVals := cloneValues(oldVals)
			for k, v := range values {
				newVals[k] = v
			}
			candidates = append(candidates, updateCandidate{
				loc:       r.Location(),
				oldValues: oldVals,
				newValues: newVals,
			})
		}
	}
	if err := iter.Err(); err != nil {
		return 0, err
	}

	if len(candidates) == 0 {
		return 0, nil
	}

	type plannedUpdate struct {
		changes []*IndexChange
	}
	planned := make([]plannedUpdate, len(candidates))
	for i, cand := range candidates {
		changes, err := table.prepareIndexUpdate(cand.oldValues, cand.newValues, cand.loc)
		if err != nil {
			return 0, err
		}
		planned[i] = plannedUpdate{changes: changes}
	}

	for i, cand := range candidates {
		newData, err := table.buildRowData(cand.newValues)
		if err != nil {
			return 0, err
		}

		start, end, err := pageBounds(cand.loc.PageNumber, table.db.pageSize, len(table.db.data))
		if err != nil {
			return 0, err
		}
		page := table.db.data[start:end]

		rebuiltPage, err := rebuildDataPage(page, int(cand.loc.RowNumber), newData, table.db.pageSize)
		if err == nil {
			copy(table.db.data[start:end], rebuiltPage)
		} else {
			// Page is full -> use overflow row
			overflowPageNum, err := table.findOrAllocateDataPage(len(newData))
			if err != nil {
				return 0, err
			}
			overflowRowNum, err := table.writeRowToPage(overflowPageNum, newData)
			if err != nil {
				return 0, err
			}

			// Mark overflow payload slot with rowDeletedMask
			oStart, oEnd, err := pageBounds(overflowPageNum, table.db.pageSize, len(table.db.data))
			if err != nil {
				return 0, err
			}
			oPage := table.db.data[oStart:oEnd]
			slotPos := 14 + int(overflowRowNum)*2
			raw := readUint16(oPage, slotPos)
			writeUint16(oPage, slotPos, raw|rowDeletedMask)

			// Pointer: [rowNum, page0, page1, page2]
			ptrData := make([]byte, 4)
			ptrData[0] = byte(overflowRowNum)
			ptrData[1] = byte(overflowPageNum & 0xFF)
			ptrData[2] = byte((overflowPageNum >> 8) & 0xFF)
			ptrData[3] = byte((overflowPageNum >> 16) & 0xFF)

			rebuiltHeader, err := rebuildDataPageWithFlags(page, int(cand.loc.RowNumber), ptrData, rowOverflowMask, table.db.pageSize)
			if err != nil {
				return 0, err
			}
			copy(table.db.data[start:end], rebuiltHeader)
		}

		commitIndexChanges(planned[i].changes)
	}

	if table.isSystemTable() {
		pageOffset := int(table.defPage) * table.db.pageSize
		writeUint32(table.db.data[pageOffset:pageOffset+table.db.pageSize], 16, table.RowCount)
	} else {
		table.db.writeTableDefPage(table)
	}

	return len(candidates), nil
}

// Update updates rows matching the condition within an atomic write transaction.
func (table *Table) Update(values map[string]interface{}, where func(*Row) bool) (int, error) {
	if table.db == nil {
		return 0, ErrInvalidData
	}
	if err := table.db.ensureWritable(); err != nil {
		return 0, err
	}
	if err := table.validateUpdateValues(values); err != nil {
		return 0, err
	}

	origDB := table.db
	var count int
	err := origDB.withWriteTransaction(func(working *Database) error {
		workingTable, err := working.Table(table.Name)
		if err != nil {
			return err
		}
		n, err := workingTable.updateInternal(values, where)
		if err != nil {
			return err
		}
		count = n
		return nil
	})
	return count, err
}

// Count returns the number of rows in the table
func (table *Table) Count() uint32 {
	return table.RowCount
}

// Get returns a single value from a row
func (row *Row) Get(column string) interface{} {
	return row.Values[column]
}

// GetString returns a string value
func (row *Row) GetString(column string) string {
	v := row.Values[column]
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// GetInt returns an integer value
func (row *Row) GetInt(column string) int {
	v := row.Values[column]
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// GetFloat returns a float value
func (row *Row) GetFloat(column string) float64 {
	v := row.Values[column]
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

// GetBool returns a boolean value
func (row *Row) GetBool(column string) bool {
	v := row.Values[column]
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// GetTime returns a time value
func (row *Row) GetTime(column string) time.Time {
	v := row.Values[column]
	if v == nil {
		return time.Time{}
	}
	if t, ok := v.(time.Time); ok {
		return t
	}
	return time.Time{}
}

// IsNull checks if a column value is null
func (row *Row) IsNull(column string) bool {
	v, exists := row.Values[column]
	return !exists || v == nil
}

// GetBytes returns a byte slice value
func (row *Row) GetBytes(column string) []byte {
	v := row.Values[column]
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		return b
	}
	return nil
}
