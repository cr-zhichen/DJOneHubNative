package config

import "testing"

func TestNormalizeMBIMTransport(t *testing.T) {
	cases := map[string]string{
		"":       MBIMTransportAuto,
		"AUTO":   MBIMTransportAuto,
		"proxy":  MBIMTransportProxy,
		"direct": MBIMTransportDirect,
		"bogus":  MBIMTransportAuto,
	}
	for in, want := range cases {
		if got := NormalizeMBIMTransport(in); got != want {
			t.Fatalf("NormalizeMBIMTransport(%q) = %q, want %q", in, got, want)
		}
	}
}
