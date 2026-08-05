package config

import "testing"

func TestNormalizeIMEI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain 15-digit", "864388041069422", "86438804106942"},
		{"trailing space", "864388041069422 ", "86438804106942"},
		{"leading space + newline", " 864388041069422\n", "86438804106942"},
		{"imeisv 16-digit", "8643880410694201", "86438804106942"},
		{"embedded non-digits", "86-4388 0410.69422", "86438804106942"},
		{"too short", "12345", ""},
		{"empty", "", ""},
		{"exactly 14", "86438804106942", "86438804106942"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeIMEI(tc.in); got != tc.want {
				t.Fatalf("NormalizeIMEI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIMEIMatches(t *testing.T) {
	if !IMEIMatches("864388041069422", " 864388041069422") {
		t.Fatal("whitespace-differing same IMEI should match")
	}
	if !IMEIMatches("864388041069422", "8643880410694201") {
		t.Fatal("IMEI(15) and IMEISV(16) of same modem should match")
	}
	if IMEIMatches("864388041069422", "864513045234397") {
		t.Fatal("different modems must not match")
	}
	if IMEIMatches("", "") {
		t.Fatal("empty must never match")
	}
	if IMEIMatches("12345", "12345") {
		t.Fatal("invalid (<14 digits) must never match")
	}
}
