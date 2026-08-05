package mbim

import (
	"context"
	"fmt"
)

// DeviceReset issues MS Basic Connect Extensions DEVICE_RESET (CID 10), the
// standardized MBIM soft reboot of the modem. The command carries an empty
// payload and the modem typically tears down the MBIM session as it resets, so
// callers should treat a successful return as "reset requested".
func DeviceReset(ctx context.Context, d *Device) error {
	resp, err := d.Command(ctx, UUIDMSBasicConnectExtensions, CIDMSBasicConnectExtDeviceReset, CommandTypeSet, nil)
	if err != nil {
		return err
	}
	if resp.Status != 0 {
		return fmt.Errorf("mbim: DEVICE_RESET status=%d", resp.Status)
	}
	return nil
}
