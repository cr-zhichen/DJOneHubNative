package modem

import "testing"

func TestParseCOPSScan(t *testing.T) {
	resp := "\r\n+COPS: (2,\"CHN-UNICOM\",\"UNICOM\",\"46001\",7),(3,\"CHINA MOBILE\",\"CMCC\",\"46000\",7),,(0,1,2,3,4),(0,1,2)\r\n\r\nOK\r\n"
	got := parseCOPSScan(resp)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(got), got)
	}
	if got[0].Status != 2 || got[0].PLMN != "46001" || got[0].Act != 7 {
		t.Fatalf("first=%+v", got[0])
	}
	if got[1].Status != 3 || got[1].PLMN != "46000" {
		t.Fatalf("second=%+v", got[1])
	}
}

func TestParseCOPSSelection(t *testing.T) {
	got, ok := parseCOPSSelection("\r\n+COPS: 1,2,\"46001\",7\r\n\r\nOK\r\n")
	if !ok {
		t.Fatal("ok=false")
	}
	if got.Mode != 1 || got.Format != 2 || got.PLMN != "46001" || got.Act != 7 || !got.HasAct {
		t.Fatalf("got=%+v", got)
	}

	got, ok = parseCOPSSelection("\r\n+COPS: 0\r\n\r\nOK\r\n")
	if !ok {
		t.Fatal("auto ok=false")
	}
	if got.Mode != 0 || got.PLMN != "" {
		t.Fatalf("auto got=%+v", got)
	}
}
