package mbim

import (
	"context"
	"testing"
)

func TestParseRadioState(t *testing.T) {
	buf := make([]byte, 8)
	le.PutUint32(buf[0:], 1)
	le.PutUint32(buf[4:], 0)
	rs, err := parseRadioState(buf)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if rs.Hardware != RadioOn || rs.Software != RadioOff {
		t.Fatalf("radio = %+v", rs)
	}
}

func TestSetRadioStateEncodes(t *testing.T) {
	var captured []byte
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			captured = append([]byte(nil), w[48:]...)
			resp := make([]byte, 8)
			le.PutUint32(resp[0:], 1)
			le.PutUint32(resp[4:], 1)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, CIDBasicConnectRadioState, resp), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	rs, err := SetRadioState(context.Background(), d, RadioOn)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if len(captured) != 4 || le.Uint32(captured) != uint32(RadioOn) {
		t.Fatalf("set info = %x, want ON", captured)
	}
	if rs.Software != RadioOn {
		t.Fatalf("SwRadioState = %d, want ON", rs.Software)
	}
}
