package mbim

import (
	"context"
	"testing"
	"unicode/utf16"
)

func encodeUTF16(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		le.PutUint16(b[i*2:], c)
	}
	return b
}

func TestDeviceCapsParsesIMEI(t *testing.T) {
	const fixed = 8*4 + 4*8
	imei := "356938035643809"
	imeiBytes := encodeUTF16(imei)
	info := make([]byte, fixed+len(imeiBytes))
	le.PutUint32(info[40:], fixed)
	le.PutUint32(info[44:], uint32(len(imeiBytes)))
	copy(info[fixed:], imeiBytes)

	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return makeCommandDoneFragment(h.TransactionID, 1, 0, 0, info, true), true
		}
		return nil, false
	}

	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}

	caps, err := DeviceCaps(context.Background(), d)
	if err != nil {
		t.Fatalf("DeviceCaps failed: %v", err)
	}
	if caps.DeviceID != imei {
		t.Fatalf("IMEI = %q, want %q", caps.DeviceID, imei)
	}
	d.Close()
}
