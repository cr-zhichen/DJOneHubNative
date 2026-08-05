package mbim

import (
	"bytes"
	"testing"
	"unicode/utf16"
)

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		le.PutUint16(b[i*2:], c)
	}
	return b
}

func TestEncodeConnectActivateWithAPN(t *testing.T) {
	info := encodeConnect(0, ActivationCommandActivate, "internet", "", "", AuthProtocolNone, ContextIPTypeIPv4v6)

	if le.Uint32(info[0:]) != 0 {
		t.Fatalf("SessionId = %d, want 0", le.Uint32(info[0:]))
	}
	if le.Uint32(info[4:]) != 1 {
		t.Fatalf("ActivationCommand = %d, want 1 (activate)", le.Uint32(info[4:]))
	}
	if le.Uint32(info[8:]) != 60 || le.Uint32(info[12:]) != 16 {
		t.Fatalf("AccessString off/len = %d/%d, want 60/16", le.Uint32(info[8:]), le.Uint32(info[12:]))
	}
	if le.Uint32(info[16:]) != 0 || le.Uint32(info[20:]) != 0 {
		t.Fatalf("UserName off/len should be 0/0")
	}
	if le.Uint32(info[24:]) != 0 || le.Uint32(info[28:]) != 0 {
		t.Fatalf("Password off/len should be 0/0")
	}
	if le.Uint32(info[36:]) != 0 {
		t.Fatalf("AuthProtocol = %d, want 0 (none)", le.Uint32(info[36:]))
	}
	if le.Uint32(info[40:]) != 3 {
		t.Fatalf("IpType = %d, want 3 (v4v6)", le.Uint32(info[40:]))
	}
	if !bytes.Equal(info[44:60], UUIDContextTypeInternet[:]) {
		t.Fatalf("ContextType uuid mismatch")
	}
	if !bytes.Equal(info[60:60+16], utf16le("internet")) {
		t.Fatalf("AccessString UTF-16LE payload mismatch")
	}
}

func TestEncodeConnectDeactivateEmptyAPN(t *testing.T) {
	info := encodeConnect(0, ActivationCommandDeactivate, "", "", "", AuthProtocolNone, ContextIPTypeDefault)
	if len(info) != 60 {
		t.Fatalf("len = %d, want 60 (no variable area when all strings empty)", len(info))
	}
	if le.Uint32(info[4:]) != 0 {
		t.Fatalf("ActivationCommand = %d, want 0 (deactivate)", le.Uint32(info[4:]))
	}
}

func TestParseConnectResponseActivated(t *testing.T) {
	buf := make([]byte, 36)
	le.PutUint32(buf[0:], 0)
	le.PutUint32(buf[4:], ActivationStateActivated)
	le.PutUint32(buf[8:], 0)
	le.PutUint32(buf[12:], ContextIPTypeIPv4v6)
	copy(buf[16:32], UUIDContextTypeInternet[:])
	le.PutUint32(buf[32:], 33)

	st, err := parseConnect(buf)
	if err != nil {
		t.Fatalf("parseConnect: %v", err)
	}
	if st.SessionID != 0 || st.ActivationState != ActivationStateActivated || st.IPType != ContextIPTypeIPv4v6 || st.NwError != 33 {
		t.Fatalf("unexpected parse: %+v", st)
	}
}
