package mbim

import "testing"

func TestUUIDStringAndEqual(t *testing.T) {
	got := UUIDBasicConnect.String()
	want := "a289cc33-bcbb-8b4f-b6b0-133ec2aae6df"
	if got != want {
		t.Fatalf("BasicConnect UUID = %s, want %s", got, want)
	}
	if !UUIDBasicConnect.Equal(UUIDBasicConnect) {
		t.Fatal("UUID should equal itself")
	}
	if UUIDBasicConnect.Equal(UUIDProxyControl) {
		t.Fatal("different service UUIDs should not be equal")
	}
}

func TestMessageTypeConstants(t *testing.T) {
	tests := map[string]struct {
		got  MessageType
		want MessageType
	}{
		"OPEN":            {MessageTypeOpen, 0x00000001},
		"CLOSE":           {MessageTypeClose, 0x00000002},
		"COMMAND":         {MessageTypeCommand, 0x00000003},
		"HOST_ERROR":      {MessageTypeHostError, 0x00000004},
		"OPEN_DONE":       {MessageTypeOpenDone, 0x80000001},
		"CLOSE_DONE":      {MessageTypeCloseDone, 0x80000002},
		"COMMAND_DONE":    {MessageTypeCommandDone, 0x80000003},
		"FUNCTION_ERROR":  {MessageTypeFunctionError, 0x80000004},
		"INDICATE_STATUS": {MessageTypeIndicateStatus, 0x80000007},
	}
	for name, tt := range tests {
		if tt.got != tt.want {
			t.Fatalf("%s = %#x, want %#x", name, tt.got, tt.want)
		}
	}
}

func TestUUIDSMS(t *testing.T) {
	if UUIDSMS.String() != "533fbeeb-14fe-4467-9f90-33a223e56c3f" {
		t.Fatalf("SMS UUID = %s", UUIDSMS.String())
	}
	if CIDSMSConfiguration != 1 || CIDSMSRead != 2 || CIDSMSSend != 3 || CIDSMSDelete != 4 {
		t.Fatal("SMS CID constants have unexpected values")
	}
}

func TestUUIDUICC(t *testing.T) {
	if UUIDMSUICCLowLevelAccess.String() != "c2f6588e-f037-4bc9-8665-f4d44bd09367" {
		t.Fatalf("UICC UUID = %s", UUIDMSUICCLowLevelAccess.String())
	}
	if CIDUICCOpenChannel != 2 || CIDUICCCloseChannel != 3 || CIDUICCAPDU != 4 {
		t.Fatal("UICC CID constants have unexpected values")
	}
}

func TestUSSDProtocolConstants(t *testing.T) {
	if ServiceUSSD != 3 {
		t.Fatalf("ServiceUSSD = %d, want %d", ServiceUSSD, 3)
	}
	if CIDUSSD != 1 {
		t.Fatalf("CIDUSSD = %d, want %d", CIDUSSD, 1)
	}
	if USSDActionInitiate != 0 || USSDActionContinue != 1 || USSDActionCancel != 2 {
		t.Fatal("USSD action constants have unexpected values")
	}
	if USSDRespNoActionRequired != 0 ||
		USSDRespActionRequired != 1 ||
		USSDRespTerminated != 2 ||
		USSDRespOtherLocalClient != 3 ||
		USSDRespOperationNotSupported != 4 ||
		USSDRespNetworkTimeout != 5 {
		t.Fatal("USSD response constants have unexpected values")
	}
	if UUIDUSSD.String() != "e550a0c8-5e82-479e-82f7-10abf4c3351f" {
		t.Fatalf("USSD UUID = %s", UUIDUSSD.String())
	}
}

func TestOperatorSelectionProtocolConstants(t *testing.T) {
	if CIDBasicConnectVisibleProviders != 8 {
		t.Fatalf("CIDBasicConnectVisibleProviders = %d, want 8", CIDBasicConnectVisibleProviders)
	}
	if RegisterActionAutomatic != 0 || RegisterActionManual != 1 {
		t.Fatalf("register actions = automatic:%d manual:%d", RegisterActionAutomatic, RegisterActionManual)
	}
	if CellularClassGSM != 1 || CellularClassCDMA != 2 {
		t.Fatalf("cellular classes = gsm:%d cdma:%d", CellularClassGSM, CellularClassCDMA)
	}
}
