package config

import "strings"

// NormalizeIMEI returns the 14-digit TAC+SNR identity key used for IMEI equality.
// It is comparison-only: callers should keep storing and displaying the original
// full IMEI value.
func NormalizeIMEI(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) < 14 {
		return ""
	}
	return digits[:14]
}

// IMEIMatches reports whether two IMEI values identify the same modem.
// Empty or invalid IMEI values never match, even against themselves.
func IMEIMatches(a, b string) bool {
	normalized := NormalizeIMEI(a)
	return normalized != "" && normalized == NormalizeIMEI(b)
}
