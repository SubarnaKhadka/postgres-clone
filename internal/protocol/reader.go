package protocol

import (
	"encoding/binary"
	"io"
	"net"
)

func ReadMessage(conn net.Conn) (byte, []byte, error) {
	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(conn, typeBuf); err != nil {
		return 0, nil, err
	}

	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(lengthBuf)

	payload := make([]byte, length-4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return 0, nil, err
	}

	return typeBuf[0], payload, nil
}
