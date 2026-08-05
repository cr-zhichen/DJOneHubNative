package mbim

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTransport is an in-memory Transport that replies only after writes,
// keyed by the written transaction id so tests are race-free.
type fakeTransport struct {
	mu      sync.Mutex
	written [][]byte
	toRead  chan []byte
	reply   func(written []byte) ([]byte, bool)
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{toRead: make(chan []byte, 8)}
}

func (f *fakeTransport) WriteMessage(b []byte) error {
	cp := append([]byte(nil), b...)
	f.mu.Lock()
	f.written = append(f.written, cp)
	reply := f.reply
	f.mu.Unlock()

	if reply != nil {
		if out, ok := reply(cp); ok {
			f.toRead <- out
		}
	}
	return nil
}

func (f *fakeTransport) ReadMessage() ([]byte, error) {
	return <-f.toRead, nil
}

func (f *fakeTransport) Close() error {
	return nil
}

func openDoneMsg(tx uint32) []byte {
	b := make([]byte, headerLen+4)
	putHeader(b, MessageTypeOpenDone, uint32(len(b)), tx)
	le.PutUint32(b[12:], 0)
	return b
}

func indicateStatusMsg(tx uint32, service UUID, cid uint32, info []byte) []byte {
	bodyLen := fragHdrLen + uuidLen + 4 + 4 + len(info)
	b := make([]byte, headerLen+bodyLen)
	putHeader(b, MessageTypeIndicateStatus, uint32(len(b)), tx)
	le.PutUint32(b[12:], 1)
	le.PutUint32(b[16:], 0)
	copy(b[20:36], service[:])
	le.PutUint32(b[36:], cid)
	le.PutUint32(b[40:], uint32(len(info)))
	copy(b[44:], info)
	return b
}

func makeCommandDoneFragmentFor(tx uint32, service UUID, cid uint32, info []byte) []byte {
	bodyLen := fragHdrLen + uuidLen + 4 + 4 + 4 + len(info)
	b := make([]byte, headerLen+bodyLen)
	putHeader(b, MessageTypeCommandDone, uint32(len(b)), tx)
	le.PutUint32(b[12:], 1)
	le.PutUint32(b[16:], 0)
	copy(b[20:36], service[:])
	le.PutUint32(b[36:], cid)
	le.PutUint32(b[40:], 0)
	le.PutUint32(b[44:], uint32(len(info)))
	copy(b[48:], info)
	return b
}

// proxyOrderTransport 模拟一个走 mbim-proxy 的传输:实现 proxyConfigurer,
// 并按 tx 回复 PROXY_CONFIG 的 COMMAND_DONE 与 OPEN_DONE,同时记录写入顺序。
type proxyOrderTransport struct {
	*fakeTransport
}

func (p *proxyOrderTransport) needsProxyConfig() (string, bool) {
	return "/dev/cdc-wdm1", true
}

func TestDeviceOpenSendsProxyConfigBeforeOpen(t *testing.T) {
	inner := newFakeTransport()
	inner.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDProxyControl, CIDProxyControlConfiguration, nil), true
		}
		return nil, false
	}
	tr := &proxyOrderTransport{fakeTransport: inner}

	dev := NewDevice(tr)
	defer dev.Close()
	if err := dev.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	inner.mu.Lock()
	defer inner.mu.Unlock()
	if len(inner.written) < 2 {
		t.Fatalf("written %d messages, want >=2", len(inner.written))
	}
	first, _ := decodeHeader(inner.written[0])
	second, _ := decodeHeader(inner.written[1])
	if first.Type != MessageTypeCommand {
		t.Fatalf("first message type = %#x, want COMMAND (proxy config first)", first.Type)
	}
	if second.Type != MessageTypeOpen {
		t.Fatalf("second message type = %#x, want OPEN after proxy config", second.Type)
	}
}

func TestDeviceOpenHandshake(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		if h.Type == MessageTypeOpen {
			return openDoneMsg(h.TransactionID), true
		}
		return nil, false
	}

	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if len(ft.written) != 1 || le.Uint32(ft.written[0][0:]) != uint32(MessageTypeOpen) {
		t.Fatal("expected first write to be OPEN")
	}
	d.Close()
}

func TestDeviceDeliversIndication(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		if h.Type == MessageTypeOpen {
			return openDoneMsg(h.TransactionID), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	info := []byte{0x01, 0x02, 0x03, 0x04}
	ft.toRead <- indicateStatusMsg(0, UUIDBasicConnect, CIDBasicConnectSignalState, info)

	select {
	case ind := <-d.Indications():
		if ind.CID != CIDBasicConnectSignalState || !ind.Service.Equal(UUIDBasicConnect) {
			t.Fatalf("indication = %+v", ind)
		}
		if string(ind.InfoBuffer) != string(info) {
			t.Fatalf("info = %x, want %x", ind.InfoBuffer, info)
		}
	case <-time.After(time.Second):
		t.Fatal("indication was not delivered")
	}
}

type proxyConfigFake struct {
	*fakeTransport
	path string
}

func (p *proxyConfigFake) needsProxyConfig() (string, bool) {
	return p.path, p.path != ""
}

func TestOpenIssuesProxyConfiguration(t *testing.T) {
	base := newFakeTransport()
	base.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			svc := UUID{}
			copy(svc[:], w[20:36])
			cid := le.Uint32(w[36:])
			return makeCommandDoneFragmentFor(h.TransactionID, svc, cid, nil), true
		}
		return nil, false
	}
	pf := &proxyConfigFake{fakeTransport: base, path: "/dev/cdc-wdm0"}
	d := newDevice(pf)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	var sawProxyCmd bool
	for _, w := range base.written {
		h, _ := decodeHeader(w)
		if h.Type != MessageTypeCommand {
			continue
		}
		svc := UUID{}
		copy(svc[:], w[20:36])
		if svc.Equal(UUIDProxyControl) && le.Uint32(w[36:]) == CIDProxyControlConfiguration {
			sawProxyCmd = true
		}
	}
	if !sawProxyCmd {
		t.Fatal("Open did not send PROXY_CONTROL CONFIGURATION")
	}
}

func TestDeviceCommandRoundTrip(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return makeCommandDoneFragment(h.TransactionID, 1, 0, 0, []byte{0xde, 0xad}, true), true
		}
		return nil, false
	}

	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectDeviceCaps, CommandTypeQuery, nil)
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}
	if resp.Status != 0 || string(resp.InfoBuffer) != string([]byte{0xde, 0xad}) {
		t.Fatalf("resp = %+v", resp)
	}
	d.Close()
}

func TestNewDeviceExportedConstructor(t *testing.T) {
	ft := newFakeTransport()
	d := NewDevice(ft)
	if d == nil {
		t.Fatal("NewDevice returned nil")
	}
}

type readErrorTransport struct {
	mu      sync.Mutex
	written [][]byte
	toRead  chan readResult
}

type readResult struct {
	msg []byte
	err error
}

func newReadErrorTransport() *readErrorTransport {
	return &readErrorTransport{toRead: make(chan readResult, 4)}
}

func (t *readErrorTransport) WriteMessage(b []byte) error {
	cp := append([]byte(nil), b...)
	t.mu.Lock()
	t.written = append(t.written, cp)
	t.mu.Unlock()
	return nil
}

func (t *readErrorTransport) ReadMessage() ([]byte, error) {
	result := <-t.toRead
	return result.msg, result.err
}

func (t *readErrorTransport) Close() error {
	return nil
}

func TestDeviceReadErrorFailsPendingCommand(t *testing.T) {
	tr := newReadErrorTransport()
	tr.toRead <- readResult{msg: openDoneMsg(1)}
	d := newDevice(tr)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}

	readErr := errors.New("read failed")
	tr.toRead <- readResult{err: readErr}
	_, err := d.Command(context.Background(), UUIDBasicConnect, CIDBasicConnectDeviceCaps, CommandTypeQuery, nil)
	if err == nil {
		t.Fatal("Command should fail when read loop fails")
	}
}

func TestDeviceMalformedCommandDoneFailsPendingCommand(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			resp := makeCommandDoneFragment(h.TransactionID, 1, 0, 0, []byte{0xde, 0xad}, true)
			le.PutUint32(resp[44:], 4)
			return resp, true
		}
		return nil, false
	}

	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}

	_, err := d.Command(context.Background(), UUIDBasicConnect, CIDBasicConnectDeviceCaps, CommandTypeQuery, nil)
	if err == nil {
		t.Fatal("Command should fail for truncated COMMAND_DONE info")
	}
	d.Close()
}
