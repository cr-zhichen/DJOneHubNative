package mbim

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestBuildQMIReadTransparentTLVLayout(t *testing.T) {
	frame := buildQMIReadTransparent(0x01, 2, 0x6F46, nil, []byte{0x00, 0x3F, 0xFF, 0x7F}, 0, 0)
	if len(frame) < 13 {
		t.Fatalf("frame too short: %d", len(frame))
	}
	if frame[4] != 0x0B {
		t.Fatalf("service = 0x%02X, want 0x0B(UIM)", frame[4])
	}
	tlvs := frame[13:]
	wantSession := []byte{0x01, 0x02, 0x00, 0x00, 0x00}
	if !bytes.Equal(tlvs[:5], wantSession) {
		t.Fatalf("session TLV = % X, want % X", tlvs[:5], wantSession)
	}
	wantFile := []byte{0x02, 0x07, 0x00, 0x46, 0x6F, 0x04, 0x00, 0x3F, 0xFF, 0x7F}
	if !bytes.Equal(tlvs[5:5+len(wantFile)], wantFile) {
		t.Fatalf("file TLV = % X, want % X", tlvs[5:5+len(wantFile)], wantFile)
	}
	off := 5 + len(wantFile)
	wantRead := []byte{0x03, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(tlvs[off:off+len(wantRead)], wantRead) {
		t.Fatalf("read TLV = % X, want % X", tlvs[off:off+len(wantRead)], wantRead)
	}
}

// 读取挂在某个 ADF(如 ADF_USIM)下的文件时：
//  1. Session TLV 必须携带完整 AID
//  2. session_type 必须为 0x04 (Non-provisioning on slot 1)，而不是 0x00 (Primary GW)
//
// 真机验证：EM7430 QMI-over-MBIM 隧道里 session_type=0x00 无论是否带 AID 都会以
// qmi_error=0x0030 (INVALID_ARGUMENT) 拒绝；session_type=0x04 + 显式 AID 才是
// 访问 ADF 子文件的正确方式。
func TestBuildQMIReadTransparentSessionTLVIncludesAID(t *testing.T) {
	aid := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02}
	frame := buildQMIReadTransparent(0x01, 2, 0x6F46, aid, nil, 0, 0)
	tlvs := frame[13:]
	// session_type=0x04 (Non-provisioning slot 1), aid_len, aid bytes
	wantSession := append([]byte{0x01, byte(2 + len(aid)), 0x00, 0x04, byte(len(aid))}, aid...)
	if !bytes.Equal(tlvs[:len(wantSession)], wantSession) {
		t.Fatalf("session TLV = % X, want % X", tlvs[:len(wantSession)], wantSession)
	}
}

func TestBuildQMIReadRecordMsgID(t *testing.T) {
	frame := buildQMIReadRecord(0x01, 2, 0x2F00, nil, []byte{0x00, 0x3F}, 1, 0)
	if got := le.Uint16(frame[9:11]); got != 0x0021 {
		t.Fatalf("msgId = 0x%04X, want 0x0021(READ_RECORD)", got)
	}
	tlvs := frame[13:]
	wantRead := []byte{0x03, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00}
	off := len(qmiSessionTLV(nil)) + len(qmiFileTLV(0x2F00, []byte{0x00, 0x3F}))
	if !bytes.Equal(tlvs[off:off+len(wantRead)], wantRead) {
		t.Fatalf("read-record TLV = % X, want % X", tlvs[off:off+len(wantRead)], wantRead)
	}
}

func buildTestQMIServiceResp(msgID uint16, tlvs []byte) []byte {
	return buildQMIMessage(0x0B, 0x01, 2, msgID, tlvs)
}

func TestParseQMIReadResultExtractsContentAndSW(t *testing.T) {
	efData := []byte{0x01, 'A', 'T', '&', 'T'}
	card := []byte{0x10, 0x02, 0x00, 0x90, 0x00}
	rr := []byte{0x11, byte(2 + len(efData)), 0x00, byte(len(efData)), 0x00}
	rr = append(rr, efData...)
	frame := buildTestQMIServiceResp(0x0020, append(card, rr...))

	data, sw1, sw2, err := parseQMIReadResult(frame)
	if err != nil {
		t.Fatalf("parseQMIReadResult err = %v", err)
	}
	if sw1 != 0x90 || sw2 != 0x00 {
		t.Fatalf("SW = %02X%02X, want 9000", sw1, sw2)
	}
	if !bytes.Equal(data, efData) {
		t.Fatalf("data = % X, want % X", data, efData)
	}
}

func TestParseQMIReadResultMissingTLVsErrors(t *testing.T) {
	frame := buildTestQMIServiceResp(0x0020, nil)
	if _, _, _, err := parseQMIReadResult(frame); err == nil {
		t.Fatal("parseQMIReadResult err = nil, want missing-TLV error")
	}
}

// 模组在请求阶段就判定失败时(如越界长度、非法路径/AID),只会回标准强制的
// Result Code TLV(0x02,result=FAILURE),不会带 card_result/read_result。
// parseQMIReadResult 必须把这个错误码透出来,而不是报"缺少 TLV"的模糊错误。
func TestParseQMIReadResultSurfacesQMIErrorCode(t *testing.T) {
	result := []byte{0x02, 0x04, 0x00, 0x01, 0x00, 0x1C, 0x00} // qmi_error=0x001C
	frame := buildTestQMIServiceResp(0x0020, result)

	_, _, _, err := parseQMIReadResult(frame)
	if err == nil {
		t.Fatal("parseQMIReadResult err = nil, want qmi_error to surface")
	}
	if !strings.Contains(err.Error(), "0x001C") {
		t.Fatalf("parseQMIReadResult err = %q, want it to mention qmi_error=0x001C", err.Error())
	}
}

func TestQMIReadTransparentEF(t *testing.T) {
	ft := newFakeTransport()
	card := []byte{0x10, 0x02, 0x00, 0x90, 0x00}
	rr := []byte{0x11, 0x03, 0x00, 0x01, 0x00, 0xAA}
	uimReadResp := buildTestQMIServiceResp(0x0020, append(card, rr...))
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			svc := UUID{}
			copy(svc[:], w[20:36])
			if !svc.Equal(UUIDQMI) {
				return nil, false
			}
			payload := w[48:]
			if len(payload) < 10 {
				return nil, false
			}
			qmiSvc := payload[4]
			msgIDOff := 9
			if qmiSvc == 0x00 {
				msgIDOff = 8
			}
			if len(payload) < msgIDOff+2 {
				return nil, false
			}
			msgID := le.Uint16(payload[msgIDOff : msgIDOff+2])
			switch {
			case qmiSvc == 0x00 && msgID == 0x0022:
				return makeCommandDoneFragmentFor(h.TransactionID, UUIDQMI, CIDQMIMsg, buildQMIMessage(0x00, 0x00, 1, 0x0022, []byte{0x01, 0x02, 0x00, 0x0B, 0x01})), true
			case qmiSvc == 0x0B && msgID == 0x0020:
				return makeCommandDoneFragmentFor(h.TransactionID, UUIDQMI, CIDQMIMsg, uimReadResp), true
			case qmiSvc == 0x00 && msgID == 0x0023:
				return makeCommandDoneFragmentFor(h.TransactionID, UUIDQMI, CIDQMIMsg, buildQMIMessage(0x00, 0x00, 2, 0x0023, nil)), true
			}
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	data, sw1, sw2, err := d.QMIReadTransparentEF(context.Background(), 0x6F46, nil, []byte{0x00, 0x3F}, 0, 1)
	if err != nil {
		t.Fatalf("QMIReadTransparentEF: %v", err)
	}
	if !bytes.Equal(data, []byte{0xAA}) || sw1 != 0x90 || sw2 != 0x00 {
		t.Fatalf("got data=% X sw=%02X%02X", data, sw1, sw2)
	}
}

func TestQMIReadRecordEF(t *testing.T) {
	ft := newFakeTransport()
	card := []byte{0x10, 0x02, 0x00, 0x90, 0x00}
	rr := []byte{0x11, 0x03, 0x00, 0x01, 0x00, 0xBB}
	uimReadResp := buildTestQMIServiceResp(0x0021, append(card, rr...))
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			svc := UUID{}
			copy(svc[:], w[20:36])
			if !svc.Equal(UUIDQMI) {
				return nil, false
			}
			payload := w[48:]
			if len(payload) < 10 {
				return nil, false
			}
			qmiSvc := payload[4]
			msgIDOff := 9
			if qmiSvc == 0x00 {
				msgIDOff = 8
			}
			if len(payload) < msgIDOff+2 {
				return nil, false
			}
			msgID := le.Uint16(payload[msgIDOff : msgIDOff+2])
			switch {
			case qmiSvc == 0x00 && msgID == 0x0022:
				return makeCommandDoneFragmentFor(h.TransactionID, UUIDQMI, CIDQMIMsg, buildQMIMessage(0x00, 0x00, 1, 0x0022, []byte{0x01, 0x02, 0x00, 0x0B, 0x01})), true
			case qmiSvc == 0x0B && msgID == 0x0021: // READ_RECORD is 0x0021
				return makeCommandDoneFragmentFor(h.TransactionID, UUIDQMI, CIDQMIMsg, uimReadResp), true
			case qmiSvc == 0x00 && msgID == 0x0023:
				return makeCommandDoneFragmentFor(h.TransactionID, UUIDQMI, CIDQMIMsg, buildQMIMessage(0x00, 0x00, 2, 0x0023, nil)), true
			}
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	data, sw1, sw2, err := d.QMIReadRecordEF(context.Background(), 0x6FC5, nil, []byte{0x00, 0x3F}, 1, 1)
	if err != nil {
		t.Fatalf("QMIReadRecordEF: %v", err)
	}
	if !bytes.Equal(data, []byte{0xBB}) || sw1 != 0x90 || sw2 != 0x00 {
		t.Fatalf("got data=% X sw=%02X%02X", data, sw1, sw2)
	}
}
