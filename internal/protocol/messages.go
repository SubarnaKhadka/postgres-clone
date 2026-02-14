package protocol

import (
	"bytes"
	"encoding/binary"
	"net"
)

type FieldDescription struct {
	Name     string
	TypeOID  uint32
	TypeSize int16
}

func WriteAuthenticationOk(conn net.Conn) error {
	buff := make([]byte, 9)
	buff[0] = 'R'
	binary.BigEndian.PutUint32(buff[1:5], 8)
	binary.BigEndian.PutUint32(buff[5:9], 0)

	if _, err := conn.Write(buff); err != nil {
		return err
	}
	return nil
}

func WriteParameterStatus(conn net.Conn, key, value string) error {
	var buf bytes.Buffer
	buf.WriteByte('S')

	length := 4 + len(key) + 1 + len(value) + 1
	binary.Write(&buf, binary.BigEndian, int32(length))

	buf.WriteString(key)
	buf.WriteByte(0)
	buf.WriteString(value)
	buf.WriteByte(0)

	if _, err := conn.Write(buf.Bytes()); err != nil {
		return err
	}
	return nil
}

func WriteBackendKeyData(conn net.Conn, processID, secretKey int32) error {
	buf := make([]byte, 13)
	buf[0] = 'K'
	binary.BigEndian.PutUint32(buf[1:5], 12)
	binary.BigEndian.PutUint32(buf[5:9], uint32(processID))
	binary.BigEndian.PutUint32(buf[9:13], uint32(secretKey))

	if _, err := conn.Write(buf); err != nil {
		return err
	}
	return nil
}

func WriteReadyForQuery(conn net.Conn, status byte) error {
	buf := make([]byte, 6)
	buf[0] = 'Z'
	binary.BigEndian.PutUint32(buf[1:5], 5)
	buf[5] = status

	if _, err := conn.Write(buf); err != nil {
		return err
	}
	return nil
}

func WriteErrorResponse(conn net.Conn, message string) error {
	var buf bytes.Buffer
	buf.WriteByte('E')

	var body bytes.Buffer
	// Severity (localized)
	body.WriteByte('S')
	body.WriteString("ERROR")
	body.WriteByte(0)

	// Severity (non-localized, required by protocol v3)
	body.WriteByte('V')
	body.WriteString("ERROR")
	body.WriteByte(0)

	// SQLSTATE code
	body.WriteByte('C')
	body.WriteString("0A000")
	body.WriteByte(0)

	// Message
	body.WriteByte('M')
	body.WriteString(message)
	body.WriteByte(0)

	// Terminator
	body.WriteByte(0)

	length := 4 + body.Len()
	binary.Write(&buf, binary.BigEndian, int32(length))
	buf.Write(body.Bytes())

	if _, err := conn.Write(buf.Bytes()); err != nil {
		return err
	}

	return nil
}

func WriteCommandComplete(conn net.Conn, tag string) error {
	var buf bytes.Buffer
	buf.WriteByte('C')

	length := 4 + len(tag) + 1
	binary.Write(&buf, binary.BigEndian, int32(length))

	buf.WriteString(tag)
	buf.WriteByte(0)

	if _, err := conn.Write(buf.Bytes()); err != nil {
		return err
	}
	return nil
}

func WriteRowDescription(conn net.Conn, fields []FieldDescription) error {
	var buf bytes.Buffer
	buf.WriteByte('T')

	var body bytes.Buffer
	binary.Write(&body, binary.BigEndian, int16(len(fields)))

	for _, f := range fields {
		body.WriteString(f.Name)
		body.WriteByte(0)

		// table OID (0= not from a table)
		binary.Write(&body, binary.BigEndian, int32(0))
		// column index(0)
		binary.Write(&body, binary.BigEndian, int16(0))

		binary.Write(&body, binary.BigEndian, int32(f.TypeOID))
		binary.Write(&body, binary.BigEndian, f.TypeSize)

		// type modifier (-1)
		binary.Write(&body, binary.BigEndian, int32(-1))

		// format code (0)
		// 0 = text (values sent as human-readable strings )
		// 1 = binary (values sent as raw bytes)
		// we write 0 because we use text-format. all values go throught TextEncode
		binary.Write(&body, binary.BigEndian, int16(0))
	}

	length := 4 + body.Len()
	binary.Write(&buf, binary.BigEndian, int32(length))
	buf.Write(body.Bytes())

	if _, err := conn.Write(buf.Bytes()); err != nil {
		return err
	}
	return nil
}

func WriteDataRow(conn net.Conn, values []string, nulls []bool) error {
	var buf bytes.Buffer
	buf.WriteByte('D')

	var body bytes.Buffer
	binary.Write(&body, binary.BigEndian, int16(len(values)))

	for i, val := range values {
		if nulls[i] {
			binary.Write(&body, binary.BigEndian, int32(-1))
		} else {
			binary.Write(&body, binary.BigEndian, int32(len(val)))
			body.WriteString(val)
		}
	}
	length := 4 + body.Len()
	binary.Write(&buf, binary.BigEndian, int32(length))
	buf.Write(body.Bytes())

	if _, err := conn.Write(buf.Bytes()); err != nil {
		return err
	}
	return nil
}
