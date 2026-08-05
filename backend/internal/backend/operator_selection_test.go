package backend

import (
	"testing"
)

func TestNormalizePLMNSelection(t *testing.T) {
	tests := []struct {
		plmn      string
		mcc       string
		mnc       string
		mncLength int
		pcs       bool
		wantErr   bool
	}{
		{plmn: "46000", mcc: "460", mnc: "00", mncLength: 2, pcs: false},
		{plmn: "310260", mcc: "310", mnc: "260", mncLength: 3, pcs: true},
		{plmn: "46A00", wantErr: true},
		{plmn: "460", wantErr: true},
	}

	for _, tt := range tests {
		got, err := NormalizePLMNSelection(tt.plmn)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("NormalizePLMNSelection(%q) error=nil", tt.plmn)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NormalizePLMNSelection(%q) error=%v", tt.plmn, err)
		}
		if got.MCC != tt.mcc || got.MNC != tt.mnc || got.MNCLength != tt.mncLength || got.IncludesPCSDigit != tt.pcs {
			t.Fatalf("NormalizePLMNSelection(%q)=%+v", tt.plmn, got)
		}
	}
}

func TestValidateSetOperatorSelectionRequest(t *testing.T) {
	tests := []struct {
		req     SetOperatorSelectionRequest
		wantErr bool
	}{
		{req: SetOperatorSelectionRequest{Mode: OperatorSelectionAutomatic}, wantErr: false},
		{req: SetOperatorSelectionRequest{Mode: OperatorSelectionManual, PLMN: "46000"}, wantErr: false},
		{req: SetOperatorSelectionRequest{Mode: OperatorSelectionManual, PLMN: "46A00"}, wantErr: true},
		{req: SetOperatorSelectionRequest{Mode: OperatorSelectionManual}, wantErr: true},
		{req: SetOperatorSelectionRequest{Mode: "invalid"}, wantErr: true},
	}

	for _, tt := range tests {
		err := ValidateSetOperatorSelectionRequest(tt.req)
		if tt.wantErr && err == nil {
			t.Fatalf("ValidateSetOperatorSelectionRequest(%+v) error=nil", tt.req)
		}
		if !tt.wantErr && err != nil {
			t.Fatalf("ValidateSetOperatorSelectionRequest(%+v) error=%v", tt.req, err)
		}
	}
}
