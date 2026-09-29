package main

const (
	djiUSBVendorID      = 0x2ca3
	djiUSBProductID     = 0x4006
	quectelUSBVendorID  = 0x2c7c
	quectelUSBProductID = 0x0125
)

// Share the supported pairs between macOS discovery, AT and ADB. A module
// configured with the Quectel ID does not need its USB identity restored.
var usbDeviceIDs = [][2]uint16{
	{djiUSBVendorID, djiUSBProductID},
	{quectelUSBVendorID, quectelUSBProductID},
}

func supportedUSBDevice(vendorID, productID int) bool {
	for _, ids := range usbDeviceIDs {
		if vendorID == int(ids[0]) && productID == int(ids[1]) {
			return true
		}
	}
	return false
}
