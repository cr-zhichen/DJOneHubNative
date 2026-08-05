package mbim

import "testing"

func makeCommandDoneFragment(tx uint32, total, current uint32, status uint32, info []byte, withFixed bool) []byte {
	if withFixed {
		bodyLen := fragHdrLen + uuidLen + 4 + 4 + 4 + len(info)
		b := make([]byte, headerLen+bodyLen)
		putHeader(b, MessageTypeCommandDone, uint32(len(b)), tx)
		le.PutUint32(b[12:], total)
		le.PutUint32(b[16:], current)
		copy(b[20:36], UUIDBasicConnect[:])
		le.PutUint32(b[36:], CIDBasicConnectDeviceCaps)
		le.PutUint32(b[40:], status)
		le.PutUint32(b[44:], uint32(len(info)))
		copy(b[48:], info)
		return b
	}

	bodyLen := fragHdrLen + len(info)
	b := make([]byte, headerLen+bodyLen)
	putHeader(b, MessageTypeCommandDone, uint32(len(b)), tx)
	le.PutUint32(b[12:], total)
	le.PutUint32(b[16:], current)
	copy(b[20:], info)
	return b
}

func TestReassembleSingleFragment(t *testing.T) {
	full := []byte{0x01, 0x02, 0x03, 0x04}
	f := makeCommandDoneFragment(7, 1, 0, 0, full, true)
	c := newCollector()
	done, err := c.add(f)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if !done {
		t.Fatal("single fragment should complete immediately")
	}
	resp, err := c.commandDone()
	if err != nil {
		t.Fatalf("commandDone failed: %v", err)
	}
	if resp.Status != 0 || resp.CID != CIDBasicConnectDeviceCaps {
		t.Fatalf("resp = %+v", resp)
	}
	if string(resp.InfoBuffer) != string(full) {
		t.Fatalf("info = %x, want %x", resp.InfoBuffer, full)
	}
}

func TestReassembleTwoFragments(t *testing.T) {
	part1 := []byte{0x11, 0x22}
	part2 := []byte{0x33, 0x44, 0x55}
	full := append(append([]byte{}, part1...), part2...)
	f0 := makeCommandDoneFragment(9, 2, 0, 0, full, true)
	f0 = f0[:48+len(part1)]
	le.PutUint32(f0[4:], uint32(len(f0)))
	f1 := makeCommandDoneFragment(9, 2, 1, 0, part2, false)

	c := newCollector()
	if done, _ := c.add(f0); done {
		t.Fatal("first fragment should not complete a two-fragment message")
	}
	done, err := c.add(f1)
	if err != nil || !done {
		t.Fatalf("second fragment should complete, done=%v err=%v", done, err)
	}
	resp, err := c.commandDone()
	if err != nil {
		t.Fatalf("commandDone failed: %v", err)
	}
	if string(resp.InfoBuffer) != string(full) {
		t.Fatalf("reassembled info = %x, want %x", resp.InfoBuffer, full)
	}
}

func TestReassembleRejectsTruncatedDeclaredInfo(t *testing.T) {
	f := makeCommandDoneFragment(7, 1, 0, 0, []byte{0x01, 0x02}, true)
	le.PutUint32(f[44:], 4)

	c := newCollector()
	done, err := c.add(f)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if !done {
		t.Fatal("single fragment should complete")
	}
	if _, err := c.commandDone(); err == nil {
		t.Fatal("truncated declared info should fail")
	}
}

func TestSplitCommandFitsSingle(t *testing.T) {
	frags := splitCommand(1, UUIDBasicConnect, CIDBasicConnectDeviceCaps, CommandTypeQuery, nil, 4096)
	if len(frags) != 1 {
		t.Fatalf("empty info should be single fragment, got %d", len(frags))
	}
	if le.Uint32(frags[0][12:]) != 1 {
		t.Fatal("total fragments should be 1")
	}
}

func TestSplitCommandMultiFragment(t *testing.T) {
	info := make([]byte, 100)
	for i := range info {
		info[i] = byte(i)
	}
	frags := splitCommand(5, UUIDBasicConnect, 1, CommandTypeSet, info, 64)
	if len(frags) < 2 {
		t.Fatalf("should split into multiple fragments, got %d", len(frags))
	}
	c := newCollector()
	var done bool
	for i, f := range frags {
		var err error
		done, err = c.add(f)
		if err != nil {
			t.Fatalf("fragment %d add failed: %v", i, err)
		}
		if len(f) > 64 {
			t.Fatalf("fragment %d length = %d, want <= 64", i, len(f))
		}
	}
	if !done {
		t.Fatal("last fragment should complete")
	}
	got, err := c.commandDone()
	if err != nil {
		t.Fatalf("commandDone: %v", err)
	}
	if string(got.InfoBuffer) != string(info) {
		t.Fatalf("reassembly failed len=%d want=%d", len(got.InfoBuffer), len(info))
	}
}
