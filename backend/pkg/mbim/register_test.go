package mbim

import (
	"context"
	"testing"
)

func TestParseRegisterState(t *testing.T) {
	const fixed = 44
	pid := encodeUTF16("46000")
	pname := encodeUTF16("CMCC")
	buf := make([]byte, fixed+len(pid)+len(pname))
	le.PutUint32(buf[0:], 0)
	le.PutUint32(buf[4:], 3)
	le.PutUint32(buf[8:], 1)
	le.PutUint32(buf[12:], 0)
	le.PutUint32(buf[16:], 0)
	off := fixed
	le.PutUint32(buf[20:], uint32(off))
	le.PutUint32(buf[24:], uint32(len(pid)))
	copy(buf[off:], pid)
	off += len(pid)
	le.PutUint32(buf[28:], uint32(off))
	le.PutUint32(buf[32:], uint32(len(pname)))
	copy(buf[off:], pname)

	rs, err := parseRegisterState(buf)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if rs.ProviderID != "46000" || rs.ProviderName != "CMCC" {
		t.Fatalf("register = %+v", rs)
	}
	if rs.MCC != "460" || rs.MNC != "00" {
		t.Fatalf("MCC/MNC = %s/%s, want 460/00", rs.MCC, rs.MNC)
	}
	if rs.RegisterState != 3 {
		t.Fatalf("RegisterState = %d", rs.RegisterState)
	}
}

func TestQueryRegisterState(t *testing.T) {
	const fixed = 44
	pid := encodeUTF16("310260")
	buf := make([]byte, fixed+len(pid))
	le.PutUint32(buf[4:], 3)
	le.PutUint32(buf[20:], fixed)
	le.PutUint32(buf[24:], uint32(len(pid)))
	copy(buf[fixed:], pid)

	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, CIDBasicConnectRegisterState, buf), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	rs, err := QueryRegisterState(context.Background(), d)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if rs.MCC != "310" || rs.MNC != "260" {
		t.Fatalf("MCC/MNC = %s/%s", rs.MCC, rs.MNC)
	}
}

func TestEncodeSetRegisterStateAutomatic(t *testing.T) {
	info := encodeSetRegisterState(RegisterActionAutomatic, "")
	if len(info) != 16 {
		t.Fatalf("len = %d, want 16", len(info))
	}
	if le.Uint32(info[0:]) != 0 || le.Uint32(info[4:]) != 0 {
		t.Fatalf("provider id offset/len = %d/%d, want 0/0", le.Uint32(info[0:]), le.Uint32(info[4:]))
	}
	if le.Uint32(info[8:]) != RegisterActionAutomatic {
		t.Fatalf("action = %d, want automatic", le.Uint32(info[8:]))
	}
}

func TestEncodeSetRegisterStateManual(t *testing.T) {
	info := encodeSetRegisterState(RegisterActionManual, "310260")
	plmnBytes := encodeUTF16("310260")
	if len(info) != 16+len(plmnBytes) {
		t.Fatalf("len = %d, want %d", len(info), 16+len(plmnBytes))
	}
	if le.Uint32(info[0:]) != 16 || le.Uint32(info[4:]) != uint32(len(plmnBytes)) {
		t.Fatalf("provider id offset/len = %d/%d", le.Uint32(info[0:]), le.Uint32(info[4:]))
	}
	if le.Uint32(info[8:]) != RegisterActionManual {
		t.Fatalf("action = %d, want manual", le.Uint32(info[8:]))
	}
	got, err := newInfoReader(info).stringAt(0)
	if err != nil {
		t.Fatalf("decode provider id: %v", err)
	}
	if got != "310260" {
		t.Fatalf("provider id = %q, want 310260", got)
	}
}

func TestSetRegisterStateSendsSetCommandAndParsesResponse(t *testing.T) {
	const fixed = 44
	pid := encodeUTF16("310260")
	buf := make([]byte, fixed+len(pid))
	le.PutUint32(buf[4:], 3)
	le.PutUint32(buf[8:], RegisterActionManual)
	le.PutUint32(buf[20:], fixed)
	le.PutUint32(buf[24:], uint32(len(pid)))
	copy(buf[fixed:], pid)

	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			if le.Uint32(w[36:]) != CIDBasicConnectRegisterState {
				t.Fatalf("CID = %d, want register state", le.Uint32(w[36:]))
			}
			if CommandType(le.Uint32(w[40:])) != CommandTypeSet {
				t.Fatalf("command type = %d, want set", le.Uint32(w[40:]))
			}
			if got := le.Uint32(w[56:]); got != RegisterActionManual {
				t.Fatalf("set action = %d, want manual", got)
			}
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, CIDBasicConnectRegisterState, buf), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	rs, err := SetRegisterState(context.Background(), d, RegisterActionManual, "310260")
	if err != nil {
		t.Fatalf("SetRegisterState: %v", err)
	}
	if rs.ProviderID != "310260" || rs.RegisterMode != RegisterActionManual {
		t.Fatalf("register state = %+v", rs)
	}
}
