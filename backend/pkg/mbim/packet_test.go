package mbim

import (
	"context"
	"testing"
)

func TestParsePacketService(t *testing.T) {
	buf := make([]byte, 28)
	le.PutUint32(buf[0:], 0)
	le.PutUint32(buf[4:], 2)
	le.PutUint32(buf[8:], 0x80000000)
	le.PutUint64(buf[12:], 150000000)
	le.PutUint64(buf[20:], 300000000)
	ps, err := parsePacketService(buf)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ps.State != 2 || ps.UplinkSpeed != 150000000 || ps.DownlinkSpeed != 300000000 {
		t.Fatalf("packet = %+v", ps)
	}
}

func TestSetPacketServiceEncodesAction(t *testing.T) {
	var captured []byte
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			captured = append([]byte(nil), w[48:]...)
			resp := make([]byte, 28)
			le.PutUint32(resp[4:], 2)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, CIDBasicConnectPacketService, resp), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if _, err := SetPacketService(context.Background(), d, PacketServiceAttach); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if len(captured) != 4 || le.Uint32(captured) != uint32(PacketServiceAttach) {
		t.Fatalf("action info = %x, want attach", captured)
	}
}
