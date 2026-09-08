package accdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"
)

// ColumnCodec defines encoding, decoding, comparison, and index key normalization for column values
type ColumnCodec interface {
	Encode(val any, col *Column) ([]byte, error)
	Decode(data []byte, col *Column) (any, error)
	Compare(left, right any, col *Column) (int, error)
	EncodeKey(val any, col *Column) ([]byte, error)
}

// Registry of codecs by ColumnType
var codecs = map[ColumnType]ColumnCodec{
	ColTypeBoolean:  BooleanCodec{},
	ColTypeByte:     ByteCodec{},
	ColTypeInt:      Int16Codec{},
	ColTypeLongInt:  Int32Codec{},
	ColTypeBigInt:   BigIntCodec{},
	ColTypeMoney:    MoneyCodec{},
	ColTypeFloat:    Float32Codec{},
	ColTypeDouble:   Float64Codec{},
	ColTypeDateTime: DateTimeCodec{},
	ColTypeText:     TextCodec{},
	ColTypeMemo:     TextCodec{},
	ColTypeGUID:     GUIDCodec{},
	ColTypeBinary:   BinaryCodec{},
	ColTypeOLE:      BinaryCodec{},
}

// GetCodec returns the ColumnCodec for a column type, defaulting to BinaryCodec
func GetCodec(t ColumnType) ColumnCodec {
	if c, ok := codecs[t]; ok {
		return c
	}
	return BinaryCodec{}
}

// ==========================================
// Boolean Codec
// ==========================================
type BooleanCodec struct{}

func (BooleanCodec) Encode(val any, col *Column) ([]byte, error) {
	if val == nil {
		return []byte{0x00}, nil
	}
	if b, ok := val.(bool); ok && b {
		return []byte{0xFF}, nil
	}
	return []byte{0x00}, nil
}

func (BooleanCodec) Decode(data []byte, col *Column) (any, error) {
	if len(data) == 0 {
		return false, nil
	}
	return data[0] != 0, nil
}

func (BooleanCodec) Compare(left, right any, col *Column) (int, error) {
	lb, _ := left.(bool)
	rb, _ := right.(bool)
	if lb == rb {
		return 0, nil
	}
	if !lb && rb {
		return -1, nil
	}
	return 1, nil
}

func (c BooleanCodec) EncodeKey(val any, col *Column) ([]byte, error) {
	b, _ := val.(bool)
	if b {
		return []byte{0x01}, nil
	}
	return []byte{0x00}, nil
}

// ==========================================
// Byte Codec
// ==========================================
type ByteCodec struct{}

func (ByteCodec) Encode(val any, col *Column) ([]byte, error) {
	var b byte
	switch v := val.(type) {
	case byte:
		b = v
	case int:
		b = byte(v)
	case int64:
		b = byte(v)
	default:
		return nil, fmt.Errorf("cannot encode %T as byte", val)
	}
	return []byte{b}, nil
}

func (ByteCodec) Decode(data []byte, col *Column) (any, error) {
	if len(data) == 0 {
		return byte(0), nil
	}
	return data[0], nil
}

func (ByteCodec) Compare(left, right any, col *Column) (int, error) {
	var lb, rb byte
	if v, ok := left.(byte); ok {
		lb = v
	} else if v, ok := left.(int); ok {
		lb = byte(v)
	}
	if v, ok := right.(byte); ok {
		rb = v
	} else if v, ok := right.(int); ok {
		rb = byte(v)
	}
	if lb < rb {
		return -1, nil
	}
	if lb > rb {
		return 1, nil
	}
	return 0, nil
}

func (ByteCodec) EncodeKey(val any, col *Column) ([]byte, error) {
	var b byte
	switch v := val.(type) {
	case byte:
		b = v
	case int:
		b = byte(v)
	case int64:
		b = byte(v)
	}
	return []byte{b}, nil
}

// ==========================================
// Int16 Codec (Short Integer)
// ==========================================
type Int16Codec struct{}

func (Int16Codec) Encode(val any, col *Column) ([]byte, error) {
	var n int16
	switch v := val.(type) {
	case int16:
		n = v
	case int:
		n = int16(v)
	case int32:
		n = int16(v)
	case int64:
		n = int16(v)
	default:
		return nil, fmt.Errorf("cannot encode %T as int16", val)
	}
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, uint16(n))
	return buf, nil
}

func (Int16Codec) Decode(data []byte, col *Column) (any, error) {
	if len(data) < 2 {
		return int16(0), nil
	}
	return int16(binary.LittleEndian.Uint16(data)), nil
}

func (Int16Codec) Compare(left, right any, col *Column) (int, error) {
	var l, r int16
	switch v := left.(type) {
	case int16:
		l = v
	case int:
		l = int16(v)
	}
	switch v := right.(type) {
	case int16:
		r = v
	case int:
		r = int16(v)
	}
	if l < r {
		return -1, nil
	}
	if l > r {
		return 1, nil
	}
	return 0, nil
}

func (Int16Codec) EncodeKey(val any, col *Column) ([]byte, error) {
	var n int16
	switch v := val.(type) {
	case int16:
		n = v
	case int:
		n = int16(v)
	case int32:
		n = int16(v)
	case int64:
		n = int16(v)
	}
	// Sign-flip for lexicographical ordering
	un := uint16(n) ^ 0x8000
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, un)
	return buf, nil
}

// ==========================================
// Int32 Codec (Long Integer)
// ==========================================
type Int32Codec struct{}

func (Int32Codec) Encode(val any, col *Column) ([]byte, error) {
	var n int32
	switch v := val.(type) {
	case int32:
		n = v
	case int:
		n = int32(v)
	case int64:
		n = int32(v)
	case uint32:
		n = int32(v)
	default:
		return nil, fmt.Errorf("cannot encode %T as int32", val)
	}
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, uint32(n))
	return buf, nil
}

func (Int32Codec) Decode(data []byte, col *Column) (any, error) {
	if len(data) < 4 {
		return int32(0), nil
	}
	return int32(binary.LittleEndian.Uint32(data)), nil
}

func (Int32Codec) Compare(left, right any, col *Column) (int, error) {
	var l, r int32
	switch v := left.(type) {
	case int32:
		l = v
	case int:
		l = int32(v)
	case int64:
		l = int32(v)
	}
	switch v := right.(type) {
	case int32:
		r = v
	case int:
		r = int32(v)
	case int64:
		r = int32(v)
	}
	if l < r {
		return -1, nil
	}
	if l > r {
		return 1, nil
	}
	return 0, nil
}

func (Int32Codec) EncodeKey(val any, col *Column) ([]byte, error) {
	var n int32
	switch v := val.(type) {
	case int32:
		n = v
	case int:
		n = int32(v)
	case int64:
		n = int32(v)
	case uint32:
		n = int32(v)
	}
	// Sign-flip for lexicographical ordering
	un := uint32(n) ^ 0x80000000
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, un)
	return buf, nil
}

// ==========================================
// BigInt Codec (64-bit Integer)
// ==========================================
type BigIntCodec struct{}

func (BigIntCodec) Encode(val any, col *Column) ([]byte, error) {
	var n int64
	switch v := val.(type) {
	case int64:
		n = v
	case int:
		n = int64(v)
	case int32:
		n = int64(v)
	default:
		return nil, fmt.Errorf("cannot encode %T as int64", val)
	}
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(n))
	return buf, nil
}

func (BigIntCodec) Decode(data []byte, col *Column) (any, error) {
	if len(data) < 8 {
		return int64(0), nil
	}
	return int64(binary.LittleEndian.Uint64(data)), nil
}

func (BigIntCodec) Compare(left, right any, col *Column) (int, error) {
	var l, r int64
	switch v := left.(type) {
	case int64:
		l = v
	case int:
		l = int64(v)
	}
	switch v := right.(type) {
	case int64:
		r = v
	case int:
		r = int64(v)
	}
	if l < r {
		return -1, nil
	}
	if l > r {
		return 1, nil
	}
	return 0, nil
}

func (BigIntCodec) EncodeKey(val any, col *Column) ([]byte, error) {
	var n int64
	switch v := val.(type) {
	case int64:
		n = v
	case int:
		n = int64(v)
	}
	un := uint64(n) ^ 0x8000000000000000
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, un)
	return buf, nil
}

// ==========================================
// Float64 Codec (Double)
// ==========================================
type Float64Codec struct{}

func (Float64Codec) Encode(val any, col *Column) ([]byte, error) {
	var f float64
	switch v := val.(type) {
	case float64:
		f = v
	case float32:
		f = float64(v)
	case int:
		f = float64(v)
	default:
		return nil, fmt.Errorf("cannot encode %T as float64", val)
	}
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, math.Float64bits(f))
	return buf, nil
}

func (Float64Codec) Decode(data []byte, col *Column) (any, error) {
	if len(data) < 8 {
		return float64(0), nil
	}
	return math.Float64frombits(binary.LittleEndian.Uint64(data)), nil
}

func (Float64Codec) Compare(left, right any, col *Column) (int, error) {
	var l, r float64
	switch v := left.(type) {
	case float64:
		l = v
	case float32:
		l = float64(v)
	case int:
		l = float64(v)
	}
	switch v := right.(type) {
	case float64:
		r = v
	case float32:
		r = float64(v)
	case int:
		r = float64(v)
	}
	if l < r {
		return -1, nil
	}
	if l > r {
		return 1, nil
	}
	return 0, nil
}

func (Float64Codec) EncodeKey(val any, col *Column) ([]byte, error) {
	var f float64
	switch v := val.(type) {
	case float64:
		f = v
	case float32:
		f = float64(v)
	case int:
		f = float64(v)
	}
	bits := math.Float64bits(f)
	if bits&(1<<63) != 0 {
		bits = ^bits
	} else {
		bits ^= 1 << 63
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, bits)
	return buf, nil
}

// ==========================================
// Float32 Codec (Single)
// ==========================================
type Float32Codec struct{}

func (Float32Codec) Encode(val any, col *Column) ([]byte, error) {
	var f float32
	switch v := val.(type) {
	case float32:
		f = v
	case float64:
		f = float32(v)
	case int:
		f = float32(v)
	default:
		return nil, fmt.Errorf("cannot encode %T as float32", val)
	}
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, math.Float32bits(f))
	return buf, nil
}

func (Float32Codec) Decode(data []byte, col *Column) (any, error) {
	if len(data) < 4 {
		return float32(0), nil
	}
	return math.Float32frombits(binary.LittleEndian.Uint32(data)), nil
}

func (Float32Codec) Compare(left, right any, col *Column) (int, error) {
	var l, r float32
	switch v := left.(type) {
	case float32:
		l = v
	case float64:
		l = float32(v)
	case int:
		l = float32(v)
	}
	switch v := right.(type) {
	case float32:
		r = v
	case float64:
		r = float32(v)
	case int:
		r = float32(v)
	}
	if l < r {
		return -1, nil
	}
	if l > r {
		return 1, nil
	}
	return 0, nil
}

func (Float32Codec) EncodeKey(val any, col *Column) ([]byte, error) {
	var f float32
	switch v := val.(type) {
	case float32:
		f = v
	case float64:
		f = float32(v)
	case int:
		f = float32(v)
	}
	bits := math.Float32bits(f)
	if bits&(1<<31) != 0 {
		bits = ^bits
	} else {
		bits ^= 1 << 31
	}
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, bits)
	return buf, nil
}

// ==========================================
// Money Codec
// ==========================================
type MoneyCodec struct{}

func (MoneyCodec) Encode(val any, col *Column) ([]byte, error) {
	var f float64
	switch v := val.(type) {
	case float64:
		f = v
	case float32:
		f = float64(v)
	case int:
		f = float64(v)
	case int64:
		f = float64(v)
	}
	fixed := int64(math.Round(f * 10000.0))
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(fixed))
	return buf, nil
}

func (MoneyCodec) Decode(data []byte, col *Column) (any, error) {
	if len(data) < 8 {
		return float64(0), nil
	}
	fixed := int64(binary.LittleEndian.Uint64(data))
	return float64(fixed) / 10000.0, nil
}

func (MoneyCodec) Compare(left, right any, col *Column) (int, error) {
	return Float64Codec{}.Compare(left, right, col)
}

func (MoneyCodec) EncodeKey(val any, col *Column) ([]byte, error) {
	var f float64
	switch v := val.(type) {
	case float64:
		f = v
	case float32:
		f = float64(v)
	case int:
		f = float64(v)
	}
	fixed := int64(math.Round(f * 10000.0))
	un := uint64(fixed) ^ 0x8000000000000000
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, un)
	return buf, nil
}

// ==========================================
// DateTime Codec
// ==========================================
type DateTimeCodec struct{}

func (DateTimeCodec) Encode(val any, col *Column) ([]byte, error) {
	var t time.Time
	switch v := val.(type) {
	case time.Time:
		t = v
	default:
		return nil, fmt.Errorf("cannot encode %T as time.Time", val)
	}
	days := writeDateTime(t)
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, math.Float64bits(days))
	return buf, nil
}

func (DateTimeCodec) Decode(data []byte, col *Column) (any, error) {
	if len(data) < 8 {
		return time.Time{}, nil
	}
	return readDateTime(data, 0), nil
}

func (DateTimeCodec) Compare(left, right any, col *Column) (int, error) {
	var lt, rt time.Time
	if t, ok := left.(time.Time); ok {
		lt = t
	}
	if t, ok := right.(time.Time); ok {
		rt = t
	}
	if lt.Before(rt) {
		return -1, nil
	}
	if lt.After(rt) {
		return 1, nil
	}
	return 0, nil
}

func (DateTimeCodec) EncodeKey(val any, col *Column) ([]byte, error) {
	var t time.Time
	switch v := val.(type) {
	case time.Time:
		t = v
	}
	days := writeDateTime(t)
	bits := math.Float64bits(days)
	if bits&(1<<63) != 0 {
		bits = ^bits
	} else {
		bits ^= 1 << 63
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, bits)
	return buf, nil
}

// ==========================================
// Text Codec (Short Text / Memo)
// ==========================================
type TextCodec struct{}

func (TextCodec) Encode(val any, col *Column) ([]byte, error) {
	s, ok := val.(string)
	if !ok {
		return nil, fmt.Errorf("cannot encode %T as string", val)
	}
	maxBytes := 0
	if col != nil && col.Length > 0 {
		maxBytes = int(col.Length) * 2
	}
	return writeUTF16String(s, maxBytes), nil
}

func (TextCodec) Decode(data []byte, col *Column) (any, error) {
	return readUTF16String(data, 0, len(data)), nil
}

func (TextCodec) Compare(left, right any, col *Column) (int, error) {
	ls, _ := left.(string)
	rs, _ := right.(string)
	return strings.Compare(strings.ToLower(ls), strings.ToLower(rs)), nil
}

func (TextCodec) EncodeKey(val any, col *Column) ([]byte, error) {
	s, _ := val.(string)
	// Case-insensitive normalized key in UTF-8
	return []byte(strings.ToLower(s)), nil
}

// ==========================================
// GUID Codec
// ==========================================
type GUIDCodec struct{}

func (GUIDCodec) Encode(val any, col *Column) ([]byte, error) {
	b, ok := val.([]byte)
	if !ok || len(b) != 16 {
		return make([]byte, 16), nil
	}
	out := make([]byte, 16)
	copy(out, b)
	return out, nil
}

func (GUIDCodec) Decode(data []byte, col *Column) (any, error) {
	if len(data) < 16 {
		return make([]byte, 16), nil
	}
	out := make([]byte, 16)
	copy(out, data[:16])
	return out, nil
}

func (GUIDCodec) Compare(left, right any, col *Column) (int, error) {
	lb, _ := left.([]byte)
	rb, _ := right.([]byte)
	return bytes.Compare(lb, rb), nil
}

func (GUIDCodec) EncodeKey(val any, col *Column) ([]byte, error) {
	b, _ := val.([]byte)
	if len(b) < 16 {
		out := make([]byte, 16)
		copy(out, b)
		return out, nil
	}
	return b[:16], nil
}

// ==========================================
// Binary Codec
// ==========================================
type BinaryCodec struct{}

func (BinaryCodec) Encode(val any, col *Column) ([]byte, error) {
	b, ok := val.([]byte)
	if !ok {
		return nil, nil
	}
	return b, nil
}

func (BinaryCodec) Decode(data []byte, col *Column) (any, error) {
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

func (BinaryCodec) Compare(left, right any, col *Column) (int, error) {
	lb, _ := left.([]byte)
	rb, _ := right.([]byte)
	return bytes.Compare(lb, rb), nil
}

func (BinaryCodec) EncodeKey(val any, col *Column) ([]byte, error) {
	b, _ := val.([]byte)
	return b, nil
}
