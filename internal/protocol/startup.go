package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

type StartupMessage struct {
	ProtocolVersion uint32
	Parameters      map[string]string
}

const (
	ProtocolVersion30 = 196608
	SSLRequestCode    = 80877103
)

func ReadStartupMessage(conn net.Conn) (*StartupMessage, error) {
	lengthBuff := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuff); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBuff)

	payload := make([]byte, length-4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}
	code := binary.BigEndian.Uint32(payload[:4])

	if code == SSLRequestCode {
		// Decline SSL
		if _, err := conn.Write([]byte{'N'}); err != nil {
			return nil, err
		}
		return ReadStartupMessage(conn)
	}
	if code != ProtocolVersion30 {
		return nil, fmt.Errorf("unsupported protocol version: %d", code)
	}

	params := make(map[string]string)
	data := payload[4:]

	// Buffer format:
	// key1\0value1\0key2\0value2\0\0
	// \0 is zeo byte and \0\0 is end of buffer

	for {
		// Find Key
		// searches for the next zero byte (\0) - key
		idx := bytes.IndexByte(data, 0)
		if idx <= 0 {
			break // No more parameters
		}
		key := string(data[:idx])
		data = data[idx+1:]

		// Find Value
		// searches for the next zero byte(\0) - value
		idx = bytes.IndexByte(data, 0)
		if idx < 0 {
			break
		}
		value := string(data[:idx])
		data = data[idx+1:]

		params[key] = value
	}

	return &StartupMessage{
		ProtocolVersion: code,
		Parameters:      params,
	}, nil
}
