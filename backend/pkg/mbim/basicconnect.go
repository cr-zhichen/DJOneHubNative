package mbim

import (
	"context"
	"fmt"
)

// Caps is the parsed Basic Connect DEVICE_CAPS response subset.
type Caps struct {
	DeviceType    uint32
	CellularClass uint32
	DataClass     uint32
	SmsCaps       uint32
	DeviceID      string
	FirmwareInfo  string
	HardwareInfo  string
}

const (
	capsFixedLen       = 8*4 + 4*8
	capsPairCustomData = 32
	capsPairDeviceID   = 40
	capsPairFirmware   = 48
	capsPairHardware   = 56
)

// DeviceCaps queries Basic Connect DEVICE_CAPS and parses the response.
func DeviceCaps(ctx context.Context, d *Device) (Caps, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectDeviceCaps, CommandTypeQuery, nil)
	if err != nil {
		return Caps{}, err
	}
	if resp.Status != 0 {
		return Caps{}, fmt.Errorf("mbim: DEVICE_CAPS status=%d", resp.Status)
	}
	if len(resp.InfoBuffer) < capsFixedLen {
		return Caps{}, fmt.Errorf("mbim: DEVICE_CAPS info too short len=%d", len(resp.InfoBuffer))
	}

	r := newInfoReader(resp.InfoBuffer)
	var c Caps
	c.DeviceType, _ = r.u32At(0)
	c.CellularClass, _ = r.u32At(4)
	c.DataClass, _ = r.u32At(16)
	c.SmsCaps, _ = r.u32At(20)
	if c.DeviceID, err = r.stringAt(capsPairDeviceID); err != nil {
		return Caps{}, fmt.Errorf("mbim: parse DeviceId: %w", err)
	}
	c.FirmwareInfo, _ = r.stringAt(capsPairFirmware)
	c.HardwareInfo, _ = r.stringAt(capsPairHardware)
	return c, nil
}
