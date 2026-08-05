package mbim

import "testing"

func TestCapabilitiesAuthAKAUsable(t *testing.T) {
	c := &Capabilities{
		Services: DeviceServices{Elements: []DeviceServiceElement{
			{Service: UUIDAuth, CIDs: []uint32{1}},
		}},
	}
	if !c.AuthAKAUsable() {
		t.Fatal("宣告 Auth 应可用")
	}
	c.MarkAuthAKADead()
	if c.AuthAKAUsable() {
		t.Fatal("熔断后应不可用")
	}
}

func TestCapabilitiesAuthAKANotAdvertised(t *testing.T) {
	c := &Capabilities{Services: DeviceServices{}}
	if c.AuthAKAUsable() {
		t.Fatal("未宣告 Auth 不应可用")
	}
}

func TestCapabilitiesUICCChannelAndMBIMEx(t *testing.T) {
	c := &Capabilities{UICCChannelOK: true, MBIMExOK: true, QMIOverMBIMOK: true}
	if !c.UICCChannelAKAUsable() || !c.MBIMExUsable() || !c.QMIReadUsable() {
		t.Fatal("探针位应透传")
	}
}

func TestCapabilitiesAppListKnownUnsupported(t *testing.T) {
	uiccAdvertised := DeviceServices{Elements: []DeviceServiceElement{
		{Service: UUIDMSUICCLowLevelAccess, CIDs: []uint32{7}},
	}}
	if !(&Capabilities{Services: uiccAdvertised, AppListOK: false}).AppListKnownUnsupported() {
		t.Fatal("宣告 UICC 但探针失败应判为确知不支持")
	}
	if (&Capabilities{Services: uiccAdvertised, AppListOK: true}).AppListKnownUnsupported() {
		t.Fatal("探针成功不应判为不支持")
	}
	if (&Capabilities{}).AppListKnownUnsupported() {
		t.Fatal("未宣告 UICC 应为 unknown,不判为确知不支持")
	}
	if (*Capabilities)(nil).AppListKnownUnsupported() {
		t.Fatal("nil 应为 false")
	}
}

func TestCapabilitiesDeviceResetUsable(t *testing.T) {
	resetAdvertised := DeviceServices{Elements: []DeviceServiceElement{
		{Service: UUIDMSBasicConnectExtensions, CIDs: []uint32{CIDMSBasicConnectExtDeviceReset}},
	}}
	if !(&Capabilities{Services: resetAdvertised}).DeviceResetUsable() {
		t.Fatal("DeviceResetUsable() = false, want true when DEVICE_RESET is advertised")
	}
	if (&Capabilities{}).DeviceResetUsable() {
		t.Fatal("DeviceResetUsable() = true, want false without DEVICE_RESET")
	}
	if (*Capabilities)(nil).DeviceResetUsable() {
		t.Fatal("nil 应为 false")
	}
}
