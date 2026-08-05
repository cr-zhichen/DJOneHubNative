package mbim

import "testing"

func TestBuildQMIUIMPowerRequestUsesSlotTLV(t *testing.T) {
	off := buildQMIUIMPowerRequest(0x21, 0x0030, 2)
	on := buildQMIUIMPowerRequest(0x21, 0x0031, 2)

	if got := le.Uint16(off[9:11]); got != 0x0030 {
		t.Fatalf("power-off msgID=0x%04X want 0x0030", got)
	}
	if got := le.Uint16(on[9:11]); got != 0x0031 {
		t.Fatalf("power-on msgID=0x%04X want 0x0031", got)
	}
	if off[len(off)-4] != 0x01 || off[len(off)-1] != 0x02 {
		t.Fatalf("power-off slot TLV=% X want type=0x01 slot=0x02", off[len(off)-4:])
	}
}

func TestParseActiveSlotReturnsLogicalSlotAndIndexFallback(t *testing.T) {
	logical, known, source, err := ParseActiveSlot(TestQMIUIMGetCardStatusResp(2, 0, true))
	if err != nil {
		t.Fatalf("ParseActiveSlot(logical): %v", err)
	}
	if !known || logical != 2 || source != "uim_slot_status" {
		t.Fatalf("logical slot result=(%d,%v,%q)", logical, known, source)
	}

	logical, known, source, err = ParseActiveSlot(TestQMIUIMGetCardStatusResp(0, 1, true))
	if err != nil {
		t.Fatalf("ParseActiveSlot(index fallback): %v", err)
	}
	if !known || logical != 2 || source != "uim_slot_status_index" {
		t.Fatalf("index fallback result=(%d,%v,%q)", logical, known, source)
	}
}
