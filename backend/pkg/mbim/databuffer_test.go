package mbim

import "testing"

func TestReadStringFromOffsetPair(t *testing.T) {
	buf := []byte{
		0x08, 0x00, 0x00, 0x00,
		0x08, 0x00, 0x00, 0x00,
		0x54, 0x00, 0x65, 0x00, 0x73, 0x00, 0x74, 0x00,
	}
	r := newInfoReader(buf)
	s, err := r.stringAt(0)
	if err != nil {
		t.Fatalf("stringAt failed: %v", err)
	}
	if s != "Test" {
		t.Fatalf("string = %q, want %q", s, "Test")
	}
}

func TestReadStringEmptyWhenZeroLength(t *testing.T) {
	buf := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	r := newInfoReader(buf)
	s, err := r.stringAt(0)
	if err != nil {
		t.Fatalf("empty string should not fail: %v", err)
	}
	if s != "" {
		t.Fatalf("string = %q, want empty", s)
	}
}

func TestReadU32(t *testing.T) {
	buf := []byte{0x78, 0x56, 0x34, 0x12}
	r := newInfoReader(buf)
	got, err := r.u32At(0)
	if err != nil {
		t.Fatalf("u32At failed: %v", err)
	}
	if got != 0x12345678 {
		t.Fatalf("u32 = %#x, want %#x", got, uint32(0x12345678))
	}
}

func TestU64At(t *testing.T) {
	buf := []byte{0, 0, 0, 0, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	r := newInfoReader(buf)
	v, err := r.u64At(4)
	if err != nil || v != 1 {
		t.Fatalf("u64At(4) = %d err=%v; want 1", v, err)
	}
	if _, err := r.u64At(8); err == nil {
		t.Fatal("out-of-range read should fail")
	}
}

func TestStringArrayCountAt(t *testing.T) {
	first := encodeUTF16("one")
	second := encodeUTF16("two")
	const fixed = 4 + 2*8
	buf := make([]byte, fixed+len(first)+len(second))
	le.PutUint32(buf[0:], 2)
	off := fixed
	le.PutUint32(buf[4:], uint32(off))
	le.PutUint32(buf[8:], uint32(len(first)))
	copy(buf[off:], first)
	off += len(first)
	le.PutUint32(buf[12:], uint32(off))
	le.PutUint32(buf[16:], uint32(len(second)))
	copy(buf[off:], second)

	got, err := newInfoReader(buf).stringArrayCountAt(0)
	if err != nil {
		t.Fatalf("stringArrayCountAt failed: %v", err)
	}
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("strings = %#v", got)
	}
}

func TestUICCByteArrayAt(t *testing.T) {
	buf := []byte{
		0x02, 0x00, 0x00, 0x00,
		0x08, 0x00, 0x00, 0x00,
		0xDE, 0xAD,
	}
	got, err := newInfoReader(buf).uiccByteArrayAt(0)
	if err != nil {
		t.Fatalf("uiccByteArrayAt failed: %v", err)
	}
	if len(got) != 2 || got[0] != 0xDE || got[1] != 0xAD {
		t.Fatalf("bytes = %x", got)
	}
}

func TestByteArrayAt(t *testing.T) {
	buf := []byte{
		0x08, 0x00, 0x00, 0x00,
		0x02, 0x00, 0x00, 0x00,
		0xBE, 0xEF,
	}
	got, err := newInfoReader(buf).byteArrayAt(0)
	if err != nil {
		t.Fatalf("byteArrayAt failed: %v", err)
	}
	if len(got) != 2 || got[0] != 0xBE || got[1] != 0xEF {
		t.Fatalf("bytes = %x", got)
	}
}

func TestByteArrayAtOutOfRange(t *testing.T) {
	buf := []byte{
		0x08, 0x00, 0x00, 0x00,
		0x04, 0x00, 0x00, 0x00,
		0xBE, 0xEF,
	}
	if _, err := newInfoReader(buf).byteArrayAt(0); err == nil {
		t.Fatal("out-of-range byte array should fail")
	}
}

func TestReadStringOutOfRange(t *testing.T) {
	buf := []byte{
		0x08, 0x00, 0x00, 0x00,
		0x04, 0x00, 0x00, 0x00,
	}
	r := newInfoReader(buf)
	if _, err := r.stringAt(0); err == nil {
		t.Fatal("out-of-range string should fail")
	}
}
