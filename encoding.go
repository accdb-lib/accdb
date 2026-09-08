package accdb

import (
	"bytes"
	"encoding/binary"
	"math"
	"time"
	"unicode/utf16"
)

// pageBounds calculates page byte boundaries safely preventing integer overflow
func pageBounds(pageNumber uint32, pageSize int, dataLength int) (start, end int, err error) {
	if pageSize <= 0 {
		return 0, 0, ErrInvalidData
	}
	maxPages := dataLength / pageSize
	if uint64(pageNumber) >= uint64(maxPages) {
		return 0, 0, ErrPageOutOfBounds
	}
	start = int(pageNumber) * pageSize
	return start, start + pageSize, nil
}

// binaryReader provides bounds-checked decoding over a byte buffer
type binaryReader struct {
	data []byte
}

func newBinaryReader(data []byte) binaryReader {
	return binaryReader{data: data}
}

func (r binaryReader) Uint16(offset int) (uint16, error) {
	if offset < 0 || offset > len(r.data)-2 {
		return 0, ErrCorruptDatabase
	}
	return binary.LittleEndian.Uint16(r.data[offset : offset+2]), nil
}

func (r binaryReader) Uint32(offset int) (uint32, error) {
	if offset < 0 || offset > len(r.data)-4 {
		return 0, ErrCorruptDatabase
	}
	return binary.LittleEndian.Uint32(r.data[offset : offset+4]), nil
}

func (r binaryReader) Int16(offset int) (int16, error) {
	u, err := r.Uint16(offset)
	return int16(u), err
}

func (r binaryReader) Int32(offset int) (int32, error) {
	u, err := r.Uint32(offset)
	return int32(u), err
}

func (r binaryReader) Float32(offset int) (float32, error) {
	u, err := r.Uint32(offset)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(u), nil
}

func (r binaryReader) Float64(offset int) (float64, error) {
	if offset < 0 || offset > len(r.data)-8 {
		return 0, ErrCorruptDatabase
	}
	bits := binary.LittleEndian.Uint64(r.data[offset : offset+8])
	return math.Float64frombits(bits), nil
}

// readUint16 reads a little-endian uint16 safely without panicking on out-of-bounds
func readUint16(data []byte, offset int) uint16 {
	if offset < 0 || offset > len(data)-2 {
		return 0
	}
	return binary.LittleEndian.Uint16(data[offset:])
}

// readUint32 reads a little-endian uint32 safely without panicking on out-of-bounds
func readUint32(data []byte, offset int) uint32 {
	if offset < 0 || offset > len(data)-4 {
		return 0
	}
	return binary.LittleEndian.Uint32(data[offset:])
}

// readInt16 reads a little-endian int16 safely without panicking on out-of-bounds
func readInt16(data []byte, offset int) int16 {
	if offset < 0 || offset > len(data)-2 {
		return 0
	}
	return int16(binary.LittleEndian.Uint16(data[offset:]))
}

// readInt32 reads a little-endian int32 safely without panicking on out-of-bounds
func readInt32(data []byte, offset int) int32 {
	if offset < 0 || offset > len(data)-4 {
		return 0
	}
	return int32(binary.LittleEndian.Uint32(data[offset:]))
}

// writeUint16 writes a little-endian uint16 safely without panicking on out-of-bounds
func writeUint16(data []byte, offset int, v uint16) {
	if offset < 0 || offset > len(data)-2 {
		return
	}
	binary.LittleEndian.PutUint16(data[offset:], v)
}

// writeUint32 writes a little-endian uint32 safely without panicking on out-of-bounds
func writeUint32(data []byte, offset int, v uint32) {
	if offset < 0 || offset > len(data)-4 {
		return
	}
	binary.LittleEndian.PutUint32(data[offset:], v)
}

// readFloat32 reads a little-endian float32 safely without panicking on out-of-bounds
func readFloat32(data []byte, offset int) float32 {
	if offset < 0 || offset > len(data)-4 {
		return 0
	}
	bits := binary.LittleEndian.Uint32(data[offset:])
	return math.Float32frombits(bits)
}

// readFloat64 reads a little-endian float64 safely without panicking on out-of-bounds
func readFloat64(data []byte, offset int) float64 {
	if offset < 0 || offset > len(data)-8 {
		return 0
	}
	bits := binary.LittleEndian.Uint64(data[offset:])
	return math.Float64frombits(bits)
}

// writeFloat64 writes a little-endian float64
func writeFloat64(buf *bytes.Buffer, v float64) {
	b := make([]byte, 8)
	bits := math.Float64bits(v)
	binary.LittleEndian.PutUint64(b, bits)
	buf.Write(b)
}

// readDateTime converts Access datetime (OLE Automation date) to Go time
func readDateTime(data []byte, offset int) time.Time {
	// Access stores dates as double (days since Dec 30, 1899)
	days := readFloat64(data, offset)

	// OLE Automation date epoch
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

	// Calculate the actual time
	wholeDays := int64(days)
	fraction := days - float64(wholeDays)

	result := epoch.AddDate(0, 0, int(wholeDays))
	// Add the fractional day as duration
	result = result.Add(time.Duration(fraction * 24 * float64(time.Hour)))

	return result
}

// writeDateTime converts Go time to Access datetime format
func writeDateTime(t time.Time) float64 {
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	diff := t.Sub(epoch)
	return diff.Hours() / 24.0
}

// readUTF16String reads a UTF-16LE encoded string
func readUTF16String(data []byte, offset int, length int) string {
	if length <= 0 || offset < 0 {
		return ""
	}

	// Bounds check
	if offset+length > len(data) {
		// Try to read as much as possible or return empty
		if offset >= len(data) {
			return ""
		}
		length = len(data) - offset
	}

	// Read as uint16 array
	u16 := make([]uint16, length/2)
	for i := 0; i < length/2; i++ {
		u16[i] = readUint16(data, offset+i*2)
	}

	// Trim null terminators
	for len(u16) > 0 && u16[len(u16)-1] == 0 {
		u16 = u16[:len(u16)-1]
	}

	return string(utf16.Decode(u16))
}

// writeUTF16String writes a string as UTF-16LE
func writeUTF16String(s string, maxLen int) []byte {
	u16 := utf16.Encode([]rune(s))

	// Ensure we don't exceed max length
	if len(u16)*2 > maxLen {
		u16 = u16[:maxLen/2]
	}

	result := make([]byte, maxLen)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(result[i*2:], v)
	}

	return result
}

// readCompressedString reads a compressed unicode string (Jet4+)
func readCompressedString(data []byte, offset int) (string, int) {
	if offset < 0 || offset+2 > len(data) {
		return "", 0
	}

	rawLen := readUint16(data, offset)
	isCompressed := rawLen&0x8000 != 0
	totalLen := int(rawLen & 0x7FFF)
	if totalLen == 0 {
		return "", 2
	}

	if offset+2+totalLen > len(data) {
		if offset+2 >= len(data) {
			return "", 2
		}
		totalLen = len(data) - (offset + 2)
	}

	if isCompressed {
		return string(data[offset+2 : offset+2+totalLen]), 2 + totalLen
	}

	// In Jet 4+, strings can be compressed (1 byte per char) or uncompressed (2 bytes per char)
	// The length field is the total number of bytes.
	// If the first bytes are 0xFF 0xFE, it's UTF-16.
	// Actually, Jet4 compression uses 0x00 as a flag after the length for some versions,
	// but common implementation checks the high bit of the length or a separate flag.
	// For ACCDB, we'll try a simpler heuristic:
	if offset+3 <= len(data) && data[offset+2] == 0xFF && data[offset+3] == 0xFE {
		// UTF-16 with BOM
		return readUTF16String(data, offset+4, totalLen-2), 2 + totalLen
	}

	// Heuristic: if many bytes are 0, it's probably UTF-16
	isUnicode := false
	if totalLen >= 2 {
		nullCount := 0
		for i := 0; i < totalLen && offset+2+i < len(data); i++ {
			if data[offset+2+i] == 0 {
				nullCount++
			}
		}
		if nullCount > totalLen/4 {
			isUnicode = true
		}
	}

	if !isUnicode {
		// Try ASCII
		s := string(data[offset+2 : offset+2+totalLen])
		return s, 2 + totalLen
	}

	return readUTF16String(data, offset+2, totalLen), 2 + totalLen
}

// writeCompressedString writes a compressed string
func writeCompressedString(s string) []byte {
	// Check if we can use ASCII compression
	canCompress := true
	for _, r := range s {
		if r > 127 {
			canCompress = false
			break
		}
	}

	var buf bytes.Buffer
	if canCompress && len(s) < 0x7FFF {
		// Write compressed
		length := uint16(len(s)) | 0x8000
		binary.Write(&buf, binary.LittleEndian, length)
		buf.WriteString(s)
	} else {
		// Write uncompressed UTF-16
		u16 := utf16.Encode([]rune(s))
		binary.Write(&buf, binary.LittleEndian, uint16(len(u16)*2))
		for _, v := range u16 {
			binary.Write(&buf, binary.LittleEndian, v)
		}
	}

	return buf.Bytes()
}

// readByte reads a single byte
func readByte(data []byte, offset int) byte {
	return data[offset]
}

// readBytes reads a byte slice
func readBytes(data []byte, offset int, length int) []byte {
	result := make([]byte, length)
	copy(result, data[offset:offset+length])
	return result
}

// alignTo aligns an offset to a boundary
func alignTo(offset, boundary int) int {
	if offset%boundary == 0 {
		return offset
	}
	return offset + (boundary - offset%boundary)
}

// calculateRowSize calculates the size of a row
func calculateRowSize(columns []*Column) int {
	var fixedSize int
	var varCount int

	for _, col := range columns {
		if isVariableLength(col.Type) {
			varCount++
		} else {
			fixedSize += getFixedColumnSize(col.Type)
		}
	}

	// Fixed data + null mask + variable offsets + row header
	nullMaskSize := (len(columns) + 7) / 8
	return fixedSize + nullMaskSize + varCount*2 + 2
}

// isVariableLength checks if column type is variable length
func isVariableLength(colType ColumnType) bool {
	switch colType {
	case ColTypeText, ColTypeMemo, ColTypeBinary, ColTypeOLE:
		return true
	default:
		return false
	}
}

// getFixedColumnSize returns the fixed size for a column type
func getFixedColumnSize(colType ColumnType) int {
	switch colType {
	case ColTypeBoolean, ColTypeByte:
		return 1
	case ColTypeInt:
		return 2
	case ColTypeLongInt:
		return 4
	case ColTypeMoney, ColTypeDateTime, ColTypeDouble:
		return 8
	case ColTypeFloat:
		return 4
	case ColTypeGUID:
		return 16
	case ColTypeBigInt:
		return 8
	default:
		return 0
	}
}

// nullBit checks if a bit in the null mask is set
func nullBit(nullMask []byte, index int) bool {
	byteIndex := index / 8
	bitIndex := uint(index % 8)
	return nullMask[byteIndex]&(1<<bitIndex) != 0
}

// setNullBit sets a bit in the null mask
func setNullBit(nullMask []byte, index int, isNull bool) {
	byteIndex := index / 8
	bitIndex := uint(index % 8)
	if isNull {
		nullMask[byteIndex] |= (1 << bitIndex)
	} else {
		nullMask[byteIndex] &^= (1 << bitIndex)
	}
}
