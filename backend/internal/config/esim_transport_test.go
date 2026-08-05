package config

import "testing"

func TestNormalizeESIMTransport(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ESIMTransportAT},
		{"AT", ESIMTransportAT},
		{" qMi ", ESIMTransportQMI},
		{" MBIM ", ESIMTransportMBIM},
	}

	for _, tc := range tests {
		if got := NormalizeESIMTransport(tc.in); got != tc.want {
			t.Fatalf("NormalizeESIMTransport(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateESIMTransport(t *testing.T) {
	if err := ValidateESIMTransport("qmi"); err != nil {
		t.Fatalf("ValidateESIMTransport(qmi) returned error: %v", err)
	}
	if err := ValidateESIMTransport("mbim"); err != nil {
		t.Fatalf("ValidateESIMTransport(mbim) returned error: %v", err)
	}
	if err := ValidateESIMTransport("bad"); err == nil {
		t.Fatal("expected invalid transport error, got nil")
	}
}
