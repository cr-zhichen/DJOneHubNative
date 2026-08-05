package mbim

import "testing"

func TestMonitorDispatchesConnectIndication(t *testing.T) {
	m := NewMonitor(nil)
	got := make(chan ConnectState, 1)
	m.SetOnConnect(func(s ConnectState) { got <- s })

	buf := make([]byte, 36)
	le.PutUint32(buf[4:], ActivationStateDeactivated)
	le.PutUint32(buf[32:], 42)
	m.apply(Indication{Service: UUIDBasicConnect, CID: CIDBasicConnectConnect, InfoBuffer: buf})

	select {
	case s := <-got:
		if s.ActivationState != ActivationStateDeactivated {
			t.Fatalf("state = %d, want deactivated", s.ActivationState)
		}
		if s.NwError != 42 {
			t.Fatalf("nw error = %d, want 42", s.NwError)
		}
	default:
		t.Fatal("onConnect callback was not invoked")
	}
}
