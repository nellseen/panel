package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WSConn is a minimal RFC 6455 compliant WebSocket connection for Termux/Linux.
type WSConn struct {
	conn net.Conn
	br   *bufio.Reader
	mu   sync.Mutex
}

// UpgradeToWS upgrades an HTTP connection to a WebSocket connection.
func UpgradeToWS(w http.ResponseWriter, r *http.Request) (*WSConn, error) {
	if r.Header.Get("Upgrade") != "websocket" {
		return nil, errors.New("not a websocket handshake")
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}

	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("webserver doesn't support hijacking")
	}

	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	res := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Accept: %s\r\n\r\n", acceptKey)

	if _, err := bufrw.WriteString(res); err != nil {
		conn.Close()
		return nil, err
	}
	if err := bufrw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}

	return &WSConn{
		conn: conn,
		br:   bufrw.Reader,
	}, nil
}

// WriteTextMessage sends a text frame to the client.
func (ws *WSConn) WriteTextMessage(msg []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	length := len(msg)
	var header []byte

	if length <= 125 {
		header = []byte{0x81, byte(length)}
	} else if length <= 65535 {
		header = make([]byte, 4)
		header[0] = 0x81
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:4], uint16(length))
	} else {
		header = make([]byte, 10)
		header[0] = 0x81
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:10], uint64(length))
	}

	if _, err := ws.conn.Write(header); err != nil {
		return err
	}
	_, err := ws.conn.Write(msg)
	return err
}

// ReadTextMessage reads a single text or close frame from the client.
func (ws *WSConn) ReadTextMessage() ([]byte, error) {
	for {
		b0, err := ws.br.ReadByte()
		if err != nil {
			return nil, err
		}
		b1, err := ws.br.ReadByte()
		if err != nil {
			return nil, err
		}

		opcode := b0 & 0x0f
		isMasked := (b1 & 0x80) != 0
		var payloadLen int64 = int64(b1 & 0x7f)

		if payloadLen == 126 {
			var l uint16
			if err := binary.Read(ws.br, binary.BigEndian, &l); err != nil {
				return nil, err
			}
			payloadLen = int64(l)
		} else if payloadLen == 127 {
			var l uint64
			if err := binary.Read(ws.br, binary.BigEndian, &l); err != nil {
				return nil, err
			}
			payloadLen = int64(l)
		}

		var maskKey [4]byte
		if isMasked {
			if _, err := io.ReadFull(ws.br, maskKey[:]); err != nil {
				return nil, err
			}
		}

		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(ws.br, payload); err != nil {
			return nil, err
		}

		if isMasked {
			for i := 0; i < len(payload); i++ {
				payload[i] ^= maskKey[i%4]
			}
		}

		if opcode == 0x8 { // Close frame
			ws.Close()
			return nil, errors.New("websocket closed")
		} else if opcode == 0x9 { // Ping
			// Send Pong
			ws.mu.Lock()
			_, _ = ws.conn.Write([]byte{0x8A, 0x00})
			ws.mu.Unlock()
			continue
		} else if opcode == 0x1 { // Text
			return payload, nil
		}
	}
}

// Close closes the connection.
func (ws *WSConn) Close() error {
	return ws.conn.Close()
}
