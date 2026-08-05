package smscodec

import (
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/warthog618/sms/encoding/tpdu"
)

// ParseATSMSHeaderTPDULength extracts the trailing TPDU octet length from
// +CMGR/+CMGL headers. In PDU mode this length excludes the SMSC field.
func ParseATSMSHeaderTPDULength(header string) (int, bool) {
	idx := strings.LastIndex(header, ",")
	if idx < 0 || idx+1 >= len(header) {
		return 0, false
	}

	token := strings.TrimSpace(header[idx+1:])
	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func TrimFullPDUHexByATHeader(pduHex, header string) (string, bool) {
	tpduLen, ok := ParseATSMSHeaderTPDULength(header)
	if !ok {
		return strings.TrimSpace(pduHex), false
	}
	return TrimFullPDUHexByTPDULength(pduHex, tpduLen)
}

func TrimFullPDUHexByTPDULength(pduHex string, tpduLen int) (string, bool) {
	raw := strings.TrimSpace(pduHex)
	if raw == "" || tpduLen < 0 {
		return raw, false
	}

	b, err := hex.DecodeString(raw)
	if err != nil || len(b) == 0 {
		return raw, false
	}

	smscLen := int(b[0])
	want := 1 + smscLen + tpduLen
	if want <= 0 || want > len(b) {
		return strings.ToUpper(raw), false
	}

	full := b[:want]
	trimmed := want < len(b)
	tpduOffset := 1 + smscLen
	if tpduBytes, ok := TrimDeliverTPDUToDeclaredLength(full[tpduOffset:]); ok {
		out := make([]byte, 0, tpduOffset+len(tpduBytes))
		out = append(out, full[:tpduOffset]...)
		out = append(out, tpduBytes...)
		full = out
		trimmed = true
	}

	return strings.ToUpper(hex.EncodeToString(full)), trimmed
}

func TrimDeliverTPDUToDeclaredLength(tpduBytes []byte) ([]byte, bool) {
	want, ok := DeliverTPDUDeclaredLength(tpduBytes)
	if !ok || want >= len(tpduBytes) {
		return tpduBytes, false
	}
	return append([]byte(nil), tpduBytes[:want]...), true
}

func DeliverTPDUDeclaredLength(tpduBytes []byte) (int, bool) {
	_, _, udOffset, udOctets, _, ok := deliverTPDULayout(tpduBytes)
	if !ok {
		return 0, false
	}
	want := udOffset + udOctets
	if want > len(tpduBytes) {
		return 0, false
	}
	return want, true
}

func normalizeDeliverTPDUGSM7SpareBits(tpduBytes []byte) ([]byte, bool) {
	_, udl, udOffset, udOctets, alphabet, ok := deliverTPDULayout(tpduBytes)
	if !ok || alphabet != tpdu.Alpha7Bit || udl == 0 || udOctets == 0 {
		return tpduBytes, false
	}
	want := udOffset + udOctets
	if want > len(tpduBytes) {
		return tpduBytes, false
	}
	usedBits := (udl * 7) % 8
	if usedBits == 0 {
		return tpduBytes, false
	}
	mask := byte((1 << usedBits) - 1)
	last := want - 1
	if tpduBytes[last]&^mask == 0 {
		return tpduBytes, false
	}
	out := append([]byte(nil), tpduBytes...)
	out[last] &= mask
	return out, true
}

func deliverTPDULayout(tpduBytes []byte) (dcs byte, udl int, udOffset int, udOctets int, alphabet tpdu.Alphabet, ok bool) {
	if len(tpduBytes) < 1 || tpduBytes[0]&0x03 != 0 {
		return 0, 0, 0, 0, 0, false
	}

	i := 1
	if i+2 > len(tpduBytes) {
		return 0, 0, 0, 0, 0, false
	}
	oaLen := int(tpduBytes[i])
	i += 2 // OA length + TOA
	oaOctets := (oaLen + 1) / 2
	if i+oaOctets > len(tpduBytes) {
		return 0, 0, 0, 0, 0, false
	}
	i += oaOctets

	if i+10 > len(tpduBytes) {
		return 0, 0, 0, 0, 0, false
	}
	dcs = tpduBytes[i+1]
	i += 2 + 7
	udl = int(tpduBytes[i])
	i++

	var err error
	alphabet, err = tpdu.DCS(dcs).Alphabet()
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}

	udOctets = udl
	if alphabet == tpdu.Alpha7Bit {
		udOctets = (udl*7 + 7) / 8
	}
	if i+udOctets > len(tpduBytes) {
		return 0, 0, 0, 0, 0, false
	}
	return dcs, udl, i, udOctets, alphabet, true
}
