package mbim

import (
	"context"
	"testing"
)

func TestEncodeVersionInfo(t *testing.T) {
	info := encodeVersionInfo(MBIMVersion1_0, MBIMExVersion2_0)
	if len(info) != 4 {
		t.Fatalf("len = %d, want 4", len(info))
	}
	if le.Uint16(info[0:]) != 0x0100 {
		t.Fatalf("bcdMBIMVersion = 0x%04x, want 0x0100", le.Uint16(info[0:]))
	}
	if le.Uint16(info[2:]) != 0x0200 {
		t.Fatalf("bcdMBIMExtendedVersion = 0x%04x, want 0x0200", le.Uint16(info[2:]))
	}
}

func TestNegotiateVersion(t *testing.T) {
	var sentInfo []byte
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			sentInfo = append([]byte(nil), w[48:]...)
			resp := make([]byte, 4)
			le.PutUint16(resp[0:], 0x0100)
			le.PutUint16(resp[2:], 0x0200)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDMSBasicConnectExtensions, CIDMSBasicConnectExtVersion, resp), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	devMBIM, devMBIMEx, err := NegotiateVersion(context.Background(), d, MBIMVersion1_0, MBIMExVersion2_0)
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if le.Uint16(sentInfo[2:]) != 0x0200 {
		t.Fatalf("sent host MBIMEx = 0x%04x, want 0x0200", le.Uint16(sentInfo[2:]))
	}
	if devMBIM != 0x0100 || devMBIMEx != 0x0200 {
		t.Fatalf("device versions = 0x%04x/0x%04x, want 0x0100/0x0200", devMBIM, devMBIMEx)
	}
}
