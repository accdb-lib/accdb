package accdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrInvalidFile        = errors.New("invalid Access database file")
	ErrUnsupportedVersion = errors.New("unsupported database version")
	ErrEncrypted          = errors.New("database is encrypted")
	ErrTableNotFound      = errors.New("table not found")
	ErrColumnNotFound     = errors.New("column not found")
	ErrInvalidData        = errors.New("invalid data")
	ErrNotImplemented     = errors.New("feature is not implemented")
	ErrDatabaseTooLarge   = errors.New("database file exceeds maximum allowed size")
	ErrReadOnly           = errors.New("database is read-only")
	ErrNumericOverflow    = errors.New("numeric value overflows column range")
)

// OpenOptions configures how database files are opened
type OpenOptions struct {
	MaxFileSize int64
	ReadOnly    bool
}

// DefaultMaxFileSize limits memory usage to 1 GiB during initial Alpha
const DefaultMaxFileSize = 1 << 30 // 1 GiB

// Open opens an existing Access database file using default options
func Open(path string) (*Database, error) {
	return OpenWithOptions(path, OpenOptions{
		MaxFileSize: DefaultMaxFileSize,
	})
}

// OpenWithOptions opens an Access database with explicit resource limits
func OpenWithOptions(path string, options OpenOptions) (*Database, error) {
	if options.MaxFileSize <= 0 {
		options.MaxFileSize = DefaultMaxFileSize
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if info.Size() > options.MaxFileSize {
		return nil, fmt.Errorf(
			"%w: file size %d exceeds limit %d",
			ErrDatabaseTooLarge,
			info.Size(),
			options.MaxFileSize,
		)
	}

	limited := io.LimitReader(file, options.MaxFileSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	if int64(len(data)) > options.MaxFileSize {
		return nil, fmt.Errorf("%w: file size exceeds limit %d", ErrDatabaseTooLarge, options.MaxFileSize)
	}

	if len(data) < 2048 {
		return nil, ErrInvalidFile
	}

	db := &Database{
		path:     path,
		data:     data,
		readOnly: options.ReadOnly,
		encoding: binary.LittleEndian,
		tables:   make(map[string]*Table),
	}

	if err := db.parseHeader(); err != nil {
		return nil, err
	}

	db.profile = GetFormatProfile(db.version)
	store, err := NewMemoryPageStore(db.pageSize, db.data)
	if err != nil {
		return nil, err
	}
	if options.ReadOnly {
		store.SetReadOnly(true)
	}
	db.pageStore = store

	if db.encrypted {
		return nil, fmt.Errorf("%w: Access encryption", ErrNotImplemented)
	}

	if err := db.loadSystemTables(); err != nil {
		return nil, err
	}

	if err := db.initUsageMapFromLoaded(); err != nil {
		return nil, err
	}

	return db, nil
}

// initUsageMapFromLoaded initializes the global usage map from an existing database file
func (db *Database) initUsageMapFromLoaded() error {
	if db.pageSize <= 0 || len(db.data)%db.pageSize != 0 {
		return fmt.Errorf("%w: invalid database file size alignment", ErrCorruptDatabase)
	}

	totalPages := len(db.data) / db.pageSize
	mapSize := (totalPages + 7) / 8
	if mapSize < 1 {
		mapSize = 1
	}

	usageMap := &UsageMap{
		StartPage: 0,
		PageCount: uint32(totalPages),
		Map:       make([]byte, mapSize),
	}

	// Mark all existing physical pages as used to prevent allocation collisions
	for p := 0; p < totalPages; p++ {
		usageMap.Map[p/8] |= (1 << uint(p%8))
	}

	db.usageMap = usageMap
	return nil
}

// Create creates a new Access database file.
// In Alpha, creation is strictly limited to verified ACE/ACCDB (JetVersion5).
func Create(path string, version JetVersion) (*Database, error) {
	if version != JetVersion5 {
		return nil, fmt.Errorf("%w: only ACE/ACCDB (JetVersion5) is currently supported for creation", ErrUnsupportedVersion)
	}

	ext := filepath.Ext(path)
	if !strings.EqualFold(ext, ".accdb") {
		return nil, errors.New("ACE database must use .accdb extension")
	}

	pageSize := PageSizeJet5

	db := &Database{
		path:       path,
		version:    version,
		profile:    GetFormatProfile(version),
		pageSize:   pageSize,
		encoding:   binary.LittleEndian,
		tables:     make(map[string]*Table),
		createdAt:  time.Now(),
		modifiedAt: time.Now(),
	}

	// Initialize empty database
	if err := db.initializeDatabase(); err != nil {
		return nil, err
	}

	store, err := NewMemoryPageStore(db.pageSize, db.data)
	if err != nil {
		return nil, err
	}
	db.pageStore = store

	return db, nil
}

// PageStore returns the database's PageStore
func (db *Database) PageStore() PageStore {
	return db.pageStore
}

// Profile returns the database's FormatProfile
func (db *Database) Profile() FormatProfile {
	return db.profile
}

// parseHeader parses the database header
func (db *Database) parseHeader() error {
	data := db.data

	// Check magic bytes (first 4 bytes should be 0x00 0x01 0x00 0x00)
	if !bytes.HasPrefix(data, MagicMDB) {
		return ErrInvalidFile
	}

	// Check Jet signature at offset 0x04
	if !bytes.Contains(data[4:20], []byte("Standard Jet")) {
		// Try alternate signatures
		if !bytes.Contains(data[4:32], []byte("Standard ACE")) {
			return ErrInvalidFile
		}
	}

	// Read Jet version at offset 0x14
	jetVersion := data[OffsetJetVersion]
	switch jetVersion {
	case 0x00:
		db.version = JetVersion3
		db.pageSize = PageSizeJet3
	case 0x01:
		db.version = JetVersion4
		db.pageSize = PageSizeJet4
	case 0x02, 0x03:
		db.version = JetVersion5
		db.pageSize = PageSizeJet4
	default:
		return ErrUnsupportedVersion
	}

	// Check encryption (offset 0x62 for Access 2007+)
	if db.version >= JetVersion5 {
		// Check for encryption flags (at offset 0x298 + 16 = 0x2A8)
		encFlag := readUint32(data, 0x2A8)
		if encFlag != 0 {
			db.encrypted = true
			db.encType = EncryptionType((encFlag >> 8) & 0xFF)
		}
	} else {
		// Older versions: check database key
		key := readUint32(data, OffsetDbKey)
		if key != 0 {
			db.encrypted = true
			db.encType = EncryptionRC4
		}
	}

	// Read code page
	db.codePage = readUint16(data, OffsetCodePage)

	return nil
}

// initializeDatabase creates the initial database structure
func (db *Database) initializeDatabase() error {
	// Calculate initial size
	numPages := 128 // Start with 128 pages (~512KB)
	db.data = make([]byte, numPages*db.pageSize)

	// Write database header (Page 0)
	db.writeHeader()

	// Initialize usage map (Page 1)
	db.initUsageMap()

	// Create usage map page for table usage maps
	db.createUsageMapPage()

	// Create system tables
	db.createSystemTables()

	return nil
}

// writeHeader writes the database header
func (db *Database) writeHeader() {
	page := db.data[0:db.pageSize]

	// Magic bytes
	copy(page[0:4], MagicMDB)

	// Jet signature
	if db.version >= JetVersion5 {
		copy(page[4:], []byte("Standard ACE DB\x00"))
	} else {
		copy(page[4:], []byte("Standard Jet DB\x00"))
	}

	// Version byte
	page[OffsetJetVersion] = byte(db.version)

	// Encoding key (0 = no encoding, critical for Jackcess compatibility)
	writeUint32(page, OffsetEncryptionKey, 0)

	// Collation (General = 0x0409)
	writeUint16(page, OffsetCollation, 0x0409)

	// Code page
	if db.version >= JetVersion5 {
		// Unicode for ACCDB
		writeUint16(page, OffsetCodePage, 0)
	} else {
		// 1252 = Windows Western for older versions
		writeUint16(page, OffsetCodePage, 1252)
	}

	// Creation date as OLE date
	oleDate := writeDateTime(db.createdAt)
	binary.LittleEndian.PutUint64(page[OffsetCreationDate:], math.Float64bits(oleDate))

	// Jet4/ACE applies an additional repeating mask, derived from the integer
	// part of the creation date, to the 40-byte password area. An empty
	// password must contain this mask before the standard header XOR is applied.
	var passwordMask [4]byte
	binary.LittleEndian.PutUint32(passwordMask[:], uint32(int32(oleDate)))
	for i := 0; i < 40; i++ {
		page[OffsetDbKey+i] = passwordMask[i%len(passwordMask)]
	}

	// Apply header mask (XOR) starting at offset 0x18
	// This is required for Jackcess/UCanAccess compatibility
	for i := 0; i < len(HeaderMask) && HeaderMaskOffset+i < db.pageSize; i++ {
		page[HeaderMaskOffset+i] ^= HeaderMask[i]
	}
}

// initUsageMap initializes the page usage map
func (db *Database) initUsageMap() {
	db.usageMap = &UsageMap{
		StartPage: 1,
		PageCount: uint32(len(db.data) / db.pageSize),
		Map:       make([]byte, (len(db.data)/db.pageSize+7)/8),
	}

	// Mark first few pages as used
	db.usageMap.Map[0] = 0x07 // Pages 0, 1, 2 used
}

// createSystemTables creates required system tables
func (db *Database) createSystemTables() {
	// First, create usage map page on page 1
	db.createUsageMapPage()

	// Page 2: MSysObjects table definition
	pageOffset := 2 * db.pageSize
	page := db.data[pageOffset : pageOffset+db.pageSize]

	// Jet4 Table definition page header (63 bytes)
	page[0] = byte(PageTypeTableDef)
	page[1] = 0x01
	writeUint16(page, 2, 0) // Free space
	writeUint32(page, 4, 0) // Next table def page (0 = none)
	writeUint32(page, 8, 0) // TDEF length, filled after the names
	writeUint32(page, 12, 1625)
	writeUint32(page, 16, 0)  // Row count = 0
	writeUint32(page, 20, 1)  // Next auto number
	writeUint32(page, 24, 0)  // Auto number (low bits)
	writeUint32(page, 28, 0)  // Next complex auto number
	writeUint32(page, 32, 0)  // Unknown
	writeUint32(page, 36, 0)  // Unknown
	page[40] = 0x01           // Table type (1 = user/system table)
	writeUint16(page, 41, 11) // Max columns
	writeUint16(page, 43, 5)  // Num var columns
	writeUint16(page, 45, 11) // Num columns
	writeUint32(page, 47, 0)  // Num index slots
	writeUint32(page, 51, 0)  // Num indexes

	// Owned pages usage map reference: (rowNum=0, pageNum=1)
	page[55] = 0    // Row 0
	page[56] = 0x01 // Page number low byte
	page[57] = 0x00 // Page number mid byte
	page[58] = 0x00 // Page number high byte

	// Free space pages usage map reference: (rowNum=1, pageNum=1)
	page[59] = 1    // Row 1
	page[60] = 0x01 // Page number low byte
	page[61] = 0x00 // Page number mid byte
	page[62] = 0x00 // Page number high byte

	// Column definitions start at offset 63
	offset := 63

	// Define MSysObjects columns
	sysColumns := []ColumnDef{
		{Name: "Id", Type: ColTypeLongInt, Nullable: false},
		{Name: "Name", Type: ColTypeText, Length: 64, Nullable: false},
		{Name: "Type", Type: ColTypeInt, Nullable: false},
		{Name: "ParentId", Type: ColTypeLongInt, Nullable: false},
		{Name: "DateCreate", Type: ColTypeDateTime, Nullable: true},
		{Name: "DateUpdate", Type: ColTypeDateTime, Nullable: true},
		{Name: "Owner", Type: ColTypeText, Length: 64, Nullable: true},
		{Name: "Flags", Type: ColTypeLongInt, Nullable: true},
		{Name: "LvProp", Type: ColTypeOLE, Nullable: true},
		{Name: "Database", Type: ColTypeText, Length: 255, Nullable: true},
		{Name: "ForeignName", Type: ColTypeText, Length: 255, Nullable: true},
	}

	// Write fixed-size ACE column definitions, followed by the names.
	var fixedOffset uint16
	var varIndex uint16
	for i, col := range sysColumns {
		offset = db.writeColumnDef(page, offset, col, uint16(i), fixedOffset, varIndex)
		if isVariableLength(col.Type) {
			varIndex++
		} else {
			fixedOffset += uint16(getFixedColumnSize(col.Type))
		}
	}

	// After columns, write column names
	for _, col := range sysColumns {
		nameBytes := writeUTF16String(col.Name, len([]rune(col.Name))*2)
		writeUint16(page, offset, uint16(len(nameBytes)))
		offset += 2
		copy(page[offset:], nameBytes)
		offset += len(nameBytes)
	}
	writeUint16(page, offset, 0xFFFF)
	offset += 2
	writeUint32(page, 8, uint32(offset))

	// Register in internal map
	sysDetails := &Table{
		Name:    "MSysObjects",
		ID:      SysTableMSysObjects,
		defPage: 2,
		db:      db,
	}
	db.tables["MSysObjects"] = sysDetails

	for i, col := range sysColumns {
		c := &Column{
			Name:          col.Name,
			Type:          col.Type,
			ID:            uint16(i + 1),
			Index:         uint16(i),
			Nullable:      col.Nullable,
			AutoIncrement: false,
		}
		if col.Length > 0 {
			c.Length = uint16(col.Length)
		} else {
			c.Length = uint16(getFixedColumnSize(col.Type))
		}
		sysDetails.Columns = append(sysDetails.Columns, c)
	}
	sysDetails.calculateOffsets()

	// Allocate data page for MSysObjects
	dataPageNum, _ := db.allocatePage()
	sysDetails.DataPages = append(sysDetails.DataPages, dataPageNum)

	dataPageOffset := int(dataPageNum) * db.pageSize
	dataPage := db.data[dataPageOffset : dataPageOffset+db.pageSize]
	dataPage[0] = byte(PageTypeData)
	writeUint16(dataPage, 2, uint16(db.pageSize-14))
	writeUint32(dataPage, 4, SysTableMSysObjects)
	writeUint16(dataPage, 12, 0)

	// The two inline maps on page 1 must expose the catalog data page.
	for _, rowStart := range []int{db.pageSize - 70, db.pageSize - 140} {
		bitmapOffset := rowStart + 5 + int(dataPageNum)/8
		db.data[db.pageSize+bitmapOffset] |= 1 << (dataPageNum % 8)
	}

	_ = sysDetails.Insert(map[string]interface{}{
		"Id":       int32(0x0F000001),
		"Name":     "Tables",
		"Type":     int16(3),
		"ParentId": int32(0x0F000000),
		"Flags":    int32(-2147483648),
	})
	db.addToMSysObjects(sysDetails)
}

// createUsageMapPage creates page 1 as a data page containing usage map rows
func (db *Database) createUsageMapPage() {
	pageOffset := 1 * db.pageSize
	page := db.data[pageOffset : pageOffset+db.pageSize]

	// Usage map row structure:
	// - 1 byte: MAP_TYPE_INLINE (0x00)
	// - 4 bytes: start page
	// - 64 bytes: bitmap (for Jet4)
	// Total: 69 bytes per row

	// Row 0 must be nearest the page end. For row N, Jackcess derives the
	// row end from row N-1, so subsequent rows have decreasing offsets.
	row0Start := db.pageSize - 70 // Row 0 data (69 bytes + 1 padding)
	row1Start := row0Start - 70   // Row 1 data

	// Write row 0 data (owned pages inline usage map)
	page[row0Start] = 0x00            // MAP_TYPE_INLINE
	writeUint32(page, row0Start+1, 0) // Start page = 0
	// Remaining 64 bytes are zeroed (no pages used initially in this map)

	// Write row 1 data (free space inline usage map)
	page[row1Start] = 0x00            // MAP_TYPE_INLINE
	writeUint32(page, row1Start+1, 0) // Start page = 0

	// Data page header (Jet4 format)
	page[0] = byte(PageTypeData)
	page[1] = 0x01                               // Flags
	writeUint16(page, 2, uint16(row0Start-14-4)) // Free space (from end of row table to start of row data)
	writeUint32(page, 4, 0)                      // Owner table ID (0 = system usage map)
	writeUint32(page, 8, 0)                      // Unknown
	writeUint16(page, 12, 2)                     // Record count = 2

	// Row offset table starts at byte 14 (right after row count)
	// Each row offset is 2 bytes, pointing to row data from page start
	writeUint16(page, 14, uint16(row0Start)) // Row 0 offset
	writeUint16(page, 16, uint16(row1Start)) // Row 1 offset
}

// writeColumnDef writes a column definition to a page
func (db *Database) writeColumnDef(page []byte, offset int, col ColumnDef, colIndex, fixedOffset, varIndex uint16) int {
	start := offset

	// Column type
	page[offset] = byte(col.Type)
	offset++

	writeUint32(page, offset, 1625)
	offset += 4

	// Column number and variable-data index.
	writeUint16(page, offset, colIndex)
	offset += 2
	if isVariableLength(col.Type) {
		writeUint16(page, offset, varIndex)
	}
	offset += 2
	writeUint16(page, offset, colIndex)
	offset += 2

	// Sort order/scale/precision area.
	offset += 4

	flags := byte(0x02) // updatable
	if !isVariableLength(col.Type) {
		flags |= 0x01
	}
	if col.AutoIncrement {
		flags |= 0x04
	}
	page[offset] = flags
	offset++
	if col.Type == ColTypeText || col.Type == ColTypeMemo {
		page[offset] = 0x01 // compressed unicode
	}
	offset++

	offset += 4
	if !isVariableLength(col.Type) {
		writeUint16(page, offset, fixedOffset)
	}
	offset += 2

	length := col.Length
	if length == 0 {
		length = getFixedColumnSize(col.Type)
	}
	if col.Type == ColTypeText {
		length *= 2
	}
	writeUint16(page, offset, uint16(length))
	offset += 2

	return start + 25
}

// loadSystemTables loads system table information
func (db *Database) loadSystemTables() error {
	if len(db.data) < 3*db.pageSize {
		return ErrInvalidData
	}
	sysTable, err := db.parseTableDef(2, db.data[2*db.pageSize:3*db.pageSize])
	if err != nil {
		return err
	}
	sysTable.Name = "MSysObjects"
	db.tables[sysTable.Name] = sysTable
	return db.loadTablesFromMSysObjects(sysTable)
}

// parseTableDef parses a table definition page
func (db *Database) parseTableDef(pageNum int, page []byte) (*Table, error) {
	if len(page) < 63 || PageType(page[0]) != PageTypeTableDef || readUint32(page, 12) != 1625 {
		return nil, ErrInvalidData
	}

	table := &Table{
		ID:           uint32(pageNum),
		defPage:      uint32(pageNum),
		RowCount:     readUint32(page, 16),
		AutoNumber:   readUint32(page, 20),
		usageMapPage: uint32(page[56]) | uint32(page[57])<<8 | uint32(page[58])<<16,
		ownedMapRow:  page[55],
		freeSpaceRow: page[59],
		db:           db,
	}

	colCount := int(readUint16(page, 45))
	indexCount := int(readUint32(page, 51))
	colOffset := 63 + indexCount*12
	nameOffset := colOffset + colCount*25
	if colOffset < 63 || nameOffset > len(page) {
		return nil, ErrInvalidData
	}

	names := make([]string, colCount)
	for i := range names {
		if nameOffset+2 > len(page) {
			return nil, ErrInvalidData
		}
		nameLen := int(readUint16(page, nameOffset))
		nameOffset += 2
		if nameLen < 0 || nameOffset+nameLen > len(page) {
			return nil, ErrInvalidData
		}
		names[i] = readUTF16String(page, nameOffset, nameLen)
		nameOffset += nameLen
	}

	for i := 0; i < colCount; i++ {
		offset := colOffset + i*25
		flags := page[offset+15]
		col := &Column{
			Name:          names[i],
			Type:          ColumnType(page[offset]),
			ID:            readUint16(page, offset+5) + 1,
			Index:         readUint16(page, offset+5),
			Offset:        readUint16(page, offset+21),
			Length:        readUint16(page, offset+23),
			Flags:         uint16(flags),
			Nullable:      true,
			AutoIncrement: flags&0x04 != 0,
		}
		if isVariableLength(col.Type) {
			col.Offset = readUint16(page, offset+7)
		}
		if col.Type == ColTypeText {
			col.Length /= 2
		}
		table.Columns = append(table.Columns, col)
	}

	// Parse indexes if any
	if indexCount > 0 {
		idxDataOffset := nameOffset
		idxImplOffset := idxDataOffset + indexCount*52
		idxNameOffset := idxImplOffset + indexCount*28

		for i := 0; i < indexCount; i++ {
			dataPos := idxDataOffset + i*52
			if dataPos+52 > len(page) {
				break
			}
			rootPage := readUint32(page, dataPos+38)
			flags := page[dataPos+46]

			idx := &Index{
				ID:          uint32(i),
				RootPage:    rootPage,
				Unique:      flags&0x01 != 0,
				Primary:     flags&0x02 != 0,
				IgnoreNulls: flags&0x04 != 0,
				Table:       table,
			}

			// Read columns from 10 slots
			for c := 0; c < 10; c++ {
				colPos := dataPos + 4 + c*3
				colNum := readUint16(page, colPos)
				if colNum != 0xFFFF {
					asc := page[colPos+2] == 0x01
					for _, col := range table.Columns {
						if col.Index == colNum {
							idx.Columns = append(idx.Columns, IndexColumn{
								ColumnID:  col.ID,
								Ascending: asc,
							})
							break
						}
					}
				}
			}

			// Read index name
			if idxNameOffset+2 <= len(page) {
				nameLen := int(readUint16(page, idxNameOffset))
				idxNameOffset += 2
				if nameLen > 0 && idxNameOffset+nameLen <= len(page) {
					idx.Name = readUTF16String(page, idxNameOffset, nameLen)
					idxNameOffset += nameLen
				}
			}

			if idx.Name == "" {
				if idx.Primary {
					idx.Name = "PrimaryKey"
				} else {
					idx.Name = fmt.Sprintf("Index_%d", i)
				}
			}

			if rootPage != 0 {
				db.readIndexLeafPage(rootPage, idx)
			}

			table.Indexes = append(table.Indexes, idx)
		}
	}

	table.DataPages = db.readInlineUsageMap(table.usageMapPage, table.ownedMapRow)

	return table, nil
}

func (db *Database) readInlineUsageMap(pageNum uint32, rowNum byte) []uint32 {
	if pageNum == 0 || int(pageNum+1)*db.pageSize > len(db.data) {
		return nil
	}
	page := db.data[int(pageNum)*db.pageSize : int(pageNum+1)*db.pageSize]
	rowOffsetPos := 14 + int(rowNum)*2
	if rowOffsetPos+2 > len(page) {
		return nil
	}
	rowStart := int(readUint16(page, rowOffsetPos) & 0x0FFF)
	if rowStart+69 > len(page) || page[rowStart] != 0x00 {
		return nil
	}
	startPage := readUint32(page, rowStart+1)
	var pages []uint32
	for byteIndex, bits := range page[rowStart+5 : rowStart+69] {
		for bit := uint(0); bit < 8; bit++ {
			if bits&(1<<bit) != 0 {
				pages = append(pages, startPage+uint32(byteIndex*8)+uint32(bit))
			}
		}
	}
	return pages
}

// loadTablesFromMSysObjects loads table information from MSysObjects
func (db *Database) loadTablesFromMSysObjects(sysTable *Table) error {
	iter, err := sysTable.Rows()
	if err != nil {
		return err
	}
	for iter.Next() {
		row := iter.Row()
		name := row.GetString("Name")
		pageNum := row.GetInt("Id")
		if name == "" || name == "MSysObjects" || row.GetInt("Type") != 1 || pageNum <= 0 {
			continue
		}
		pageOffset := pageNum * db.pageSize
		if pageOffset+db.pageSize > len(db.data) {
			continue
		}
		table, err := db.parseTableDef(pageNum, db.data[pageOffset:pageOffset+db.pageSize])
		if err != nil {
			continue
		}
		table.Name = name
		db.tables[name] = table
	}
	return iter.Err()
}

// targetMode retrieves existing file permissions or defaults to 0600 for new files
func targetMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode().Perm(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return 0600, nil
	}
	return 0, err
}

// atomicWriteFile writes data to a temporary file, syncs it, renames atomically, and syncs the directory
func atomicWriteFile(path string, data []byte) error {
	mode, err := targetMode(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	temp, err := os.CreateTemp(dir, ".accdb-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}

	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}

	if err := temp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tempName, path); err != nil {
		return err
	}

	// Sync directory on Unix platforms
	dirFile, err := os.Open(dir)
	if err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}

	return nil
}

// Save writes the database to disk atomically after verifying writable status and validation
func (db *Database) Save() error {
	if err := db.ensureWritable(); err != nil {
		return err
	}
	if err := db.Validate(); err != nil {
		return err
	}
	return atomicWriteFile(db.path, db.data)
}

// SaveAs writes the database to a new file atomically and updates db.path upon success
func (db *Database) SaveAs(path string) error {
	if err := db.ensureWritable(); err != nil {
		return err
	}
	if err := db.Validate(); err != nil {
		return err
	}
	if err := atomicWriteFile(path, db.data); err != nil {
		return err
	}
	db.path = path
	return nil
}

// IsReadOnly returns whether the database was opened in read-only mode
func (db *Database) IsReadOnly() bool {
	if db == nil {
		return false
	}
	return db.readOnly
}

// ensureWritable checks that the database is not in read-only mode
func (db *Database) ensureWritable() error {
	if db == nil {
		return ErrInvalidData
	}
	if db.readOnly {
		return ErrReadOnly
	}
	return nil
}

// deepClone creates an independent in-memory clone of the database
func (db *Database) deepClone() (*Database, error) {
	cloneData := make([]byte, len(db.data))
	copy(cloneData, db.data)

	cloneDB := &Database{
		path:       db.path,
		version:    db.version,
		profile:    db.profile,
		pageSize:   db.pageSize,
		encoding:   db.encoding,
		codePage:   db.codePage,
		encrypted:  db.encrypted,
		encType:    db.encType,
		password:   db.password,
		readOnly:   db.readOnly,
		data:       cloneData,
		tables:     make(map[string]*Table),
		createdAt:  db.createdAt,
		modifiedAt: time.Now(),
	}

	if db.usageMap != nil {
		clonedMap := make([]byte, len(db.usageMap.Map))
		copy(clonedMap, db.usageMap.Map)
		cloneDB.usageMap = &UsageMap{
			StartPage: db.usageMap.StartPage,
			PageCount: db.usageMap.PageCount,
			Map:       clonedMap,
		}
	} else if cloneDB.pageSize > 0 {
		if err := cloneDB.initUsageMapFromLoaded(); err != nil {
			return nil, err
		}
	}

	store, err := NewMemoryPageStore(cloneDB.pageSize, cloneDB.data)
	if err != nil {
		return nil, err
	}
	cloneDB.pageStore = store

	if !cloneDB.encrypted {
		if err := cloneDB.loadSystemTables(); err != nil {
			return nil, err
		}
	}

	return cloneDB, nil
}

// replaceState replaces the database's internal state with that of a committed working database
func (db *Database) replaceState(working *Database) {
	db.data = working.data
	db.usageMap = working.usageMap
	db.pageStore = working.pageStore
	db.modifiedAt = time.Now()

	if db.tables == nil {
		db.tables = make(map[string]*Table)
	}

	// 1. Remove dropped tables from db.tables
	for name := range db.tables {
		found := false
		for wName := range working.tables {
			if strings.EqualFold(name, wName) {
				found = true
				break
			}
		}
		if !found {
			delete(db.tables, name)
		}
	}

	// 2. In-place update existing table pointers and add new tables
	for wName, wTbl := range working.tables {
		var target *Table
		for name, tbl := range db.tables {
			if strings.EqualFold(name, wName) {
				target = tbl
				break
			}
		}

		if target != nil {
			*target = *wTbl
			target.db = db
			for _, idx := range target.Indexes {
				idx.Table = target
			}
		} else {
			newTbl := wTbl
			newTbl.db = db
			for _, idx := range newTbl.Indexes {
				idx.Table = newTbl
			}
			db.tables[wName] = newTbl
		}
	}
}

// withWriteTransaction runs fn within an isolated copy-on-write transaction and commits atomically
func (db *Database) withWriteTransaction(fn func(working *Database) error) error {
	if err := db.ensureWritable(); err != nil {
		return err
	}

	working, err := db.deepClone()
	if err != nil {
		return err
	}

	if err := fn(working); err != nil {
		return err
	}

	if err := working.Validate(); err != nil {
		return err
	}

	db.replaceState(working)
	return nil
}

// Validate checks database page alignment, table TDEFs, catalog entries, and counts
func (db *Database) Validate() error {
	if db.pageSize <= 0 || len(db.data)%db.pageSize != 0 {
		return fmt.Errorf("%w: invalid database file size alignment", ErrCorruptDatabase)
	}

	if len(db.data) < db.pageSize*2 {
		return fmt.Errorf("%w: database smaller than required system header pages", ErrCorruptDatabase)
	}

	// Validate MSysObjects table
	sysTable := db.tables["MSysObjects"]
	if sysTable == nil {
		return fmt.Errorf("%w: missing MSysObjects catalog", ErrCorruptDatabase)
	}

	// Validate each registered table
	for name, tbl := range db.tables {
		if tbl.defPage == 0 || int(tbl.defPage)*db.pageSize >= len(db.data) {
			return fmt.Errorf("%w: table %s def page %d out of bounds", ErrCorruptDatabase, name, tbl.defPage)
		}
		for _, dp := range tbl.DataPages {
			if int(dp)*db.pageSize >= len(db.data) {
				return fmt.Errorf("%w: table %s data page %d out of bounds", ErrCorruptDatabase, name, dp)
			}
		}
	}

	return nil
}

// Close closes the database
func (db *Database) Close() error {
	db.data = nil
	db.tables = nil
	return nil
}

// Path returns the database file path
func (db *Database) Path() string {
	return db.path
}

// Version returns the Jet version
func (db *Database) Version() JetVersion {
	return db.version
}

// IsEncrypted returns whether the database is encrypted
func (db *Database) IsEncrypted() bool {
	return db.encrypted
}
