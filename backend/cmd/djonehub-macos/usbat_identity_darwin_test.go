//go:build darwin && cgo

package main

import "testing"

func TestUSBATDescriptionUsesOpenedIdentity(t *testing.T) {
	dev := &usbAT{vendorID: 0x2c7c, productID: 0x0125, iface: 2, endpointOut: 3, endpointIn: 0x83}
	if got := dev.Description(); got != "USB AT · 2c7c:0125 interface 2 out 0x03 in 0x83" {
		t.Fatalf("description = %q", got)
	}
}
