package mbim

import (
	"bytes"
	"context"
	"testing"
)

func TestDeviceResetSendsExtensionsDeviceReset(t *testing.T) {
	var (
		gotService UUID
		gotCID     uint32
		gotType    uint32
		gotInfoLen uint32
	)
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			copy(gotService[:], w[20:36])
			gotCID = le.Uint32(w[36:40])
			gotType = le.Uint32(w[40:44])
			gotInfoLen = le.Uint32(w[44:48])
			// Device Reset has an empty response payload.
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDMSBasicConnectExtensions, CIDMSBasicConnectExtDeviceReset, nil), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	if err := DeviceReset(context.Background(), d); err != nil {
		t.Fatalf("DeviceReset failed: %v", err)
	}
	if !bytes.Equal(gotService[:], UUIDMSBasicConnectExtensions[:]) {
		t.Fatalf("service = %s, want MS Basic Connect Extensions", gotService)
	}
	if gotCID != CIDMSBasicConnectExtDeviceReset {
		t.Fatalf("cid = %d, want %d", gotCID, CIDMSBasicConnectExtDeviceReset)
	}
	if gotType != uint32(CommandTypeSet) {
		t.Fatalf("command type = %d, want Set", gotType)
	}
	if gotInfoLen != 0 {
		t.Fatalf("info len = %d, want 0 (empty payload)", gotInfoLen)
	}
}
