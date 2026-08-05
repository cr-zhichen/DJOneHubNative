package mbim

import (
	"context"
	"fmt"
)

// MBIM/MBIMEx release numbers in BCD (an implied decimal point between bits 7
// and 8, e.g. 0x0100 = 1.0, 0x0200 = 2.0).
const (
	MBIMVersion1_0   uint16 = 0x0100
	MBIMExVersion1_0 uint16 = 0x0100
	MBIMExVersion2_0 uint16 = 0x0200
)

func encodeVersionInfo(mbimVer, mbimExVer uint16) []byte {
	info := make([]byte, 4)
	le.PutUint16(info[0:], mbimVer)
	le.PutUint16(info[2:], mbimExVer)
	return info
}

// NegotiateVersion exchanges MBIM/MBIMEx release numbers via MBIM_CID_VERSION
// and returns the versions the device announces. Sending the host's MBIMEx
// version is the trigger that makes a device emit V2 payloads (e.g. RSRP/SNR in
// SIGNAL_STATE); per the MBIMEx 2.0 spec this CID must be the first one sent
// after device init. Devices predating MBIMEx 2.0 may not implement it, in
// which case the caller should fall back to MBIMEx 1.0 behaviour.
func NegotiateVersion(ctx context.Context, d *Device, hostMBIM, hostMBIMEx uint16) (devMBIM, devMBIMEx uint16, err error) {
	resp, err := d.Command(ctx, UUIDMSBasicConnectExtensions, CIDMSBasicConnectExtVersion, CommandTypeQuery, encodeVersionInfo(hostMBIM, hostMBIMEx))
	if err != nil {
		return 0, 0, err
	}
	if resp.Status != 0 {
		return 0, 0, fmt.Errorf("mbim: CID_VERSION status=%d", resp.Status)
	}
	if len(resp.InfoBuffer) < 4 {
		return 0, 0, fmt.Errorf("mbim: CID_VERSION info too short len=%d", len(resp.InfoBuffer))
	}
	devMBIM = le.Uint16(resp.InfoBuffer[0:])
	devMBIMEx = le.Uint16(resp.InfoBuffer[2:])
	return devMBIM, devMBIMEx, nil
}
