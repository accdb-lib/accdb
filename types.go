package accdb

import (
	"encoding/binary"
	"time"
)

// Database represents an Access database file
type Database struct {
	path      string
	version   JetVersion
	profile   FormatProfile
	pageSize  int
	encoding  binary.ByteOrder
	codePage  uint16
	encrypted bool
	encType   EncryptionType
	password  string
	readOnly  bool

	// Internal state
	data      []byte
	pageStore PageStore
	pages     []Page
	tables    map[string]*Table
	usageMap  *UsageMap

	// Metadata
	createdAt  time.Time
	modifiedAt time.Time
}

// Page represents a database page
type Page struct {
	Type      PageType
	Index     uint32
	Data      []byte
	FreeSpace uint16
}

// Table represents a database table
type Table struct {
	Name       string
	ID         uint32
	RowCount   uint32
	AutoNumber uint32
	Columns    []*Column
	Indexes    []*Index
	DataPages  []uint32

	// Internal
	defPage      uint32
	usageMapPage uint32
	ownedMapRow  byte
	freeSpaceRow byte
	db           *Database
}

// Column represents a table column
type Column struct {
	Name          string
	Type          ColumnType
	ID            uint16
	Index         uint16
	Offset        uint16
	Length        uint16
	Precision     byte
	Scale         byte
	Flags         uint16
	Nullable      bool
	AutoIncrement bool
	DefaultValue  interface{}
}

// RowLocation points to a specific row in a data page
type RowLocation struct {
	PageNumber uint32
	RowNumber  uint16
}

// IndexEntry represents an entry within an index
type IndexEntry struct {
	Key      []byte
	Location RowLocation
}

// Index represents a table index
type Index struct {
	Name        string
	ID          uint32
	Columns     []IndexColumn
	Unique      bool
	Primary     bool
	Foreign     bool
	IgnoreNulls bool
	RootPage    uint32
	Table       *Table
	Entries     []IndexEntry
}

// IndexColumn represents a column in an index
type IndexColumn struct {
	ColumnID  uint16
	Ascending bool
}

// Row offset table flags for slotted data pages
const (
	rowOffsetMask   uint16 = 0x1FFF
	rowOverflowMask uint16 = 0x4000
	rowDeletedMask  uint16 = 0x8000
)

// Row represents a table row
type Row struct {
	Values   map[string]interface{}
	table    *Table
	location RowLocation
	deleted  bool
}

// Location returns the physical page and row number of the row
func (r *Row) Location() RowLocation {
	return r.location
}

// Table returns the table this row belongs to
func (r *Row) Table() *Table {
	return r.table
}

// IsDeleted returns true if this row has been marked deleted
func (r *Row) IsDeleted() bool {
	return r.deleted
}

// UsageMap tracks page usage
type UsageMap struct {
	StartPage uint32
	PageCount uint32
	Map       []byte
}

// QueryDef represents a stored query
type QueryDef struct {
	Name string
	SQL  string
	Type QueryType
}

// QueryType defines query types
type QueryType byte

const (
	QuerySelect    QueryType = 0
	QueryCrossTab  QueryType = 1
	QueryDelete    QueryType = 2
	QueryUpdate    QueryType = 3
	QueryAppend    QueryType = 4
	QueryMakeTable QueryType = 5
)

// Relation represents a table relationship
type Relation struct {
	Name        string
	FromTable   string
	ToTable     string
	FromColumns []string
	ToColumns   []string
	Flags       uint32
	Cascade     bool
}

// TableDef represents table definition for creation
type TableDef struct {
	Name    string
	Columns []ColumnDef
	Indexes []IndexDef
}

// ColumnDef represents column definition for creation
type ColumnDef struct {
	Name          string
	Type          ColumnType
	Length        int
	Nullable      bool
	AutoIncrement bool
	DefaultValue  interface{}
}

// IndexDef represents index definition for creation
type IndexDef struct {
	Name    string
	Columns []string
	Unique  bool
	Primary bool
}
