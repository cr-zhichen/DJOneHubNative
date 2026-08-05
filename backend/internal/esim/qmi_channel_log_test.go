package esim

import (
	"testing"
	"time"
)

func TestShouldLogQMIAPDUSuccessSuppressesOrdinaryLatency(t *testing.T) {
	if shouldLogQMIAPDUSuccess(95 * time.Millisecond) {
		t.Fatal("ordinary QMI APDU latency should not be logged")
	}
	if !shouldLogQMIAPDUSuccess(750 * time.Millisecond) {
		t.Fatal("slow QMI APDU latency should still be logged")
	}
}
