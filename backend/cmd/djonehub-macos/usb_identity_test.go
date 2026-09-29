package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func usbInterfaceFixture(vendor, product, location, iface int) string {
	return fmt.Sprintf(`+-o Module@%d <class IOUSBHostInterface>
  {
    "idVendor" = %d
    "idProduct" = %d
    "locationID" = %d
    "bInterfaceNumber" = %d
    "bInterfaceClass" = 255
    "bInterfaceSubClass" = 0
    "bInterfaceProtocol" = 0
    "bNumEndpoints" = 2
    "USBSpeed" = 3
  }

`, iface, vendor, product, location, iface)
}

func TestUSBDiscoverySupportedIdentities(t *testing.T) {
	for _, tc := range []struct {
		name            string
		vendor, product int
		want            bool
	}{
		{"DJI", 0x2ca3, 0x4006, true},
		{"Quectel", 0x2c7c, 0x0125, true},
		{"other DJI product", 0x2ca3, 0x0001, false},
		{"other Quectel product", 0x2c7c, 0x0126, false},
		{"mixed IDs", 0x2ca3, 0x0125, false},
		{"unrelated vendor", 0x1234, 0x0125, false},
		{"out of range", 0x12c7c, 0x0125, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := usbInterfaceFixture(tc.vendor, tc.product, 1, 3) +
				usbInterfaceFixture(tc.vendor, tc.product, 1, 2)
			got := parseDJIUSBDevice(output)
			if (got != nil) != tc.want {
				t.Fatalf("discovered = %#v, want supported = %v", got, tc.want)
			}
			if got == nil {
				return
			}
			if got.VendorID != fmt.Sprintf("%04x", tc.vendor) || got.ProductID != fmt.Sprintf("%04x", tc.product) {
				t.Fatalf("incorrect identity: %#v", got)
			}
			if len(got.Interfaces) != 2 || got.Interfaces[0].Number != 2 || got.Interfaces[1].Number != 3 {
				t.Fatalf("incorrect interfaces: %#v", got.Interfaces)
			}
			if got.Speed != "high-speed" || got.Mode != "vendor-specific QMI/diagnostic mode" {
				t.Fatalf("incorrect mode/speed: %#v", got)
			}
		})
	}
}

func TestUSBDiscoveryDoesNotMergeDevices(t *testing.T) {
	output := usbInterfaceFixture(0x2c7c, 0x0125, 1, 2) +
		usbInterfaceFixture(0x2ca3, 0x4006, 2, 3) +
		usbInterfaceFixture(0x2c7c, 0x0125, 3, 4)
	got := parseDJIUSBDevice(output)
	if got == nil || got.VendorID != "2c7c" || len(got.Interfaces) != 1 || got.Interfaces[0].Number != 2 {
		t.Fatalf("merged unrelated devices: %#v", got)
	}
}

func TestUSBDiscoveryReenumeration(t *testing.T) {
	// Exercise the same inventory refresh used by status/ensureUSBAT, including
	// clearing a previous successful discovery while the module re-enumerates.
	dir := t.TempDir()
	fixture := filepath.Join(dir, "inventory")
	if err := os.WriteFile(filepath.Join(dir, "ioreg"), []byte("#!/bin/sh\n/bin/cat \"$DJONEHUB_TEST_USB_INVENTORY\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DJONEHUB_TEST_USB_INVENTORY", fixture)
	application := &app{}
	for _, tc := range []struct{ output, identity string }{
		{usbInterfaceFixture(0x2ca3, 0x4006, 1, 2), "2ca3:4006"},
		{"", ""},
		{usbInterfaceFixture(0x2c7c, 0x0125, 1, 2), "2c7c:0125"},
		{"\"idVendor\" = 11388", ""},
	} {
		if err := os.WriteFile(fixture, []byte(tc.output), 0o600); err != nil {
			t.Fatal(err)
		}
		got := application.currentUSBDevice()
		identity := ""
		if got != nil {
			identity = got.VendorID + ":" + got.ProductID
		}
		if identity != tc.identity {
			t.Fatalf("identity = %q, want %q", identity, tc.identity)
		}
	}
}

func TestCallModePreservesQuectelIdentity(t *testing.T) {
	original, err := parseUSBComposition(`+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,1`)
	if err != nil {
		t.Fatal(err)
	}
	target, err := original.callModeTarget()
	if err != nil {
		t.Fatal(err)
	}
	if got := target.command(); got != `AT+QCFG="USBCFG",0x2C7C,0x0125,1,1,1,1,1,1,1` {
		t.Fatalf("command changed configured identity: %s", got)
	}
}
