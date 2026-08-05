package mbim

import (
	"context"
	"testing"
)

func TestParseSlotInfoStatus(t *testing.T) {
	buf := make([]byte, 8)
	le.PutUint32(buf[0:], 0)
	le.PutUint32(buf[4:], UICCSlotStateActive)
	s, err := parseSlotInfoStatus(buf)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if s.SlotIndex != 0 || s.State != UICCSlotStateActive {
		t.Fatalf("slot info = %+v", s)
	}
}

func TestParseSlotInfoStatusTooShort(t *testing.T) {
	if _, err := parseSlotInfoStatus([]byte{0, 0, 0}); err == nil {
		t.Fatal("expected error for short buffer")
	}
}

func TestQuerySlotInfoStatus(t *testing.T) {
	ft := newFakeTransport()
	info := make([]byte, 8)
	le.PutUint32(info[0:], 1)
	le.PutUint32(info[4:], UICCSlotStateActiveEsim)
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDMSBasicConnectExtensions, CIDMSBasicConnectExtSlotInfoStatus, info), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	s, err := QuerySlotInfoStatus(context.Background(), d, 1)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if s.SlotIndex != 1 || s.State != UICCSlotStateActiveEsim {
		t.Fatalf("slot info = %+v", s)
	}
}
