package modem

import "testing"

func TestDecodePNNRecord(t *testing.T) {
	data := append([]byte{0x43, 0x05}, []byte("China")...)
	data = append(data, 0x45, 0x02)
	data = append(data, []byte("CM")...)
	data = append(data, 0xFF, 0xFF)

	record, ok := DecodePNNRecord(1, data)
	if !ok {
		t.Fatal("DecodePNNRecord() ok=false")
	}
	if record.Record != 1 || record.FullName != "China" || record.ShortName != "CM" {
		t.Fatalf("DecodePNNRecord()=%+v", record)
	}
	if record.RawHex != "43054368696E614502434D" {
		t.Fatalf("RawHex=%q", record.RawHex)
	}
}

func TestDecodePNNRecordGSM7NetworkName(t *testing.T) {
	record, ok := DecodePNNRecord(1, []byte{0x43, 0x0A, 0x82, 0x43, 0x6A, 0x11, 0x3F, 0x2E, 0xB3, 0xC5, 0x69, 0x3D})
	if !ok {
		t.Fatal("DecodePNNRecord() ok=false")
	}
	if record.FullName != "CTExcelbiz" {
		t.Fatalf("FullName=%q want CTExcelbiz", record.FullName)
	}
}

func TestDecodePNNRecordTrimsDirtyTail(t *testing.T) {
	record, ok := DecodePNNRecord(1, []byte{
		0x43, 0x0A, 0x82, 0x43, 0x6A, 0x11, 0x3F, 0x2E, 0xB3, 0xC5, 0x69, 0x3D,
		0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00, 0xD7, 0xE9,
	})
	if !ok {
		t.Fatal("DecodePNNRecord() ok=false")
	}
	if record.FullName != "CTExcelbiz" {
		t.Fatalf("FullName=%q want CTExcelbiz", record.FullName)
	}
	if record.RawHex != "430A82436A113F2EB3C5693D" {
		t.Fatalf("RawHex=%q want valid TLV only", record.RawHex)
	}
}

func TestDecodePNNRecordRejectsPaddingOnly(t *testing.T) {
	if record, ok := DecodePNNRecord(1, []byte{0xFF, 0xFF, 0x00, 0x00}); ok {
		t.Fatalf("DecodePNNRecord()=%+v, true; want false", record)
	}
}

func TestDecodeOPLRecord(t *testing.T) {
	record, ok := DecodeOPLRecord(2, []byte{0x64, 0xF0, 0x10, 0x00, 0x01, 0xFF, 0xFE, 0x03})
	if !ok {
		t.Fatal("DecodeOPLRecord() ok=false")
	}
	if record.Record != 2 || record.PLMN != "46001" || record.LACStart != 1 || record.LACEnd != 65534 || record.PNNRecord != 3 {
		t.Fatalf("DecodeOPLRecord()=%+v", record)
	}
}

func TestNativeMCCMNCFromOPLRecordsUsesFirstExactPLMN(t *testing.T) {
	mcc, mnc, ok := NativeMCCMNCFromOPLRecords([]OPLRecord{
		{Record: 1, PLMN: "51566"},
		{Record: 2, PLMN: "20404"},
	})
	if !ok {
		t.Fatal("NativeMCCMNCFromOPLRecords() ok=false")
	}
	if mcc != "515" || mnc != "66" {
		t.Fatalf("mcc/mnc = %s/%s, want 515/66", mcc, mnc)
	}
}

func TestNativeMCCMNCFromOPLRecordsSkipsWildcardPLMN(t *testing.T) {
	mcc, mnc, ok := NativeMCCMNCFromOPLRecords([]OPLRecord{
		{Record: 1, PLMN: "515xx"},
		{Record: 2, PLMN: "310260"},
	})
	if !ok {
		t.Fatal("NativeMCCMNCFromOPLRecords() ok=false")
	}
	if mcc != "310" || mnc != "260" {
		t.Fatalf("mcc/mnc = %s/%s, want 310/260", mcc, mnc)
	}
}

func TestHomeMCCMNCFromIMSIAndEFADUsesTwoDigitMNC(t *testing.T) {
	mcc, mnc, mncLen, source, err := HomeMCCMNCFromIMSIAndEFAD("234336575868434", []byte{0x00, 0x00, 0x00, 0x02})
	if err != nil {
		t.Fatalf("HomeMCCMNCFromIMSIAndEFAD() error = %v", err)
	}
	if mcc != "234" || mnc != "33" || mncLen != 2 || source != "imsi_efad" {
		t.Fatalf("mcc/mnc/len/source = %s/%s/%d/%s, want 234/33/2/imsi_efad", mcc, mnc, mncLen, source)
	}
}

func TestHomeMCCMNCFromIMSIAndEFADUsesThreeDigitMNC(t *testing.T) {
	mcc, mnc, mncLen, source, err := HomeMCCMNCFromIMSIAndEFAD("234336575868434", []byte{0x00, 0x00, 0x00, 0x03})
	if err != nil {
		t.Fatalf("HomeMCCMNCFromIMSIAndEFAD() error = %v", err)
	}
	if mcc != "234" || mnc != "336" || mncLen != 3 || source != "imsi_efad" {
		t.Fatalf("mcc/mnc/len/source = %s/%s/%d/%s, want 234/336/3/imsi_efad", mcc, mnc, mncLen, source)
	}
}

func TestHomeMCCMNCFromIMSIAndEFADFallsBackToHeuristic(t *testing.T) {
	mcc, mnc, mncLen, source, err := HomeMCCMNCFromIMSIAndEFAD("310280233641503", nil)
	if err != nil {
		t.Fatalf("HomeMCCMNCFromIMSIAndEFAD() error = %v", err)
	}
	if mcc != "310" || mnc != "280" || mncLen != 3 || source != "imsi_heuristic" {
		t.Fatalf("mcc/mnc/len/source = %s/%s/%d/%s, want 310/280/3/imsi_heuristic", mcc, mnc, mncLen, source)
	}
}

func TestDecodeSIMServiceTable(t *testing.T) {
	table := DecodeSIMServiceTable("UST", []byte{0x05, 0x80, 0xFF})
	if table == nil {
		t.Fatal("DecodeSIMServiceTable() nil")
	}
	if table.RawHex != "0580" {
		t.Fatalf("RawHex=%q", table.RawHex)
	}
	want := []int{1, 3, 16}
	if len(table.EnabledServices) != len(want) {
		t.Fatalf("EnabledServices=%v want=%v", table.EnabledServices, want)
	}
	for i := range want {
		if table.EnabledServices[i] != want[i] {
			t.Fatalf("EnabledServices=%v want=%v", table.EnabledServices, want)
		}
	}
}

func TestSIMRawHexTrimsPadding(t *testing.T) {
	if got := simRawHex([]byte{0x41, 0x42, 0x00, 0xFF}); got != "4142" {
		t.Fatalf("simRawHex()=%q want=4142", got)
	}
}
