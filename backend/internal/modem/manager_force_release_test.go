package modem

import (
	"os"
	"testing"
)

func TestParseFuserPIDs(t *testing.T) {
	raw := "/dev/ttyUSB2: 1234 5678c 1234 9999\n"
	got := parseFuserPIDs(raw)
	if len(got) != 3 {
		t.Fatalf("expected 3 unique pids, got %d (%v)", len(got), got)
	}
	want := map[int]struct{}{1234: {}, 5678: {}, 9999: {}}
	for _, pid := range got {
		if _, ok := want[pid]; !ok {
			t.Fatalf("unexpected pid parsed: %d (all=%v)", pid, got)
		}
	}
}

func TestCurrentProcessTaskPIDSetContainsSelf(t *testing.T) {
	self := os.Getpid()
	set := currentProcessTaskPIDSet()
	if _, ok := set[self]; !ok {
		t.Fatalf("expected self pid %d in task pid set", self)
	}
}

func TestIsRetryableSerialOpenErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "busy", err: assertErr("device or resource busy"), want: true},
		{name: "temp unavailable", err: assertErr("temporarily unavailable"), want: true},
		{name: "permission", err: assertErr("permission denied"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRetryableSerialOpenErr(tc.err)
			if got != tc.want {
				t.Fatalf("isRetryableSerialOpenErr(%v)=%v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
