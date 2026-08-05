package smscodec

import (
	"encoding/hex"
	"strings"
	"testing"
)

const screenshotPDUShort = "079144872000302320048102020000625061028204401AD9775D0E72D7DBE2B21C949E8360B75A4E7683D16AB71B"
const fixedSlotPaddedPDU = "0791448720003023400ED0E7B4D97C0E9BCD000062500221230140A00500036A0402CAA0B49B5E96BBCB741DE81C369B5DECFC8B2E0FDBCBEC3099FC76CF158A6198CD9E83C6EF391D1488B960AF76DA5DA79741F437A81D5E9741613719242F8FCB697BD905A296F1F439282C2F8366303888FE06CDCB6E32485CA783CCF27219447F83E4E571396D2FBB40C4303D0C4ACF416374587E2E9341613A480683BF9A429742617CCB41000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

func TestTrimFullPDUHexByTPDULengthRemovesStoragePadding(t *testing.T) {
	padded := screenshotPDUShort + strings.Repeat("00", 128)
	got, trimmed := TrimFullPDUHexByTPDULength(padded, 38)
	if !trimmed {
		t.Fatal("TrimFullPDUHexByTPDULength trimmed=false, want true")
	}
	if got != screenshotPDUShort {
		t.Fatalf("trimmed PDU mismatch\ngot  %s\nwant %s", got, screenshotPDUShort)
	}
}

func TestTrimFullPDUHexByTPDULengthFallsBackToTPDUDeclaredLength(t *testing.T) {
	got, trimmed := TrimFullPDUHexByTPDULength(fixedSlotPaddedPDU, 247)
	if !trimmed {
		t.Fatal("TrimFullPDUHexByTPDULength trimmed=false, want true")
	}
	if want := fixedSlotPaddedPDU[:336]; got != want {
		t.Fatalf("trimmed PDU mismatch\ngot  %s\nwant %s", got, want)
	}
}

func TestDecodeDeliverTPDUTrimsFixedSlotPadding(t *testing.T) {
	b, err := hexStringToBytesForTest(fixedSlotPaddedPDU)
	if err != nil {
		t.Fatal(err)
	}
	smscLen := int(b[0])
	tpduBytes := b[1+smscLen:]

	_, text, _, concat, err := DecodeDeliverTPDU(tpduBytes)
	if err != nil {
		t.Fatalf("DecodeDeliverTPDU() error = %v", err)
	}
	if text == "" {
		t.Fatal("DecodeDeliverTPDU() text is empty")
	}
	if !concat.IsConcat || concat.Total != 4 || concat.Seq != 2 {
		t.Fatalf("concat=%+v, want total=4 seq=2", concat)
	}
}

func TestDecodeDeliverTPDUAcceptsNonZeroGSM7SpareBits(t *testing.T) {
	tpduBytes, err := hexStringToBytesForTest("04038101F100006250724190410A3754747A0E4ABBCD6F793B4C4FBFDDA0F41CE47ED341617B38CD0E8BD96590F92D07E5DF7539283C1EBFEB6E3A889E87971B")
	if err != nil {
		t.Fatal(err)
	}

	sender, text, _, concat, err := DecodeDeliverTPDU(tpduBytes)
	if err != nil {
		t.Fatalf("DecodeDeliverTPDU() error = %v", err)
	}
	if sender != "101" {
		t.Fatalf("sender=%q want 101", sender)
	}
	if text != "This information is not available for your account type" {
		t.Fatalf("text=%q", text)
	}
	if concat.IsConcat {
		t.Fatalf("concat=%+v, want non-concat", concat)
	}
}

func TestParseATSMSHeaderTPDULengthUsesLastNumericField(t *testing.T) {
	got, ok := ParseATSMSHeaderTPDULength(`+CMGL: 7,1,,38`)
	if !ok || got != 38 {
		t.Fatalf("ParseATSMSHeaderTPDULength()=(%d,%v), want (38,true)", got, ok)
	}
}

func TestTrimFullPDUHexByATHeaderKeepsRawWhenHeaderLengthMissing(t *testing.T) {
	padded := screenshotPDUShort + "00"
	got, trimmed := TrimFullPDUHexByATHeader(padded, `+CMGR: 0`)
	if trimmed {
		t.Fatal("TrimFullPDUHexByATHeader trimmed=true, want false")
	}
	if got != padded {
		t.Fatalf("got %q want original %q", got, padded)
	}
}

func hexStringToBytesForTest(s string) ([]byte, error) {
	return hex.DecodeString(s)
}
