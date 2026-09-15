package accdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

const (
	lvalOwner             = 0x4C41564C // "LVAL" in ASCII little-endian
	lvalFlagInline uint32 = 0x80000000
	lvalFlagSingle uint32 = 0x40000000
	lvalFlagMulti  uint32 = 0x00000000
	lvalFlagsMask  uint32 = 0xC0000000
	lvalLenMask    uint32 = 0x3FFFFFFF

	lvalDescriptorLen = 12
	lvalInlineMax     = 64
	lvalChunkMax      = 4076 // Maximum payload per single LVAL slotted row
	lvalPayloadMax    = 4072 // In multi-chunk: 4076 - 4 (next pointer)
)

type longValueRef struct {
	Row  uint8
	Page uint32
}

type longValueDescriptor struct {
	Flags         uint32
	Length        uint32
	Ref           longValueRef
	InlinePayload []byte
}

func parseLongValueDescriptor(data []byte) (longValueDescriptor, error) {
	if len(data) < lvalDescriptorLen {
		return longValueDescriptor{}, ErrCorruptDatabase
	}
	lenAndFlags := binary.LittleEndian.Uint32(data[0:4])
	flags := lenAndFlags & lvalFlagsMask
	length := lenAndFlags & lvalLenMask

	desc := longValueDescriptor{
		Flags:  flags,
		Length: length,
	}

	switch flags {
	case lvalFlagInline:
		if len(data) < lvalDescriptorLen+int(length) {
			return longValueDescriptor{}, ErrCorruptDatabase
		}
		desc.InlinePayload = data[lvalDescriptorLen : lvalDescriptorLen+int(length)]
	case lvalFlagSingle, lvalFlagMulti:
		desc.Ref = longValueRef{
			Row:  data[4],
			Page: uint32(data[5]) | (uint32(data[6]) << 8) | (uint32(data[7]) << 16),
		}
	default:
		return longValueDescriptor{}, ErrCorruptDatabase
	}
	return desc, nil
}

func encodeLongValueDescriptor(desc longValueDescriptor) []byte {
	out := make([]byte, lvalDescriptorLen+len(desc.InlinePayload))
	lenAndFlags := (desc.Flags & lvalFlagsMask) | (desc.Length & lvalLenMask)
	binary.LittleEndian.PutUint32(out[0:4], lenAndFlags)
	if desc.Flags != lvalFlagInline {
		out[4] = desc.Ref.Row
		out[5] = byte(desc.Ref.Page)
		out[6] = byte(desc.Ref.Page >> 8)
		out[7] = byte(desc.Ref.Page >> 16)
	}
	if len(desc.InlinePayload) > 0 {
		copy(out[lvalDescriptorLen:], desc.InlinePayload)
	}
	return out
}

func encodeLongText(s string) []byte {
	u16 := utf16.Encode([]rune(s))
	b := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}

func decodeLongText(data []byte) string {
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return string(data[2:])
	}
	return readUTF16String(data, 0, len(data))
}

func readLongValue(db *Database, desc longValueDescriptor) ([]byte, error) {
	if desc.Length == 0 {
		return []byte{}, nil
	}

	maxSize := int64(DefaultMaxLongValueSize)
	if db != nil && db.maxLongValueSize > 0 {
		maxSize = db.maxLongValueSize
	}
	if int64(desc.Length) > maxSize {
		return nil, ErrLongValueTooLarge
	}

	switch desc.Flags {
	case lvalFlagInline:
		if len(desc.InlinePayload) != int(desc.Length) {
			return nil, ErrCorruptDatabase
		}
		res := make([]byte, desc.Length)
		copy(res, desc.InlinePayload)
		return res, nil

	case lvalFlagSingle:
		return readSingleLongValue(db, desc.Ref, desc.Length)

	case lvalFlagMulti:
		return readMultiLongValue(db, desc.Ref, desc.Length, maxSize)

	default:
		return nil, ErrCorruptDatabase
	}
}

func readSingleLongValue(db *Database, ref longValueRef, declaredLen uint32) ([]byte, error) {
	chunk, err := readLVALSlot(db, ref)
	if err != nil {
		return nil, err
	}
	if uint32(len(chunk)) < declaredLen {
		return nil, ErrCorruptDatabase
	}
	res := make([]byte, declaredLen)
	copy(res, chunk[:declaredLen])
	return res, nil
}

func readMultiLongValue(db *Database, startRef longValueRef, declaredLen uint32, maxSize int64) ([]byte, error) {
	var buf bytes.Buffer
	currRef := startRef
	visited := make(map[uint32]bool)
	maxChunks := int((declaredLen / lvalPayloadMax) + 10)
	if maxChunks < 50 {
		maxChunks = 50
	}

	for chunksRead := 0; chunksRead < maxChunks; chunksRead++ {
		if currRef.Page == 0 {
			break
		}
		if visited[currRef.Page] {
			return nil, fmt.Errorf("%w: circular chain detected in long value", ErrCorruptDatabase)
		}
		visited[currRef.Page] = true

		chunk, err := readLVALSlot(db, currRef)
		if err != nil {
			return nil, err
		}
		if len(chunk) < 4 {
			return nil, ErrCorruptDatabase
		}

		nextRow := chunk[0]
		nextPage := uint32(chunk[1]) | (uint32(chunk[2]) << 8) | (uint32(chunk[3]) << 16)
		payload := chunk[4:]

		if int64(buf.Len()+len(payload)) > maxSize {
			return nil, ErrLongValueTooLarge
		}
		buf.Write(payload)

		currRef = longValueRef{Row: nextRow, Page: nextPage}
	}

	if currRef.Page != 0 {
		return nil, fmt.Errorf("%w: chunk chain exceeded maximum allowed chunks", ErrCorruptDatabase)
	}

	res := buf.Bytes()
	if uint32(len(res)) < declaredLen {
		return nil, fmt.Errorf("%w: chain returned %d bytes, declared %d", ErrCorruptDatabase, len(res), declaredLen)
	}
	return res[:declaredLen], nil
}

func readLVALSlot(db *Database, ref longValueRef) ([]byte, error) {
	if db == nil {
		return nil, ErrInvalidData
	}
	start, end, err := pageBounds(ref.Page, db.pageSize, len(db.data))
	if err != nil {
		return nil, err
	}
	page := db.data[start:end]

	if page[0] != byte(PageTypeData) || binary.LittleEndian.Uint32(page[4:8]) != lvalOwner {
		return nil, fmt.Errorf("%w: page %d is not an LVAL page", ErrCorruptDatabase, ref.Page)
	}

	recCount := int(binary.LittleEndian.Uint16(page[12:14]))
	if int(ref.Row) >= recCount {
		return nil, fmt.Errorf("%w: row %d out of bounds on LVAL page %d (recCount=%d)",
			ErrCorruptDatabase, ref.Row, ref.Page, recCount)
	}

	slotPos := 14 + int(ref.Row)*2
	rawOffset := binary.LittleEndian.Uint16(page[slotPos:])
	if (rawOffset&rowDeletedMask != 0) && (rawOffset&rowOverflowMask != 0) {
		return nil, fmt.Errorf("%w: LVAL slot %d on page %d is deleted", ErrCorruptDatabase, ref.Row, ref.Page)
	}

	slotOffset := int(rawOffset & rowOffsetMask)
	rowEnd := db.pageSize
	if ref.Row > 0 {
		rowEnd = int(binary.LittleEndian.Uint16(page[14+int(ref.Row-1)*2:]) & rowOffsetMask)
	}

	if slotOffset < 14+recCount*2 || slotOffset > rowEnd || rowEnd > db.pageSize {
		return nil, fmt.Errorf("%w: invalid slot offsets %d..%d on LVAL page %d",
			ErrCorruptDatabase, slotOffset, rowEnd, ref.Page)
	}

	return page[slotOffset:rowEnd], nil
}

func writeLongValue(db *Database, data []byte) (longValueDescriptor, error) {
	if len(data) <= lvalInlineMax {
		return longValueDescriptor{
			Flags:         lvalFlagInline,
			Length:        uint32(len(data)),
			InlinePayload: data,
		}, nil
	}

	if len(data) <= lvalChunkMax {
		ref, err := allocateLVALSlot(db, data)
		if err != nil {
			return longValueDescriptor{}, err
		}
		return longValueDescriptor{
			Flags:  lvalFlagSingle,
			Length: uint32(len(data)),
			Ref:    ref,
		}, nil
	}

	// Multi-chunk chain: write chunks in reverse order to resolve next pointers
	numChunks := (len(data) + lvalPayloadMax - 1) / lvalPayloadMax
	var nextRef longValueRef

	for i := numChunks - 1; i >= 0; i-- {
		start := i * lvalPayloadMax
		end := start + lvalPayloadMax
		if end > len(data) {
			end = len(data)
		}
		part := data[start:end]

		chunk := make([]byte, 4+len(part))
		if nextRef.Page != 0 {
			chunk[0] = nextRef.Row
			chunk[1] = byte(nextRef.Page)
			chunk[2] = byte(nextRef.Page >> 8)
			chunk[3] = byte(nextRef.Page >> 16)
		}
		copy(chunk[4:], part)

		ref, err := allocateLVALSlot(db, chunk)
		if err != nil {
			return longValueDescriptor{}, err
		}
		nextRef = ref
	}

	return longValueDescriptor{
		Flags:  lvalFlagMulti,
		Length: uint32(len(data)),
		Ref:    nextRef,
	}, nil
}

func allocateLVALSlot(db *Database, chunk []byte) (longValueRef, error) {
	if db == nil || db.pageSize <= 0 {
		return longValueRef{}, ErrInvalidData
	}
	needed := len(chunk)

	totalPages := len(db.data) / db.pageSize
	for p := 3; p < totalPages; p++ {
		start := p * db.pageSize
		page := db.data[start : start+db.pageSize]
		if page[0] != byte(PageTypeData) || binary.LittleEndian.Uint32(page[4:8]) != lvalOwner {
			continue
		}

		recCount := int(binary.LittleEndian.Uint16(page[12:14]))
		if recCount >= 254 {
			continue
		}

		rowEnd := db.pageSize
		for r := 0; r < recCount; r++ {
			raw := binary.LittleEndian.Uint16(page[14+r*2:])
			off := int(raw & rowOffsetMask)
			if off > 0 && off < rowEnd {
				rowEnd = off
			}
		}

		slotEnd := 14 + (recCount+1)*2
		if rowEnd-slotEnd >= needed {
			rowOffset := rowEnd - needed
			copy(page[rowOffset:rowEnd], chunk)
			binary.LittleEndian.PutUint16(page[14+recCount*2:], uint16(rowOffset))
			binary.LittleEndian.PutUint16(page[12:14], uint16(recCount+1))
			binary.LittleEndian.PutUint16(page[2:4], uint16(rowOffset-slotEnd))
			return longValueRef{Row: uint8(recCount), Page: uint32(p)}, nil
		}
	}

	// Allocate a new LVAL page
	newPage, err := db.allocatePage()
	if err != nil {
		return longValueRef{}, err
	}

	pageStart := int(newPage) * db.pageSize
	page := db.data[pageStart : pageStart+db.pageSize]

	page[0] = byte(PageTypeData)
	page[1] = 0x01
	binary.LittleEndian.PutUint32(page[4:8], lvalOwner)
	binary.LittleEndian.PutUint32(page[8:12], 0)
	binary.LittleEndian.PutUint16(page[12:14], 1)

	rowOffset := db.pageSize - needed
	copy(page[rowOffset:], chunk)
	binary.LittleEndian.PutUint16(page[14:16], uint16(rowOffset))
	slotEnd := 16
	binary.LittleEndian.PutUint16(page[2:4], uint16(rowOffset-slotEnd))

	return longValueRef{Row: 0, Page: newPage}, nil
}

func freeLongValue(db *Database, desc longValueDescriptor) error {
	if db == nil {
		return nil
	}
	if desc.Flags == lvalFlagInline || desc.Length == 0 {
		return nil
	}
	if desc.Flags == lvalFlagSingle {
		freeLVALSlot(db, desc.Ref)
		return nil
	}
	if desc.Flags == lvalFlagMulti {
		curr := desc.Ref
		visited := make(map[uint32]bool)
		for curr.Page != 0 {
			if visited[curr.Page] {
				break
			}
			visited[curr.Page] = true
			chunk, err := readLVALSlot(db, curr)
			if err != nil || len(chunk) < 4 {
				freeLVALSlot(db, curr)
				break
			}
			nextRow := chunk[0]
			nextPage := uint32(chunk[1]) | (uint32(chunk[2]) << 8) | (uint32(chunk[3]) << 16)
			freeLVALSlot(db, curr)
			curr = longValueRef{Row: nextRow, Page: nextPage}
		}
	}
	return nil
}

func freeLVALSlot(db *Database, ref longValueRef) {
	if db == nil || ref.Page == 0 {
		return
	}
	start, end, err := pageBounds(ref.Page, db.pageSize, len(db.data))
	if err != nil {
		return
	}
	page := db.data[start:end]
	if page[0] != byte(PageTypeData) || binary.LittleEndian.Uint32(page[4:8]) != lvalOwner {
		return
	}

	recCount := int(binary.LittleEndian.Uint16(page[12:14]))
	if int(ref.Row) >= recCount {
		return
	}

	slotPos := 14 + int(ref.Row)*2
	raw := binary.LittleEndian.Uint16(page[slotPos:])
	binary.LittleEndian.PutUint16(page[slotPos:], raw|rowDeletedMask|rowOverflowMask)

	allDeleted := true
	for r := 0; r < recCount; r++ {
		slotRaw := binary.LittleEndian.Uint16(page[14+r*2:])
		if (slotRaw & (rowDeletedMask | rowOverflowMask)) != (rowDeletedMask | rowOverflowMask) {
			allDeleted = false
			break
		}
	}

	if allDeleted {
		db.freePage(ref.Page)
		for i := range page {
			page[i] = 0
		}
	}
}
