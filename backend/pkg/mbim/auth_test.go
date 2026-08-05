package mbim

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestUUIDAuth(t *testing.T) {
	if UUIDAuth.String() != "1d2b5ff7-0aa1-48b2-aa52-50f15767174e" {
		t.Fatalf("Auth UUID = %s", UUIDAuth.String())
	}
	if CIDAuthAKA != 1 {
		t.Fatalf("CIDAuthAKA = %d", CIDAuthAKA)
	}
}

func TestEncodeAuthAKA(t *testing.T) {
	rand := make([]byte, 16)
	autn := make([]byte, 16)
	for i := range rand {
		rand[i] = byte(i)
		autn[i] = byte(0x40 + i)
	}
	info := encodeAuthAKA(rand, autn)
	if len(info) != 32 {
		t.Fatalf("info len = %d, want 32", len(info))
	}
	if info[0] != 0x00 || info[15] != 0x0F || info[16] != 0x40 || info[31] != 0x4F {
		t.Fatalf("rand/autn not written correctly: %x", info)
	}
}

func TestEncodeAuthSIM(t *testing.T) {
	r1 := make([]byte, 16)
	for i := range r1 {
		r1[i] = byte(i)
	}
	info := encodeAuthSIM(r1, nil, nil, 1)
	if len(info) != 52 {
		t.Fatalf("info len = %d, want 52", len(info))
	}
	if info[0] != 0x00 || info[15] != 0x0F {
		t.Fatalf("Rand1 not written: %x", info[0:16])
	}
	if le.Uint32(info[48:]) != 1 {
		t.Fatalf("N = %d, want 1", le.Uint32(info[48:]))
	}
}

func TestAuthSIMRoundTrip(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			// Sres1(u32) + Kc1(u64) + Sres2 + Kc2 + Sres3 + Kc3 + N = 40
			resp := make([]byte, 40)
			le.PutUint32(resp[0:], 0x11223344)
			le.PutUint64(resp[4:], 0xAABBCCDDEEFF0011)
			le.PutUint32(resp[36:], 1)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDAuth, CIDAuthSIM, resp), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	sres, kc, err := AuthSIM(context.Background(), d, make([]byte, 16))
	if err != nil {
		t.Fatalf("AuthSIM: %v", err)
	}
	if sres != 0x11223344 {
		t.Fatalf("SRES = 0x%x, want 0x11223344", sres)
	}
	if kc != 0xAABBCCDDEEFF0011 {
		t.Fatalf("Kc = 0x%x", kc)
	}
}

func TestAuthSIMStatusError(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return buildCommandDoneStatus(h.TransactionID, UUIDAuth, CIDAuthSIM, 9, nil), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	_, _, err := AuthSIM(context.Background(), d, make([]byte, 16))
	var se *StatusError
	if err == nil || !errors.As(err, &se) || se.Status != 9 {
		t.Fatalf("err = %v, want StatusError status=9", err)
	}
}

// MBIM_STATUS_AUTH_SYNC_FAILURE (35): SQN mismatch. The modem should still
// include the AUTS resync token in the InfoBuffer even though Status != 0.
// AuthAKA must extract AUTS and return it alongside the StatusError so that
// the EAP-AKA engine can send a Synchronization-Failure response.
func TestAuthAKASyncFailureExtractsAUTS(t *testing.T) {
	ft := newFakeTransport()
	wantAUTS := make([]byte, 14)
	for i := range wantAUTS {
		wantAUTS[i] = byte(0xA0 + i)
	}
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			// Build a full 66-byte MBIM_AUTH_AKA_RESPONSE and return it with
			// Status=35 (AUTH_SYNC_FAILURE).
			resp := make([]byte, 66)
			copy(resp[52:66], wantAUTS)
			return buildCommandDoneStatus(h.TransactionID, UUIDAuth, CIDAuthAKA, 35, resp), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	_, _, _, auts, err := AuthAKA(context.Background(), d, make([]byte, 16), make([]byte, 16))
	var se *StatusError
	if err == nil || !errors.As(err, &se) || se.Status != 35 {
		t.Fatalf("AuthAKA err = %v, want StatusError{Status:35}", err)
	}
	if !bytes.Equal(auts, wantAUTS) {
		t.Fatalf("AUTS = % X, want % X", auts, wantAUTS)
	}
}

func TestAuthAKARoundTrip(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			// RES[16] + ResLen(=8) + IK[16] + CK[16] + AUTS[14] = 66
			resp := make([]byte, 66)
			for i := 0; i < 8; i++ {
				resp[i] = byte(0x10 + i)
			}
			le.PutUint32(resp[16:], 8) // ResLen
			resp[20] = 0xAA            // IK[0]
			resp[36] = 0xBB            // CK[0]
			resp[52] = 0xCC            // AUTS[0]
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDAuth, CIDAuthAKA, resp), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	res, ik, ck, auts, err := AuthAKA(context.Background(), d, make([]byte, 16), make([]byte, 16))
	if err != nil {
		t.Fatalf("AuthAKA: %v", err)
	}
	if len(res) != 8 || res[0] != 0x10 {
		t.Fatalf("RES = %x, want 8 bytes starting 0x10", res)
	}
	if len(ik) != 16 || ik[0] != 0xAA {
		t.Fatalf("IK = %x", ik)
	}
	if len(ck) != 16 || ck[0] != 0xBB {
		t.Fatalf("CK = %x", ck)
	}
	if len(auts) != 14 || auts[0] != 0xCC {
		t.Fatalf("AUTS = %x", auts)
	}
}
