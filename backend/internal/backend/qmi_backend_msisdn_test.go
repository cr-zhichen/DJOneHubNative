package backend

import (
	"context"
	"testing"
)

func TestQMIBackendGetMSISDNPassThrough(t *testing.T) {
	src := &qmiBackendSendSourceStub{}
	srcMSISDN := "+8613800138000"
	src.getMSISDN = func(ctx context.Context) (string, error) {
		return srcMSISDN, nil
	}

	backend, err := NewQMIBackend("/dev/null", src)
	if err != nil {
		t.Fatalf("NewQMIBackend failed: %v", err)
	}

	got, err := backend.GetMSISDN(context.Background())
	if err != nil {
		t.Fatalf("GetMSISDN failed: %v", err)
	}
	if got != srcMSISDN {
		t.Fatalf("GetMSISDN()=%q want=%q", got, srcMSISDN)
	}
}

func TestQMIBackendGetMSISDNAddsPlusPrefixForBareDigits(t *testing.T) {
	src := &qmiBackendSendSourceStub{}
	src.getMSISDN = func(ctx context.Context) (string, error) {
		return "8613800138000", nil
	}

	backend, err := NewQMIBackend("/dev/null", src)
	if err != nil {
		t.Fatalf("NewQMIBackend failed: %v", err)
	}

	got, err := backend.GetMSISDN(context.Background())
	if err != nil {
		t.Fatalf("GetMSISDN failed: %v", err)
	}
	if got != "+8613800138000" {
		t.Fatalf("GetMSISDN()=%q want=%q", got, "+8613800138000")
	}
}
