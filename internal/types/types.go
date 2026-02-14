package types

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type OID = uint32

const (
	OidBool    OID = 16
	OidInt2    OID = 21
	OidInt4    OID = 23
	OidInt8    OID = 20
	OidFloat4  OID = 700
	OidFloat8  OID = 701
	OidText    OID = 25
	OidVarchar OID = 1043
	OidChar    OID = 1042
	OidBytea   OID = 17
	OidOid     OID = 26
)

type Type struct {
	OID        OID
	Name       string
	Size       int16
	TextEncode func(v any) (string, error)
	TextDecode func(text string) (any, error)
}

var (
	typesByOID = map[OID]*Type{}
	oidsByName = map[string]OID{}
)

func registerType(t *Type, aliases ...string) {
	typesByOID[t.OID] = t
	oidsByName[t.Name] = t.OID
	for _, alias := range aliases {
		oidsByName[alias] = t.OID
	}
}

func init() {
	registerType(&Type{
		OID: OidBool, Name: "boolean", Size: 1,
		TextEncode: func(v any) (string, error) {
			b, ok := v.(bool)
			if !ok {
				return "", fmt.Errorf("boolean encode: expected bool, got %T", v)
			}
			if b {
				return "t", nil
			}
			return "f", nil
		},
		TextDecode: func(s string) (any, error) {
			switch strings.ToLower(s) {
			case "t", "true", "1", "yes", "on":
				return true, nil
			case "f", "false", "0", "no", "off":
				return false, nil
			}
			return nil, fmt.Errorf("boolean decode: invalid v %q", s)
		},
	}, "bool")

	registerType(&Type{
		OID: OidInt2, Name: "smallint", Size: 2,
		TextEncode: func(v any) (string, error) {
			i, ok := v.(int16)
			if !ok {
				return "", fmt.Errorf("smallint encode: expected int16, got %T", v)
			}
			return strconv.FormatInt(int64(i), 10), nil
		},
		TextDecode: func(s string) (any, error) {
			i, err := strconv.ParseInt(s, 10, 16)
			if err != nil {
				return nil, fmt.Errorf("smallint decode: %w", err)
			}
			return int16(i), nil
		},
	}, "int2")

	registerType(&Type{
		OID: OidInt4, Name: "integer", Size: 4,
		TextEncode: func(v any) (string, error) {
			i, ok := v.(int32)
			if !ok {
				return "", fmt.Errorf("integer encode: expected int32, got %T", v)
			}
			return strconv.FormatInt(int64(i), 10), nil
		},
		TextDecode: func(s string) (any, error) {
			i, err := strconv.ParseInt(s, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("integer decode: %w", err)
			}
			return int32(i), nil
		},
	}, "int4", "int")

	registerType(&Type{
		OID: OidInt8, Name: "bigint", Size: 8,
		TextEncode: func(v any) (string, error) {
			i, ok := v.(int64)
			if !ok {
				return "", fmt.Errorf("bigint encode: expected int64, got %T", v)
			}
			return strconv.FormatInt(int64(i), 10), nil
		},
		TextDecode: func(s string) (any, error) {
			i, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bigint decode: %w", err)
			}
			return int64(i), nil
		},
	}, "int8")

	registerType(&Type{
		OID: OidFloat4, Name: "real", Size: 4,
		TextEncode: func(v any) (string, error) {
			f, ok := v.(float32)
			if !ok {
				return "", fmt.Errorf("real encode: expected float32, got %T", v)
			}
			return strconv.FormatFloat(float64(f), 'G', -1, 32), nil
		},
		TextDecode: func(s string) (any, error) {
			f, err := strconv.ParseFloat(s, 32)
			if err != nil {
				return nil, fmt.Errorf("real decode: %w", err)
			}
			return float32(f), nil
		},
	}, "float4")

	registerType(&Type{
		OID: OidFloat8, Name: "double precision", Size: 8,
		TextEncode: func(v any) (string, error) {
			f, ok := v.(float64)
			if !ok {
				return "", fmt.Errorf("double precision encode: expected float64, got %T", v)
			}
			return strconv.FormatFloat(float64(f), 'G', -1, 64), nil
		},
		TextDecode: func(s string) (any, error) {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("double precision decode: %w", err)
			}
			return float64(f), nil
		},
	}, "float8")

	registerType(&Type{
		OID: OidText, Name: "text", Size: -1,
		TextEncode: func(v any) (string, error) {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("text encode: expected string, got %T", v)
			}
			return s, nil
		},
		TextDecode: func(s string) (any, error) {
			return s, nil
		},
	})

	registerType(&Type{
		OID: OidVarchar, Name: "character varying", Size: -1,
		TextEncode: func(v any) (string, error) {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("varchar encode: expected string, got %T", v)
			}
			return s, nil
		},
		TextDecode: func(s string) (any, error) {
			return s, nil
		},
	}, "varchar")

	registerType(&Type{
		OID: OidChar, Name: "character", Size: -1,
		TextEncode: func(v any) (string, error) {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("character encode: expected character, got %T", v)
			}
			return s, nil
		},
		TextDecode: func(s string) (any, error) {
			return s, nil
		},
	}, "char")

	registerType(&Type{
		OID: OidBytea, Name: "bytea", Size: -1,
		TextEncode: func(v any) (string, error) {
			b, ok := v.([]byte)
			if !ok {
				return "", fmt.Errorf("bytea encode: expected byte[], got %T", v)
			}
			return `\x` + hex.EncodeToString(b), nil
		},
		TextDecode: func(s string) (any, error) {
			if strings.HasPrefix(s, `\x`) {
				b, err := hex.DecodeString(s[2:])
				if err != nil {
					return nil, fmt.Errorf("bytea decode: %w", err)
				}
				return b, nil
			}
			return []byte(s), nil
		},
	})
	registerType(&Type{
		OID: OidOid, Name: "oid", Size: 4,
		TextEncode: func(v any) (string, error) {
			o, ok := v.(uint32)
			if !ok {
				return "", fmt.Errorf("oid encode: expected uint32, got %T", v)
			}
			return strconv.FormatUint(uint64(o), 10), nil
		},
		TextDecode: func(s string) (any, error) {
			o, err := strconv.ParseUint(s, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("oid decode: %w", err)
			}
			return uint32(o), nil
		},
	})
}

func TypeByOID(oid OID) (*Type, bool) {
	t, ok := typesByOID[oid]
	return t, ok
}

func OIDByName(name string) (OID, bool) {
	oid, ok := oidsByName[strings.ToLower(name)]
	return oid, ok
}
