package mbim

import (
	"context"
	"testing"
)

func TestParseSignalState(t *testing.T) {
	buf := make([]byte, 20)
	le.PutUint32(buf[0:], 20)
	le.PutUint32(buf[4:], 99)
	s, err := parseSignalState(buf)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if s.RSSI != 20 || s.DBM != -73 {
		t.Fatalf("signal = %+v, want RSSI=20 DBM=-73", s)
	}
}

func TestParseSignalStateUnknown(t *testing.T) {
	buf := make([]byte, 20)
	le.PutUint32(buf[0:], 99)
	s, err := parseSignalState(buf)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if s.DBM != 0 || !s.Unknown {
		t.Fatalf("99 should be unknown, got %+v", s)
	}
}

func TestQuerySignalState(t *testing.T) {
	buf := make([]byte, 20)
	le.PutUint32(buf[0:], 31)
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, CIDBasicConnectSignalState, buf), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	s, err := QuerySignalState(context.Background(), d)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if s.DBM != -51 {
		t.Fatalf("DBM = %d, want -51", s.DBM)
	}
}
