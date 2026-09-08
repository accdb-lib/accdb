package accdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrDuplicateKey  = errors.New("duplicate key violates unique constraint")
	ErrIndexNotFound = errors.New("index not found")
	ErrNoPrimaryKey  = errors.New("table has no primary key")
	ErrRowNotFound   = errors.New("row not found")
	ErrIndexPageFull = errors.New("index page full (multi-page B-tree indexing not yet implemented)")
)

// ColumnByID finds a column in the table by its ID
func (table *Table) ColumnByID(id uint16) *Column {
	for _, col := range table.Columns {
		if col.ID == id {
			return col
		}
	}
	return nil
}

// EncodeKey generates an index key from a row's values map
func (idx *Index) EncodeKey(values map[string]interface{}) ([]byte, error) {
	if len(idx.Columns) == 0 {
		return nil, errors.New("index has no columns defined")
	}

	var keyBuf bytes.Buffer
	for _, idxCol := range idx.Columns {
		col := idx.Table.ColumnByID(idxCol.ColumnID)
		if col == nil {
			return nil, fmt.Errorf("index column ID %d not found in table", idxCol.ColumnID)
		}

		val := values[col.Name]
		codec := GetCodec(col.Type)
		colKey, err := codec.EncodeKey(val, col)
		if err != nil {
			return nil, fmt.Errorf("failed to encode key for column %s: %w", col.Name, err)
		}

		// If descending, bitwise invert all bytes
		if !idxCol.Ascending {
			for i := range colKey {
				colKey[i] = ^colKey[i]
			}
		}

		// In composite indexes, length-prefix each column's key bytes to avoid ambiguity
		if len(idx.Columns) > 1 {
			lengthBuf := make([]byte, 2)
			binary.BigEndian.PutUint16(lengthBuf, uint16(len(colKey)))
			keyBuf.Write(lengthBuf)
		}
		keyBuf.Write(colKey)
	}

	return keyBuf.Bytes(), nil
}

// EncodeKeyFromValue generates a key for a single-column index from a standalone value
func (idx *Index) EncodeKeyFromValue(val interface{}) ([]byte, error) {
	if len(idx.Columns) != 1 {
		return nil, errors.New("EncodeKeyFromValue requires a single-column index")
	}
	col := idx.Table.ColumnByID(idx.Columns[0].ColumnID)
	if col == nil {
		return nil, errors.New("index column not found in table")
	}
	codec := GetCodec(col.Type)
	key, err := codec.EncodeKey(val, col)
	if err != nil {
		return nil, err
	}
	if !idx.Columns[0].Ascending {
		for i := range key {
			key[i] = ^key[i]
		}
	}
	return key, nil
}

// ContainsKey returns true if the key already exists in the index
func (idx *Index) ContainsKey(key []byte) bool {
	i := sort.Search(len(idx.Entries), func(i int) bool {
		return bytes.Compare(idx.Entries[i].Key, key) >= 0
	})
	return i < len(idx.Entries) && bytes.Equal(idx.Entries[i].Key, key)
}

// Find looks up a key in the index and returns the RowLocation if found
func (idx *Index) Find(key []byte) (*RowLocation, bool) {
	i := sort.Search(len(idx.Entries), func(i int) bool {
		return bytes.Compare(idx.Entries[i].Key, key) >= 0
	})
	if i < len(idx.Entries) && bytes.Equal(idx.Entries[i].Key, key) {
		return &idx.Entries[i].Location, true
	}
	return nil, false
}

// Insert adds a new entry to the index, maintaining sorted order and unique constraint
func (idx *Index) Insert(entry IndexEntry) error {
	i := sort.Search(len(idx.Entries), func(i int) bool {
		return bytes.Compare(idx.Entries[i].Key, entry.Key) >= 0
	})

	if i < len(idx.Entries) && bytes.Equal(idx.Entries[i].Key, entry.Key) {
		if idx.Unique || idx.Primary {
			return ErrDuplicateKey
		}
	}

	// Check page capacity if bound to a database page
	if idx.Table != nil && idx.Table.db != nil && idx.RootPage != 0 {
		required := 14
		for _, e := range idx.Entries {
			required += 2 + len(e.Key) + 4 + 2
		}
		required += 2 + len(entry.Key) + 4 + 2
		if required > idx.Table.db.pageSize {
			return ErrIndexPageFull
		}
	}

	// Insert in sorted order
	idx.Entries = append(idx.Entries, IndexEntry{})
	copy(idx.Entries[i+1:], idx.Entries[i:])
	idx.Entries[i] = entry

	// If index is bound to a database page, persist to leaf index page
	if idx.Table != nil && idx.Table.db != nil && idx.RootPage != 0 {
		idx.Table.db.writeIndexLeafPage(idx)
	}

	return nil
}

// EntriesList returns a copy of the index entries
func (idx *Index) EntriesList() []IndexEntry {
	out := make([]IndexEntry, len(idx.Entries))
	copy(out, idx.Entries)
	return out
}

// IndexChange represents a planned modification to an index
type IndexChange struct {
	Index  *Index
	Remove []IndexEntry
	Add    []IndexEntry
}

// removeEntry removes a specific entry matching key and location
func (idx *Index) removeEntry(key []byte, loc RowLocation) bool {
	i := sort.Search(len(idx.Entries), func(i int) bool {
		return bytes.Compare(idx.Entries[i].Key, key) >= 0
	})
	for i < len(idx.Entries) && bytes.Equal(idx.Entries[i].Key, key) {
		if idx.Entries[i].Location == loc {
			idx.Entries = append(idx.Entries[:i], idx.Entries[i+1:]...)
			if idx.Table != nil && idx.Table.db != nil && idx.RootPage != 0 {
				idx.Table.db.writeIndexLeafPage(idx)
			}
			return true
		}
		i++
	}
	return false
}

// PrepareInsert validates and plans an index insertion
func (idx *Index) PrepareInsert(values map[string]interface{}, loc RowLocation) (*IndexChange, error) {
	key, err := idx.EncodeKey(values)
	if err != nil {
		return nil, err
	}

	if idx.Unique || idx.Primary {
		if idx.ContainsKey(key) {
			return nil, ErrDuplicateKey
		}
	}

	if idx.Table != nil && idx.Table.db != nil && idx.RootPage != 0 {
		required := 14
		for _, e := range idx.Entries {
			required += 2 + len(e.Key) + 4 + 2
		}
		required += 2 + len(key) + 4 + 2
		if required > idx.Table.db.pageSize {
			return nil, ErrIndexPageFull
		}
	}

	return &IndexChange{
		Index: idx,
		Add:   []IndexEntry{{Key: key, Location: loc}},
	}, nil
}

// PrepareDelete validates and plans removing a row's index entries
func (idx *Index) PrepareDelete(values map[string]interface{}, loc RowLocation) (*IndexChange, error) {
	key, err := idx.EncodeKey(values)
	if err != nil {
		return nil, err
	}

	return &IndexChange{
		Index:  idx,
		Remove: []IndexEntry{{Key: key, Location: loc}},
	}, nil
}

// PrepareUpdate validates and plans changing an index entry
func (idx *Index) PrepareUpdate(oldValues, newValues map[string]interface{}, loc RowLocation) (*IndexChange, error) {
	oldKey, err := idx.EncodeKey(oldValues)
	if err != nil {
		return nil, err
	}
	newKey, err := idx.EncodeKey(newValues)
	if err != nil {
		return nil, err
	}

	if bytes.Equal(oldKey, newKey) {
		return nil, nil // No change needed
	}

	if idx.Unique || idx.Primary {
		if existingLoc, found := idx.Find(newKey); found && *existingLoc != loc {
			return nil, ErrDuplicateKey
		}
	}

	// Check leaf page capacity for the diff
	if idx.Table != nil && idx.Table.db != nil && idx.RootPage != 0 {
		required := 14
		for _, e := range idx.Entries {
			if bytes.Equal(e.Key, oldKey) && e.Location == loc {
				continue
			}
			required += 2 + len(e.Key) + 4 + 2
		}
		required += 2 + len(newKey) + 4 + 2
		if required > idx.Table.db.pageSize {
			return nil, ErrIndexPageFull
		}
	}

	return &IndexChange{
		Index:  idx,
		Remove: []IndexEntry{{Key: oldKey, Location: loc}},
		Add:    []IndexEntry{{Key: newKey, Location: loc}},
	}, nil
}

func (table *Table) prepareIndexInsert(values map[string]interface{}, loc RowLocation) ([]*IndexChange, error) {
	changes := make([]*IndexChange, 0, len(table.Indexes))
	for _, idx := range table.Indexes {
		ch, err := idx.PrepareInsert(values, loc)
		if err != nil {
			return nil, err
		}
		if ch != nil {
			changes = append(changes, ch)
		}
	}
	return changes, nil
}

func (table *Table) prepareIndexDelete(values map[string]interface{}, loc RowLocation) ([]*IndexChange, error) {
	changes := make([]*IndexChange, 0, len(table.Indexes))
	for _, idx := range table.Indexes {
		ch, err := idx.PrepareDelete(values, loc)
		if err != nil {
			return nil, err
		}
		if ch != nil {
			changes = append(changes, ch)
		}
	}
	return changes, nil
}

func (table *Table) prepareIndexUpdate(oldValues, newValues map[string]interface{}, loc RowLocation) ([]*IndexChange, error) {
	changes := make([]*IndexChange, 0, len(table.Indexes))
	for _, idx := range table.Indexes {
		ch, err := idx.PrepareUpdate(oldValues, newValues, loc)
		if err != nil {
			return nil, err
		}
		if ch != nil {
			changes = append(changes, ch)
		}
	}
	return changes, nil
}

func commitIndexChanges(changes []*IndexChange) {
	for _, ch := range changes {
		if ch == nil || ch.Index == nil {
			continue
		}
		for _, rem := range ch.Remove {
			ch.Index.removeEntry(rem.Key, rem.Location)
		}
		for _, add := range ch.Add {
			_ = ch.Index.Insert(add)
		}
	}
}

// FindByPrimaryKey finds a row using the primary key index
func (table *Table) FindByPrimaryKey(val interface{}) (*Row, error) {
	pk := table.PrimaryKey()
	if pk == nil {
		return nil, ErrNoPrimaryKey
	}

	key, err := pk.EncodeKeyFromValue(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode primary key: %w", err)
	}

	loc, found := pk.Find(key)
	if !found {
		return nil, ErrRowNotFound
	}

	return table.RowAt(*loc)
}

// FindByIndex finds a row using a named index
func (table *Table) FindByIndex(indexName string, val interface{}) (*Row, error) {
	idx := table.Index(indexName)
	if idx == nil {
		return nil, fmt.Errorf("%w: %s", ErrIndexNotFound, indexName)
	}

	key, err := idx.EncodeKeyFromValue(val)
	if err != nil {
		return nil, fmt.Errorf("failed to encode key for index %s: %w", indexName, err)
	}

	loc, found := idx.Find(key)
	if !found {
		return nil, ErrRowNotFound
	}

	return table.RowAt(*loc)
}

// RowAt parses and returns the row at the given location (PageNumber, RowNumber)
func (table *Table) RowAt(loc RowLocation) (*Row, error) {
	db := table.db
	if db == nil {
		return nil, ErrInvalidData
	}
	start, end, err := pageBounds(loc.PageNumber, db.pageSize, len(db.data))
	if err != nil {
		return nil, err
	}

	page := db.data[start:end]
	row, err := table.parseRowAt(page, loc.PageNumber, int(loc.RowNumber), 0)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrRowNotFound
	}
	return row, nil
}

// writeIndexLeafPage serializes an index's entries into its root leaf page
func (db *Database) writeIndexLeafPage(idx *Index) {
	if idx.RootPage == 0 {
		return
	}
	pageOffset := int(idx.RootPage) * db.pageSize
	if pageOffset+db.pageSize > len(db.data) {
		return
	}
	page := db.data[pageOffset : pageOffset+db.pageSize]

	// Header: PageTypeLeafIndex (0x04)
	page[0] = byte(PageTypeLeafIndex)
	page[1] = 0x00 // Leaf level
	if idx.Table != nil {
		writeUint32(page, 4, idx.Table.ID)
	}
	writeUint16(page, 12, uint16(len(idx.Entries))) // Entry count

	// Slotted layout or sequential entries:
	// Entry: 2-byte key length + key bytes + 4-byte pageNum + 2-byte rowNum
	offset := 14
	for _, entry := range idx.Entries {
		entrySize := 2 + len(entry.Key) + 4 + 2
		if offset+entrySize > db.pageSize {
			break // fits in leaf page
		}
		writeUint16(page, offset, uint16(len(entry.Key)))
		offset += 2
		copy(page[offset:], entry.Key)
		offset += len(entry.Key)
		writeUint32(page, offset, entry.Location.PageNumber)
		offset += 4
		writeUint16(page, offset, entry.Location.RowNumber)
		offset += 2
	}
	writeUint16(page, 2, uint16(db.pageSize-offset)) // Free space
}

// readIndexLeafPage deserializes index entries from its leaf page
func (db *Database) readIndexLeafPage(pageNum uint32, idx *Index) {
	pageOffset := int(pageNum) * db.pageSize
	if pageOffset+db.pageSize > len(db.data) {
		return
	}
	page := db.data[pageOffset : pageOffset+db.pageSize]
	if page[0] != byte(PageTypeLeafIndex) {
		return
	}

	entryCount := int(readUint16(page, 12))
	offset := 14
	for i := 0; i < entryCount && offset < db.pageSize-8; i++ {
		keyLen := int(readUint16(page, offset))
		offset += 2
		if offset+keyLen+6 > db.pageSize {
			break
		}
		key := make([]byte, keyLen)
		copy(key, page[offset:offset+keyLen])
		offset += keyLen

		pNum := readUint32(page, offset)
		offset += 4
		rNum := readUint16(page, offset)
		offset += 2

		idx.Entries = append(idx.Entries, IndexEntry{
			Key: key,
			Location: RowLocation{
				PageNumber: pNum,
				RowNumber:  rNum,
			},
		})
	}
}
