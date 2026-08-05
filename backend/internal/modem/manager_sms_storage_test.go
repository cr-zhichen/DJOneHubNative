package modem

import (
	"reflect"
	"testing"
	"time"
)

func TestHandleCMTIUsesIndicatedStorageForReadAndDelete(t *testing.T) {
	m := newRunningTestManager(t)
	validPDU := "079144872000302320048102020000625061028204401AD9775D0E72D7DBE2B21C949E8360B75A4E7683D16AB71B"

	done := make(chan []string, 1)
	go func() {
		done <- respondToCommands(t, m, 5, func(req commandRequest) {
			switch req.cmd {
			case "AT+CPMS?":
				req.respChan <- "\r\n+CPMS: \"SM\",0,10,\"SM\",0,10,\"SM\",0,10\r\n\r\nOK\r\n"
			case `AT+CPMS="ME","ME","ME"`:
				req.respChan <- "OK"
			case "AT+CMGR=7":
				req.respChan <- "\r\n+CMGR: 0,,38\r\n" + validPDU + "\r\n\r\nOK\r\n"
			case "AT+CMGD=7":
				req.respChan <- "OK"
			case `AT+CPMS="SM","SM","SM"`:
				req.respChan <- "OK"
			default:
				req.errChan <- nil
			}
		})
	}()

	m.handleURC(`+CMTI: "ME",7`)

	var got []string
	select {
	case got = <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for storage-aware CMTI handling")
	}
	want := []string{
		"AT+CPMS?",
		`AT+CPMS="ME","ME","ME"`,
		"AT+CMGR=7",
		"AT+CMGD=7",
		`AT+CPMS="SM","SM","SM"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands=%#v want %#v", got, want)
	}
}
