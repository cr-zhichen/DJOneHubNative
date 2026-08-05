package smscodec

import (
	"testing"

	"github.com/warthog618/sms/encoding/tpdu"
	"github.com/warthog618/sms/encoding/ucs2"
)

func TestBuildSubmitTPDUsWithOptionsForcesUCS2(t *testing.T) {
	tpdus, _, err := BuildSubmitTPDUsWithOptions("10086", "hello", SubmitOptions{Encoding: SMSEncodingUCS2})
	if err != nil {
		t.Fatalf("BuildSubmitTPDUsWithOptions() error = %v", err)
	}
	if len(tpdus) != 1 {
		t.Fatalf("parts=%d want 1", len(tpdus))
	}

	pdu := &tpdu.TPDU{Direction: tpdu.MO}
	if err := pdu.UnmarshalBinary(tpdus[0]); err != nil {
		t.Fatalf("UnmarshalBinary() error = %v", err)
	}
	if pdu.DCS != tpdu.DcsUCS2Data {
		t.Fatalf("DCS=0x%02x want 0x%02x", byte(pdu.DCS), byte(tpdu.DcsUCS2Data))
	}
	if got, want := []byte(pdu.UD), ucs2.Encode([]rune("hello")); string(got) != string(want) {
		t.Fatalf("UD=%x want UCS2 %x", got, want)
	}
}

func TestBuildSubmitTPDUsKeepsAutoEncodingByDefault(t *testing.T) {
	tpdus, _, err := BuildSubmitTPDUsWithOptions("10086", "hello", SubmitOptions{})
	if err != nil {
		t.Fatalf("BuildSubmitTPDUs() error = %v", err)
	}
	if len(tpdus) != 1 {
		t.Fatalf("parts=%d want 1", len(tpdus))
	}

	pdu := &tpdu.TPDU{Direction: tpdu.MO}
	if err := pdu.UnmarshalBinary(tpdus[0]); err != nil {
		t.Fatalf("UnmarshalBinary() error = %v", err)
	}
	if pdu.DCS != 0x00 {
		t.Fatalf("DCS=0x%02x want auto GSM7 0x00", byte(pdu.DCS))
	}
}
