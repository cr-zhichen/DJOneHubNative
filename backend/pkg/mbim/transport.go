package mbim

import (
	"fmt"
	"io"
	"os"
)

const maxMessageSize = 64 * 1024

// Transport exchanges complete MBIM messages.
type Transport interface {
	WriteMessage([]byte) error
	ReadMessage() ([]byte, error)
	Close() error
}

type streamTransport struct {
	rw  io.ReadWriteCloser
	buf []byte
}

func newStreamTransport(rw io.ReadWriteCloser) *streamTransport {
	return &streamTransport{rw: rw}
}

func (t *streamTransport) WriteMessage(msg []byte) error {
	for len(msg) > 0 {
		n, err := t.rw.Write(msg)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		msg = msg[n:]
	}
	return nil
}

func (t *streamTransport) ReadMessage() ([]byte, error) {
	for {
		if len(t.buf) >= headerLen {
			msgLen := int(le.Uint32(t.buf[4:]))
			if msgLen < headerLen {
				return nil, fmt.Errorf("mbim: invalid message length %d below header length %d", msgLen, headerLen)
			}
			if msgLen > maxMessageSize {
				return nil, fmt.Errorf("mbim: message length %d exceeds max %d", msgLen, maxMessageSize)
			}
			if len(t.buf) >= msgLen {
				msg := append([]byte(nil), t.buf[:msgLen]...)
				t.buf = t.buf[msgLen:]
				return msg, nil
			}
		}

		tmp := make([]byte, 4096)
		n, err := t.rw.Read(tmp)
		if n > 0 {
			t.buf = append(t.buf, tmp[:n]...)
		}
		if err != nil {
			if err == io.EOF && len(t.buf) > 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
}

func (t *streamTransport) Close() error {
	return t.rw.Close()
}

func openDirect(devPath string) (Transport, error) {
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("mbim: open direct device %s: %w", devPath, err)
	}
	return newStreamTransport(f), nil
}

func Dial(mode, devicePath string) (Transport, error) {
	return dialWith(dialOptions{mode: mode, devicePath: devicePath})
}
