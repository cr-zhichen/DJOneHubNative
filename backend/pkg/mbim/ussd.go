package mbim

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"unicode/utf16"

	"github.com/warthog618/sms/encoding/gsm7"
)

type USSDResponse struct {
	Response     uint32
	SessionState uint32
	DCS          uint32
	Payload      []byte
}

type USSDResult struct {
	Response uint32
	DCS      uint32
	Text     string
	RawHex   string
}

func EncodeUSSDRequest(command string) (dcs uint32, payload []byte) {
	septets, err := gsm7.Encode([]byte(command))
	if err != nil {
		units := utf16.Encode([]rune(command))
		payload := make([]byte, len(units)*2)
		for i, unit := range units {
			binary.BigEndian.PutUint16(payload[i*2:], unit)
		}
		return 0x48, payload
	}
	return 0x0F, gsm7.Pack7BitUSSD(septets, 0)
}

func NewUSSDResult(u USSDResponse) USSDResult {
	return USSDResult{
		Response: u.Response,
		DCS:      u.DCS,
		Text:     DecodeUSSDText(u.DCS, u.Payload),
		RawHex:   hex.EncodeToString(u.Payload),
	}
}

func DecodeUSSDText(dcs uint32, payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	if dcs == 0x48 || (dcs&0xC0) == 0x40 {
		if len(payload)%2 != 0 {
			return ""
		}
		units := make([]uint16, 0, len(payload)/2)
		for i := 0; i < len(payload); i += 2 {
			units = append(units, binary.BigEndian.Uint16(payload[i:i+2]))
		}
		return string(utf16.Decode(units))
	}

	unpacked := gsm7.Unpack7BitUSSD(payload, 0)
	decoded, err := gsm7.Decode(unpacked)
	if err != nil {
		return ""
	}
	return string(decoded)
}

func encodeUSSD(action, dcs uint32, payload []byte) []byte {
	const fixed = 16
	info := make([]byte, fixed+pad4(len(payload)))
	le.PutUint32(info[0:], action)
	le.PutUint32(info[4:], dcs)
	le.PutUint32(info[8:], fixed)
	le.PutUint32(info[12:], uint32(len(payload)))
	copy(info[fixed:], payload)
	return info
}

func parseUSSDResponse(info []byte) (USSDResponse, error) {
	const fixed = 20
	if len(info) < 20 {
		return USSDResponse{}, fmt.Errorf("mbim: USSD response too short len=%d", len(info))
	}
	r := newInfoReader(info)
	var resp USSDResponse
	resp.Response, _ = r.u32At(0)
	resp.SessionState, _ = r.u32At(4)
	resp.DCS, _ = r.u32At(8)
	payloadOffset, _ := r.u32At(12)
	payloadSize, _ := r.u32At(16)
	if payloadSize != 0 && payloadOffset < fixed {
		return USSDResponse{}, fmt.Errorf("mbim: USSD payload offset %d points into fixed header", payloadOffset)
	}
	payload, err := r.byteArrayAt(12)
	if err != nil {
		return USSDResponse{}, err
	}
	resp.Payload = payload
	return resp, nil
}

func SendUSSD(ctx context.Context, d *Device, action, dcs uint32, payload []byte) (USSDResponse, error) {
	resp, err := d.Command(ctx, UUIDUSSD, CIDUSSD, CommandTypeSet, encodeUSSD(action, dcs, payload))
	if err != nil {
		return USSDResponse{}, err
	}
	if resp.Status != 0 {
		return USSDResponse{}, &StatusError{Op: "USSD", Status: resp.Status}
	}
	return parseUSSDResponse(resp.InfoBuffer)
}
