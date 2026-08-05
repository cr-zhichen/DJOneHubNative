package simaid

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("DecodeString(%q) error = %v", s, err)
	}
	return b
}

func TestAIDClassificationAcceptsShortAndFullUSIMISIM(t *testing.T) {
	if !IsUSIM(mustHex(t, "A0000000871002")) {
		t.Fatal("short USIM AID was not classified as USIM")
	}
	if !IsUSIM(mustHex(t, "A0000000871002FF49FF0189")) {
		t.Fatal("full USIM AID was not classified as USIM")
	}
	if !IsISIM(mustHex(t, "A0000000871004")) {
		t.Fatal("short ISIM AID was not classified as ISIM")
	}
	if IsUSIM(mustHex(t, "A0000000871004")) {
		t.Fatal("ISIM AID was classified as USIM")
	}
}

func TestCollectTLVValuesFindsNestedAIDs(t *testing.T) {
	record := mustHex(t, "61114F0CA0000000871002FF49FF0189500101")

	got := CollectTLVValues(record, 0x4F)
	want := [][]byte{mustHex(t, "A0000000871002FF49FF0189")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectTLVValues() = %X, want %X", got, want)
	}
}

func TestAppendUniqueAIDsClonesAndDeduplicates(t *testing.T) {
	aid := mustHex(t, "A0000000871002FF49FF0189")
	got := AppendUniqueAIDs(nil, aid, aid)
	if len(got) != 1 {
		t.Fatalf("len(AppendUniqueAIDs()) = %d, want 1", len(got))
	}
	aid[0] = 0x00
	if bytes.Equal(got[0], aid) {
		t.Fatal("AppendUniqueAIDs retained caller-owned backing array")
	}
}

func TestReadDirectoryAIDsReadsLinearFixedEFDir(t *testing.T) {
	var calls [][]byte
	responses := map[string][]byte{
		"00a40004023f00": mustHex(t, "9000"),
		"00a40004022f00": mustHex(t, "621482054221000A018002000A83022F008A01058B032F06019000"),
		"00b201040a":     mustHex(t, "61114F0CA0000000871002FF49FF01895001019000"),
	}
	transmit := func(apdu []byte) ([]byte, error) {
		calls = append(calls, append([]byte(nil), apdu...))
		if rsp := responses[hex.EncodeToString(apdu)]; rsp != nil {
			return append([]byte(nil), rsp...), nil
		}
		return mustHex(t, "6A83"), nil
	}

	got, err := ReadDirectoryAIDs(transmit)
	if err != nil {
		t.Fatalf("ReadDirectoryAIDs() error = %v", err)
	}
	want := [][]byte{mustHex(t, "A0000000871002FF49FF0189")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadDirectoryAIDs() = %X, want %X", got, want)
	}
	wantCalls := [][]byte{
		mustHex(t, "00A40004023F00"),
		mustHex(t, "00A40004022F00"),
		mustHex(t, "00B201040A"),
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %X, want %X", calls, wantCalls)
	}
}
