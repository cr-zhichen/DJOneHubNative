package main

import "testing"

func TestParseMacNetworkServiceOrderMarksModuleInterface(t *testing.T) {
	const output = `An asterisk (*) denotes that a network service is disabled.
(1) DL-Dock
(Hardware Port: DL-Dock, Device: en7)

(2) Thunderbolt Bridge
(Hardware Port: Thunderbolt Bridge, Device: bridge0)

(3) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(4) Baiwang
(Hardware Port: Baiwang, Device: en8)
`

	services := parseMacNetworkServiceOrder(output, "Baiwang")
	if len(services) != 4 {
		t.Fatalf("len(services) = %d, want 4", len(services))
	}
	if services[0].Module {
		t.Fatal("DL-Dock must not be marked as the module interface")
	}
	module := services[3]
	if module.Name != "Baiwang" || module.Device != "en8" || !module.Module {
		t.Fatalf("module service = %+v, want Baiwang/en8 with module=true", module)
	}
}

func TestSelectModuleTrafficInterfaceIgnoresEarlierUSBInterfaces(t *testing.T) {
	interfaces := []macNetInterface{
		{Name: "en7", Status: "active", Kind: "ethernet"},
		{Name: "en0", Status: "active", Kind: "ethernet"},
		{Name: "en8", Status: "active", Kind: "ethernet"},
	}
	services := []networkService{
		{Name: "DL-Dock", Device: "en7", USB: true},
		{Name: "Wi-Fi", Device: "en0"},
		{Name: "Baiwang", Device: "en8", USB: true, Module: true},
	}

	name, active := selectModuleTrafficInterface(interfaces, services)
	if name != "en8" || !active {
		t.Fatalf("selectModuleTrafficInterface() = %q, %v; want en8, true", name, active)
	}
}

func TestSelectModuleTrafficInterfaceDoesNotFallback(t *testing.T) {
	interfaces := []macNetInterface{
		{Name: "en7", Status: "active", Kind: "ethernet"},
		{Name: "en8", Status: "inactive", Kind: "ethernet"},
	}

	t.Run("module inactive", func(t *testing.T) {
		services := []networkService{
			{Name: "DL-Dock", Device: "en7", USB: true},
			{Name: "Baiwang", Device: "en8", USB: true, Module: true},
		}
		name, active := selectModuleTrafficInterface(interfaces, services)
		if name != "en8" || active {
			t.Fatalf("selectModuleTrafficInterface() = %q, %v; want en8, false", name, active)
		}
	})

	t.Run("module unidentified", func(t *testing.T) {
		services := []networkService{
			{Name: "DL-Dock", Device: "en7", USB: true},
			{Name: "Baiwang", Device: "en8", USB: true},
		}
		name, active := selectModuleTrafficInterface(interfaces, services)
		if name != "" || active {
			t.Fatalf("selectModuleTrafficInterface() = %q, %v; want empty, false", name, active)
		}
	})
}
