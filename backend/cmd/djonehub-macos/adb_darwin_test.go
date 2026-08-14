//go:build darwin && cgo

package main

import (
	"encoding/hex"
	"testing"
)

func TestADBHeaderRoundTrip(t *testing.T) {
	payload := []byte("host::DJOneHub\x00")
	command := adbCommand("CNXN")
	header := adbEncodeHeader(command, adbVersion, adbMaxPayload, payload)
	if len(header) != 24 {
		t.Fatalf("header length = %d", len(header))
	}
	if got := leUint32(header[0:]); got != command {
		t.Fatalf("command = 0x%x", got)
	}
	if got := leUint32(header[12:]); got != uint32(len(payload)) {
		t.Fatalf("payload length = %d", got)
	}
	if got := leUint32(header[16:]); got != adbChecksum(payload) {
		t.Fatalf("checksum = %d", got)
	}
	if got := leUint32(header[20:]); got != command^0xffffffff {
		t.Fatalf("magic = 0x%x", got)
	}
}

func TestADBStatusUsesUnpredictableToken(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for index := 0; index < 64; index++ {
		token := adbToken()
		if len(token) < 16 {
			t.Fatalf("token too short: %q", token)
		}
		if _, err := hex.DecodeString(token); err != nil {
			t.Fatalf("token is not hex: %q", token)
		}
		if _, exists := seen[token]; exists {
			t.Fatalf("duplicate token: %q", token)
		}
		seen[token] = struct{}{}
		raw := "output\n__DJ_STATUS_" + token + "_17__\n"
		status, ok := parseADBStatus(raw, token)
		if !ok || status != 17 {
			t.Fatalf("status = %d, %v", status, ok)
		}
	}
}

func TestADBStatusParserRejectsTrailingDataAndOutOfRangeValues(t *testing.T) {
	token := "0123456789abcdef"
	for _, raw := range []string{
		"__DJ_STATUS_" + token + "_0x__",
		"__DJ_STATUS_" + token + "_-1__",
		"__DJ_STATUS_" + token + "_256__",
	} {
		if _, ok := parseADBStatus(raw, token); ok {
			t.Fatalf("invalid status unexpectedly accepted: %q", raw)
		}
	}
}

func TestADBSyncDonePacketUsesTimestampWordWithoutPayload(t *testing.T) {
	const timestamp = uint32(0x12345678)
	packet := adbSyncDonePacket(timestamp)
	if len(packet) != 8 {
		t.Fatalf("DONE packet length = %d, want 8", len(packet))
	}
	if string(packet[:4]) != "DONE" {
		t.Fatalf("DONE packet identifier = %q", packet[:4])
	}
	if got := leUint32(packet[4:]); got != timestamp {
		t.Fatalf("DONE timestamp = 0x%x, want 0x%x", got, timestamp)
	}
}
