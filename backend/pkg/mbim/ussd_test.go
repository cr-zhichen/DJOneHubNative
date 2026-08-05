package mbim

import (
	"bytes"
	"context"
	"testing"

	"github.com/warthog618/sms/encoding/gsm7"
)

func TestEncodeUSSD(t *testing.T) {
	payload := []byte{0x2a, 0x31, 0x32, 0x33, 0x23}
	info := encodeUSSD(USSDActionInitiate, 0x0F, payload)
	if le.Uint32(info[0:]) != USSDActionInitiate {
		t.Fatalf("Action = %d, want %d", le.Uint32(info[0:]), USSDActionInitiate)
	}
	if le.Uint32(info[4:]) != 0x0F {
		t.Fatalf("DCS = %d, want %d", le.Uint32(info[4:]), 0x0F)
	}
	// SET_USSD fixed header is Action(4) + DataCodingScheme(4) + Payload ref(8) = 16
	// bytes (see mbim-service-ussd.json "set"). The 20-byte layout only applies to
	// the response/notification struct, which has an extra SessionState field.
	if le.Uint32(info[8:]) != 16 {
		t.Fatalf("PayloadOffset = %d, want 16", le.Uint32(info[8:]))
	}
	if le.Uint32(info[12:]) != uint32(len(payload)) {
		t.Fatalf("PayloadSize = %d, want %d", le.Uint32(info[12:]), len(payload))
	}
	if len(info) != 16+pad4(len(payload)) {
		t.Fatalf("len(info) = %d, want %d", len(info), 16+pad4(len(payload)))
	}
	if !bytes.Equal(info[16:16+len(payload)], payload) {
		t.Fatalf("Payload = %x, want %x", info[16:16+len(payload)], payload)
	}
}

func TestEncodeUSSDRequestRoundTripGSM7(t *testing.T) {
	dcs, payload := EncodeUSSDRequest("*100#")

	if dcs != 0x0F {
		t.Fatalf("DCS = 0x%02x, want 0x0f", dcs)
	}
	if got := DecodeUSSDText(dcs, payload); got != "*100#" {
		t.Fatalf("DecodeUSSDText() = %q, want %q", got, "*100#")
	}
}

func TestEncodeUSSDRequestFallsBackToUCS2(t *testing.T) {
	dcs, payload := EncodeUSSDRequest("*余额#")

	if dcs != 0x48 {
		t.Fatalf("DCS = 0x%02x, want 0x48", dcs)
	}
	if len(payload) == 0 {
		t.Fatal("payload is empty")
	}
	if got := DecodeUSSDText(dcs, payload); got != "*余额#" {
		t.Fatalf("DecodeUSSDText() = %q, want %q", got, "*余额#")
	}
}

func TestNewUSSDResultDecodesTextAndRawHex(t *testing.T) {
	_, payload := EncodeUSSDRequest("*100#")

	got := NewUSSDResult(USSDResponse{
		Response: USSDRespNoActionRequired,
		DCS:      0x0F,
		Payload:  payload,
	})

	if got.Response != USSDRespNoActionRequired || got.DCS != 0x0F {
		t.Fatalf("NewUSSDResult() = %+v", got)
	}
	if got.Text != "*100#" {
		t.Fatalf("Text = %q, want %q", got.Text, "*100#")
	}
	if got.RawHex != "aa180c3602" {
		t.Fatalf("RawHex = %q, want packed payload hex", got.RawHex)
	}
}

func TestParseUSSDResponse(t *testing.T) {
	payload := []byte{0x41, 0x42, 0x43}
	info := make([]byte, 20+len(payload))
	le.PutUint32(info[0:], USSDRespActionRequired)
	le.PutUint32(info[4:], 2)
	le.PutUint32(info[8:], 0x0F)
	le.PutUint32(info[12:], 20)
	le.PutUint32(info[16:], uint32(len(payload)))
	copy(info[20:], payload)

	resp, err := parseUSSDResponse(info)
	if err != nil {
		t.Fatalf("parseUSSDResponse: %v", err)
	}
	if resp.Response != USSDRespActionRequired {
		t.Fatalf("Response = %d, want %d", resp.Response, USSDRespActionRequired)
	}
	if resp.SessionState != 2 {
		t.Fatalf("SessionState = %d, want 2", resp.SessionState)
	}
	if resp.DCS != 0x0F {
		t.Fatalf("DCS = %d, want %d", resp.DCS, 0x0F)
	}
	if !bytes.Equal(resp.Payload, payload) {
		t.Fatalf("Payload = %x, want %x", resp.Payload, payload)
	}
}

func TestParseUSSDResponseRejectsPayloadOffsetIntoFixedHeader(t *testing.T) {
	info := make([]byte, 24)
	le.PutUint32(info[0:], USSDRespActionRequired)
	le.PutUint32(info[4:], 2)
	le.PutUint32(info[8:], 0x0F)
	le.PutUint32(info[12:], 16)
	le.PutUint32(info[16:], 4)

	if _, err := parseUSSDResponse(info); err == nil {
		t.Fatal("parseUSSDResponse should reject payload offset into fixed header")
	}
}

func TestSendUSSD(t *testing.T) {
	payload := []byte{0x2a, 0x31, 0x32, 0x33, 0x23}
	replyPayload := []byte{0x30, 0x31}
	var command []byte

	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			command = append([]byte(nil), w...)

			respInfo := make([]byte, 20+len(replyPayload))
			le.PutUint32(respInfo[0:], USSDRespNoActionRequired)
			le.PutUint32(respInfo[4:], 1)
			le.PutUint32(respInfo[8:], 0x48)
			le.PutUint32(respInfo[12:], 20)
			le.PutUint32(respInfo[16:], uint32(len(replyPayload)))
			copy(respInfo[20:], replyPayload)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDUSSD, CIDUSSD, respInfo), true
		}
		return nil, false
	}

	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	resp, err := SendUSSD(context.Background(), d, USSDActionInitiate, 0x0F, payload)
	if err != nil {
		t.Fatalf("SendUSSD: %v", err)
	}
	svc := UUID{}
	copy(svc[:], command[20:36])
	if !svc.Equal(UUIDUSSD) {
		t.Fatalf("service = %s, want %s", svc.String(), UUIDUSSD.String())
	}
	if cid := le.Uint32(command[36:]); cid != CIDUSSD {
		t.Fatalf("CID = %d, want %d", cid, CIDUSSD)
	}
	if ct := le.Uint32(command[40:]); ct != uint32(CommandTypeSet) {
		t.Fatalf("CommandType = %d, want %d", ct, CommandTypeSet)
	}
	if got := encodeUSSD(USSDActionInitiate, 0x0F, payload); !bytes.Equal(command[48:], got) {
		t.Fatalf("COMMAND info = %x, want %x", command[48:], got)
	}
	if resp.Response != USSDRespNoActionRequired || resp.SessionState != 1 || resp.DCS != 0x48 {
		t.Fatalf("resp = %+v", resp)
	}
	if !bytes.Equal(resp.Payload, replyPayload) {
		t.Fatalf("Payload = %x, want %x", resp.Payload, replyPayload)
	}
}

func TestDecodeUSSDTextDecodesPackedGSM7(t *testing.T) {
	packedGSM7 := gsm7.Pack7BitUSSD([]byte("OK"), 0)

	if got := DecodeUSSDText(0x0F, packedGSM7); got != "OK" {
		t.Fatalf("DecodeUSSDText() = %q, want %q", got, "OK")
	}
}

func TestDecodeUSSDTextDecodesUCS2BigEndian(t *testing.T) {
	if got := DecodeUSSDText(0x48, []byte{0x4F, 0x60}); got != "你" {
		t.Fatalf("DecodeUSSDText() = %q, want %q", got, "你")
	}
}

func TestDecodeUSSDTextDecodesUCS2ClassDCS(t *testing.T) {
	if got := DecodeUSSDText(0x41, []byte{0x4F, 0x60}); got != "你" {
		t.Fatalf("DecodeUSSDText() = %q, want %q", got, "你")
	}
}

func TestDecodeUSSDTextRejectsOddLengthUCS2Payload(t *testing.T) {
	if got := DecodeUSSDText(0x41, []byte{0x4F}); got != "" {
		t.Fatalf("DecodeUSSDText() = %q, want empty string", got)
	}
}
