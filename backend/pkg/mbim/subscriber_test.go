package mbim

import (
	"context"
	"testing"
)

func TestParseSubscriberReady(t *testing.T) {
	const fixed = 36
	imsi := encodeUTF16("460001234567890")
	iccid := encodeUTF16("89860012345678901234")
	msisdn := encodeUTF16("13800138000")
	buf := make([]byte, fixed+len(imsi)+len(iccid)+len(msisdn))
	le.PutUint32(buf[0:], 1)
	off := fixed
	le.PutUint32(buf[4:], uint32(off))
	le.PutUint32(buf[8:], uint32(len(imsi)))
	copy(buf[off:], imsi)
	off += len(imsi)
	le.PutUint32(buf[12:], uint32(off))
	le.PutUint32(buf[16:], uint32(len(iccid)))
	copy(buf[off:], iccid)
	off += len(iccid)
	le.PutUint32(buf[20:], 0)
	le.PutUint32(buf[24:], 1)
	le.PutUint32(buf[28:], uint32(off))
	le.PutUint32(buf[32:], uint32(len(msisdn)))
	copy(buf[off:], msisdn)

	s, err := parseSubscriberReady(buf)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if s.IMSI != "460001234567890" || s.ICCID != "89860012345678901234" || s.MSISDN != "13800138000" {
		t.Fatalf("subscriber = %+v", s)
	}
	if s.ReadyState != 1 {
		t.Fatalf("ReadyState = %d, want 1", s.ReadyState)
	}
}

func TestQuerySubscriberReady(t *testing.T) {
	ft := newFakeTransport()
	info := buildSubscriberBuf("460009999999999", "8986001111", "")
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, CIDBasicConnectSubscriberReadyStatus, info), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	s, err := QuerySubscriberReady(context.Background(), d)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if s.IMSI != "460009999999999" {
		t.Fatalf("IMSI = %q", s.IMSI)
	}
}

func buildSubscriberBuf(imsi, iccid, msisdn string) []byte {
	const fixed = 36
	bi, bc, bm := encodeUTF16(imsi), encodeUTF16(iccid), encodeUTF16(msisdn)
	buf := make([]byte, fixed+len(bi)+len(bc)+len(bm))
	le.PutUint32(buf[0:], 1)
	off := fixed
	le.PutUint32(buf[4:], uint32(off))
	le.PutUint32(buf[8:], uint32(len(bi)))
	copy(buf[off:], bi)
	off += len(bi)
	le.PutUint32(buf[12:], uint32(off))
	le.PutUint32(buf[16:], uint32(len(bc)))
	copy(buf[off:], bc)
	off += len(bc)
	le.PutUint32(buf[20:], 0)
	cnt := uint32(0)
	if msisdn != "" {
		cnt = 1
	}
	le.PutUint32(buf[24:], cnt)
	if cnt == 1 {
		le.PutUint32(buf[28:], uint32(off))
		le.PutUint32(buf[32:], uint32(len(bm)))
		copy(buf[off:], bm)
	}
	return buf
}
