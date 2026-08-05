package mbim

import (
	"context"
	"testing"
)

func TestEncodeSMSSendPDU(t *testing.T) {
	pdu := []byte{0x01, 0x00, 0x0b, 0x91}
	info := encodeSMSSend(pdu)
	if le.Uint32(info[0:]) != SMSFormatPDU {
		t.Fatalf("Format = %d, want PDU", le.Uint32(info[0:]))
	}
	if le.Uint32(info[4:]) != 12 {
		t.Fatalf("PduDataOffset = %d, want 12", le.Uint32(info[4:]))
	}
	if le.Uint32(info[8:]) != uint32(len(pdu)) {
		t.Fatalf("PduDataSize = %d, want %d", le.Uint32(info[8:]), len(pdu))
	}
	if string(info[12:12+len(pdu)]) != string(pdu) {
		t.Fatal("PDU data was not written")
	}
	if len(info)%4 != 0 {
		t.Fatalf("info length not 4-byte aligned: %d", len(info))
	}
}

func TestSendSMS(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			resp := make([]byte, 4)
			le.PutUint32(resp, 42)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDSMS, CIDSMSSend, resp), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	ref, err := SendSMS(context.Background(), d, []byte{0x01, 0x02, 0x03})
	if err != nil {
		t.Fatalf("SendSMS failed: %v", err)
	}
	if ref != 42 {
		t.Fatalf("MessageReference = %d, want 42", ref)
	}
}

func TestEncodeSetSMSConfig(t *testing.T) {
	info := encodeSetSMSConfig("+8613800100500")
	if le.Uint32(info[0:]) != SMSFormatPDU {
		t.Fatalf("Format = %d, want PDU", le.Uint32(info[0:]))
	}
	off := le.Uint32(info[4:])
	size := le.Uint32(info[8:])
	if off != 12 {
		t.Fatalf("ScAddressOffset = %d, want 12", off)
	}
	want := encodeUTF16("+8613800100500")
	if size != uint32(len(want)) {
		t.Fatalf("ScAddressSize = %d, want %d", size, len(want))
	}
	if string(info[off:off+size]) != string(want) {
		t.Fatal("SC address not written")
	}
	if len(info)%4 != 0 {
		t.Fatalf("info length not 4-byte aligned: %d", len(info))
	}
}

func TestSetSMSC(t *testing.T) {
	var captured []byte
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			captured = append([]byte(nil), w[48:]...)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDSMS, CIDSMSConfiguration, nil), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if err := SetSMSC(context.Background(), d, "+8613800100500"); err != nil {
		t.Fatalf("SetSMSC failed: %v", err)
	}
	off := le.Uint32(captured[4:])
	size := le.Uint32(captured[8:])
	got, err := decodeUTF16Range(captured, off, size)
	if err != nil {
		t.Fatalf("decode SC: %v", err)
	}
	if got != "+8613800100500" {
		t.Fatalf("SC = %q", got)
	}
}

func buildSMSReadResp(index, status uint32, pdu []byte) []byte {
	const fixed = 16
	const recFixed = 16
	rec := make([]byte, recFixed+pad4(len(pdu)))
	le.PutUint32(rec[0:], index)
	le.PutUint32(rec[4:], status)
	le.PutUint32(rec[8:], recFixed)
	le.PutUint32(rec[12:], uint32(len(pdu)))
	copy(rec[recFixed:], pdu)

	info := make([]byte, fixed+len(rec))
	le.PutUint32(info[0:], SMSFormatPDU)
	le.PutUint32(info[4:], 1)
	le.PutUint32(info[8:], fixed)
	le.PutUint32(info[12:], uint32(len(rec)))
	copy(info[fixed:], rec)
	return info
}

func TestParseSMSRead(t *testing.T) {
	pdu := []byte{0x07, 0x91, 0x68, 0x10}
	recs, err := parseSMSRead(buildSMSReadResp(5, 1, pdu))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	if recs[0].Index != 5 || recs[0].Status != 1 {
		t.Fatalf("record = %+v", recs[0])
	}
	if string(recs[0].PDU) != string(pdu) {
		t.Fatalf("PDU = %x, want %x", recs[0].PDU, pdu)
	}
}

func TestReadSMSByIndex(t *testing.T) {
	pdu := []byte{0xAA, 0xBB}
	ft := newFakeTransport()
	var sentQuery []byte
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			sentQuery = append([]byte(nil), w[48:]...)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDSMS, CIDSMSRead, buildSMSReadResp(7, 0, pdu)), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	rec, err := ReadSMS(context.Background(), d, 7)
	if err != nil {
		t.Fatalf("ReadSMS failed: %v", err)
	}
	if string(rec.PDU) != string(pdu) {
		t.Fatalf("PDU = %x", rec.PDU)
	}
	if le.Uint32(sentQuery[4:]) != SMSFlagIndex || le.Uint32(sentQuery[8:]) != 7 {
		t.Fatalf("query flag/index = %d/%d", le.Uint32(sentQuery[4:]), le.Uint32(sentQuery[8:]))
	}
}

func TestDeleteSMS(t *testing.T) {
	var captured []byte
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			captured = append([]byte(nil), w[48:]...)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDSMS, CIDSMSDelete, nil), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if err := DeleteSMS(context.Background(), d, 3); err != nil {
		t.Fatalf("DeleteSMS failed: %v", err)
	}
	if le.Uint32(captured[0:]) != SMSFlagIndex || le.Uint32(captured[4:]) != 3 {
		t.Fatalf("delete flag/index = %d/%d", le.Uint32(captured[0:]), le.Uint32(captured[4:]))
	}
	if err := DeleteAllSMS(context.Background(), d); err != nil {
		t.Fatalf("DeleteAllSMS failed: %v", err)
	}
	if le.Uint32(captured[0:]) != SMSFlagAll {
		t.Fatalf("delete-all flag = %d, want ALL", le.Uint32(captured[0:]))
	}
}

func TestParseSignalStateV2DecodesRsrpSnr(t *testing.T) {
	// MBIM_SIGNAL_STATE_INFO_V2: 28-byte fixed head + MBIM_RSRP_SNR buffer.
	const fixed = 28
	rsrpCoded := uint32(72) // -85 dBm  (coded - 157)
	snrCoded := uint32(66)  // 10 dB    (coded*0.5 - 23)

	rsrpSnr := make([]byte, 4+20) // ElementCount + one MBIM_RSRP_SNR_INFO
	le.PutUint32(rsrpSnr[0:], 1)  // ElementCount
	le.PutUint32(rsrpSnr[4:], rsrpCoded)
	le.PutUint32(rsrpSnr[8:], snrCoded)
	le.PutUint32(rsrpSnr[12:], 0xFFFFFFFF) // RSRPThreshold
	le.PutUint32(rsrpSnr[16:], 0xFFFFFFFF) // SNRThreshold
	le.PutUint32(rsrpSnr[20:], 0x20)       // SystemType = LTE

	info := make([]byte, fixed+len(rsrpSnr))
	le.PutUint32(info[0:], 99) // RSSI=99 since RSRP is reported
	le.PutUint32(info[20:], fixed)
	le.PutUint32(info[24:], uint32(len(rsrpSnr)))
	copy(info[fixed:], rsrpSnr)

	s, err := parseSignalState(info)
	if err != nil {
		t.Fatalf("parseSignalState: %v", err)
	}
	if !s.HasRSRP || s.RSRP != -85 {
		t.Fatalf("RSRP has=%v val=%d, want true/-85", s.HasRSRP, s.RSRP)
	}
	if !s.HasSNR || s.SNR != 10 {
		t.Fatalf("SNR has=%v val=%d, want true/10", s.HasSNR, s.SNR)
	}
}

func TestParseSignalStateV1NoRsrp(t *testing.T) {
	info := make([]byte, signalFixedLen)
	le.PutUint32(info[0:], 20) // RSSI coded
	s, err := parseSignalState(info)
	if err != nil {
		t.Fatalf("parseSignalState: %v", err)
	}
	if s.HasRSRP || s.HasSNR {
		t.Fatalf("V1 buffer should not report RSRP/SNR: %+v", s)
	}
}

func TestGetSMSC(t *testing.T) {
	const fixed = 16
	smsc := encodeUTF16("+8613800100500")
	info := make([]byte, fixed+8+len(smsc))
	le.PutUint32(info[16:], fixed+8)
	le.PutUint32(info[20:], uint32(len(smsc)))
	copy(info[fixed+8:], smsc)

	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDSMS, CIDSMSConfiguration, info), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	smscStr, err := GetSMSC(context.Background(), d)
	if err != nil {
		t.Fatalf("GetSMSC failed: %v", err)
	}
	if smscStr != "+8613800100500" {
		t.Fatalf("SMSC = %q", smscStr)
	}
}
