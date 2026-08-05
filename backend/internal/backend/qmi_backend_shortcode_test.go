package backend

import (
	"testing"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"
)

func TestIsLikelyShortCode(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "empty", input: "", want: false},
		{name: "international", input: "+491701234567", want: false},
		{name: "normal local", input: "13800138000", want: false},
		{name: "short code", input: "10086", want: true},
		{name: "short code with spaces", input: " 56656 ", want: true},
		{name: "contains letter", input: "10A86", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLikelyShortCode(tc.input); got != tc.want {
				t.Fatalf("isLikelyShortCode(%q)=%v want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeSubmitDestinationForShortCode(t *testing.T) {
	pdus, err := sms.Encode([]byte("x"), sms.AsSubmit, sms.To("10086"))
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(pdus) == 0 {
		t.Fatal("no pdu generated")
	}

	pdu := &pdus[0]
	if got := pdu.DA.TypeOfNumber(); got != tpdu.TonInternational {
		t.Fatalf("unexpected precondition ToN=%v want %v", got, tpdu.TonInternational)
	}

	normalizeSubmitDestinationForShortCode(pdu)

	if got := pdu.DA.TypeOfNumber(); got != tpdu.TonUnknown {
		t.Fatalf("ToN after normalize=%v want %v", got, tpdu.TonUnknown)
	}
	if got := pdu.DA.NumberingPlan(); got != tpdu.NpISDN {
		t.Fatalf("NP after normalize=%v want %v", got, tpdu.NpISDN)
	}
}
