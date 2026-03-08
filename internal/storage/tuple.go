package storage

import (
	"encoding/binary"
	"fmt"
	"math"

	"gopostgres/internal/catalog"
	"gopostgres/internal/types"
)

type TupleHeader struct {
	Xmin uint64 // XID of the transaction that created this tuple
	Xmax uint64 // XID of the transaction that deleted this tuple (0 = alive)
}

const TupleHeaderSize = 16 // 8 bytes xmin + 8 bytes xmax

func EncodeTuple(header *TupleHeader, values []any, columns []*catalog.ColumnDef) ([]byte, error) {
	if len(values) != len(columns) {
		return nil, fmt.Errorf("expected %d values, got %d", len(columns), len(values))
	}

	var buf []byte
	hdr := make([]byte, TupleHeaderSize)
	binary.BigEndian.PutUint64(hdr[0:8], header.Xmin)
	binary.BigEndian.PutUint64(hdr[8:16], header.Xmax)
	buf = append(buf, hdr...)

	// null bitmap: 1 bit per column, rounded up to whole bytes
	bitmapLen := (len(columns) + 7) / 8
	bitmap := make([]byte, bitmapLen)
	for i, v := range values {
		if v == nil {
			bitmap[i/8] |= 1 << (uint(i) % 8)
		}
	}
	buf = append(buf, bitmap...)

	// column data
	for i, v := range values {
		if v == nil {
			continue
		}
		col := columns[i]
		t, ok := types.TypeByOID(col.TypeOID)
		if !ok {
			return nil, fmt.Errorf("unknown type OID %d", col.TypeOID)
		}
		encoded, err := encodeValue(v, t)
		if err != nil {
			return nil, err
		}
		buf = append(buf, encoded...)
	}
	return buf, nil
}

func encodeValue(v any, t *types.Type) ([]byte, error) {
	if t.Size > 0 {
		// fixed-size type
		return encodeFixed(v, t)
	}
	// variable-length type
	return encodeVariable(v)
}

func encodeFixed(v any, t *types.Type) ([]byte, error) {
	buf := make([]byte, t.Size)
	switch t.OID {
	case types.OidBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool, got %T", v)
		}
		if b {
			buf[0] = 1
		}
	case types.OidInt2:
		i, ok := v.(int16)
		if !ok {
			return nil, fmt.Errorf("expected int16, got %T", v)
		}
		binary.BigEndian.PutUint16(buf, uint16(i))
	case types.OidInt4:
		i, ok := v.(int32)
		if !ok {
			return nil, fmt.Errorf("expected int32, got %T", v)
		}
		binary.BigEndian.PutUint32(buf, uint32(i))
	case types.OidInt8:
		i, ok := v.(int64)
		if !ok {
			return nil, fmt.Errorf("expected int64, got %T", v)
		}
		binary.BigEndian.PutUint64(buf, uint64(i))
	case types.OidFloat4:
		f, ok := v.(float32)
		if !ok {
			return nil, fmt.Errorf("expected float32, got %T", v)
		}
		binary.BigEndian.PutUint32(buf, math.Float32bits(f))
	case types.OidFloat8:
		f, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("expected float64, got %T", v)
		}
		binary.BigEndian.PutUint64(buf, math.Float64bits(f))
	case types.OidOid:
		o, ok := v.(uint32)
		if !ok {
			return nil, fmt.Errorf("expected uint32, got %T", v)
		}
		binary.BigEndian.PutUint32(buf, o)
	default:
		return nil, fmt.Errorf("unsupported fixed type OID %d", t.OID)

	}
	return buf, nil
}

func encodeVariable(v any) ([]byte, error) {
	var data []byte
	switch s := v.(type) {
	case string:
		data = []byte(s)
	case []byte:
		data = s
	default:
		return nil, fmt.Errorf("expected string or []byte, got %T", v)
	}

	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(data)))
	copy(buf[4:], data)
	return buf, nil
}

func DecodeTuple(data []byte, columns []*catalog.ColumnDef) (*TupleHeader, []any,
	error,
) {
	if len(data) < TupleHeaderSize {
		return nil, nil, fmt.Errorf("tuple data too short for header")
	}

	header := &TupleHeader{
		Xmin: binary.BigEndian.Uint64(data[0:8]),
		Xmax: binary.BigEndian.Uint64(data[8:16]),
	}

	bitmapLen := (len(columns) + 7) / 8
	if len(data) < TupleHeaderSize+bitmapLen {
		return nil, nil, fmt.Errorf("tuple data too short for null bitmap")
	}

	bitmap := data[TupleHeaderSize : TupleHeaderSize+bitmapLen]
	offset := TupleHeaderSize + bitmapLen
	values := make([]any, len(columns))

	for i, col := range columns {
		// check null bit
		if bitmap[i/8]&(1<<(uint(i)%8)) != 0 {
			values[i] = nil
			continue
		}

		t, ok := types.TypeByOID(col.TypeOID)
		if !ok {
			return nil, nil, fmt.Errorf("unknown type OID %d", col.TypeOID)
		}

		val, n, err := decodeValue(data[offset:], t)
		if err != nil {
			return nil, nil, err
		}
		values[i] = val
		offset += n
	}

	return header, values, nil
}

func decodeValue(data []byte, t *types.Type) (any, int, error) {
	if t.Size > 0 {
		return decodeFixed(data, t)
	}
	return decodeVariable(data)
}

func decodeFixed(data []byte, t *types.Type) (any, int, error) {
	size := int(t.Size)
	if len(data) < size {
		return nil, 0, fmt.Errorf("not enough data for type %s", t.Name)
	}

	switch t.OID {
	case types.OidBool:
		return data[0] != 0, size, nil
	case types.OidInt2:
		return int16(binary.BigEndian.Uint16(data)), size, nil
	case types.OidInt4:
		return int32(binary.BigEndian.Uint32(data)), size, nil
	case types.OidInt8:
		return int64(binary.BigEndian.Uint64(data)), size, nil
	case types.OidFloat4:
		return math.Float32frombits(binary.BigEndian.Uint32(data)), size,
			nil
	case types.OidFloat8:
		return math.Float64frombits(binary.BigEndian.Uint64(data)), size,
			nil
	case types.OidOid:
		return binary.BigEndian.Uint32(data), size, nil
	}
	return nil, 0, fmt.Errorf("unsupported fixed type OID %d", t.OID)
}

func decodeVariable(data []byte) (any, int, error) {
	if len(data) < 4 {
		return nil, 0, fmt.Errorf("not enough data for variable-length header")
	}

	length := int(binary.BigEndian.Uint32(data[:4]))
	if len(data) < 4+length {
		return nil, 0, fmt.Errorf("not enough data for variable-length value")
	}

	value := make([]byte, length)
	copy(value, data[4:4+length])
	return string(value), 4 + length, nil
}
