package mbim

import (
	"context"
	"testing"
)

func TestEncodeSubscribeList(t *testing.T) {
	entries := []EventEntry{
		{Service: UUIDBasicConnect, CIDs: []uint32{CIDBasicConnectSignalState, CIDBasicConnectRegisterState}},
		{Service: UUIDSMS, CIDs: []uint32{CIDSMSRead}},
	}
	info := encodeSubscribeList(entries)
	if le.Uint32(info[0:]) != 2 {
		t.Fatalf("EventsCount = %d, want 2", le.Uint32(info[0:]))
	}
	off0 := le.Uint32(info[4:])
	if !bytesEqualUUID(info[off0:off0+16], UUIDBasicConnect) {
		t.Fatal("entry0 UUID mismatch")
	}
	if le.Uint32(info[int(off0)+16:]) != 2 {
		t.Fatalf("entry0 CidsCount = %d, want 2", le.Uint32(info[int(off0)+16:]))
	}
	if le.Uint32(info[int(off0)+20:]) != CIDBasicConnectSignalState {
		t.Fatal("entry0 first CID mismatch")
	}
}

func bytesEqualUUID(b []byte, u UUID) bool {
	if len(b) < 16 {
		return false
	}
	for i := 0; i < 16; i++ {
		if b[i] != u[i] {
			return false
		}
	}
	return true
}

func TestSubscribeDefaultEvents(t *testing.T) {
	ft := newFakeTransport()
	var sawSubscribe bool
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			if le.Uint32(w[36:]) == CIDBasicConnectDeviceServiceSubscribeList {
				sawSubscribe = true
			}
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, le.Uint32(w[36:]), nil), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if err := SubscribeDefaultEvents(context.Background(), d); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if !sawSubscribe {
		t.Fatal("SUBSCRIBE_LIST command not sent")
	}
}
