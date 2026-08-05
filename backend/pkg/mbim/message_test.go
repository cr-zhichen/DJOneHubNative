package mbim

import (
	"bytes"
	"testing"
)

func TestEncodeOpen(t *testing.T) {
	got := encodeOpen(0x11, 4096)
	want := []byte{
		0x01, 0x00, 0x00, 0x00,
		0x10, 0x00, 0x00, 0x00,
		0x11, 0x00, 0x00, 0x00,
		0x00, 0x10, 0x00, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("OPEN encoding mismatch\n got=%x\nwant=%x", got, want)
	}
}

func TestEncodeClose(t *testing.T) {
	got := encodeClose(0x22)
	want := []byte{
		0x02, 0x00, 0x00, 0x00,
		0x0c, 0x00, 0x00, 0x00,
		0x22, 0x00, 0x00, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("CLOSE encoding mismatch\n got=%x\nwant=%x", got, want)
	}
}

func TestEncodeCommandSingleFragment(t *testing.T) {
	got := encodeCommand(1, UUIDBasicConnect, CIDBasicConnectDeviceCaps, CommandTypeQuery, nil)
	if le.Uint32(got[0:]) != uint32(MessageTypeCommand) {
		t.Fatalf("type = %x, want COMMAND", got[0:4])
	}
	if le.Uint32(got[4:]) != 48 {
		t.Fatalf("length = %d, want 48", le.Uint32(got[4:]))
	}
	if le.Uint32(got[12:]) != 1 || le.Uint32(got[16:]) != 0 {
		t.Fatal("fragment total/current should be 1/0")
	}
	if !bytes.Equal(got[20:36], UUIDBasicConnect[:]) {
		t.Fatal("service UUID mismatch")
	}
	if le.Uint32(got[36:]) != CIDBasicConnectDeviceCaps || le.Uint32(got[40:]) != uint32(CommandTypeQuery) {
		t.Fatal("cid/command_type mismatch")
	}
	if le.Uint32(got[44:]) != 0 {
		t.Fatalf("buffer_length = %d, want 0", le.Uint32(got[44:]))
	}
}

func TestDecodeHeader(t *testing.T) {
	f := makeCommandDoneFragment(7, 1, 0, 0, []byte{0xaa, 0xbb}, true)
	h, err := decodeHeader(f)
	if err != nil {
		t.Fatalf("decodeHeader failed: %v", err)
	}
	if h.Type != MessageTypeCommandDone || h.TransactionID != 7 {
		t.Fatalf("header = %+v", h)
	}
}
