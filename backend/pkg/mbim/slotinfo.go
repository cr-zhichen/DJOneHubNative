package mbim

import (
	"context"
	"fmt"
)

type SlotInfoStatus struct {
	SlotIndex uint32
	State     uint32
}

const (
	UICCSlotStateUnknown              uint32 = 0
	UICCSlotStateOffEmpty             uint32 = 1
	UICCSlotStateOff                  uint32 = 2
	UICCSlotStateEmpty                uint32 = 3
	UICCSlotStateNotReady             uint32 = 4
	UICCSlotStateActive               uint32 = 5
	UICCSlotStateActiveEsim           uint32 = 6
	UICCSlotStateActiveEsimNoProfiles uint32 = 7
	UICCSlotStateIoError              uint32 = 8
)

const slotInfoStatusLen = 8

func parseSlotInfoStatus(info []byte) (SlotInfoStatus, error) {
	if len(info) < slotInfoStatusLen {
		return SlotInfoStatus{}, fmt.Errorf("mbim: slot info status too short len=%d", len(info))
	}
	r := newInfoReader(info)
	var s SlotInfoStatus
	s.SlotIndex, _ = r.u32At(0)
	s.State, _ = r.u32At(4)
	return s, nil
}

func encodeSlotInfoStatusQuery(slotIndex uint32) []byte {
	b := make([]byte, 4)
	le.PutUint32(b, slotIndex)
	return b
}

func QuerySlotInfoStatus(ctx context.Context, d *Device, slotIndex uint32) (SlotInfoStatus, error) {
	resp, err := d.Command(ctx, UUIDMSBasicConnectExtensions, CIDMSBasicConnectExtSlotInfoStatus, CommandTypeQuery, encodeSlotInfoStatusQuery(slotIndex))
	if err != nil {
		return SlotInfoStatus{}, err
	}
	if resp.Status != 0 {
		return SlotInfoStatus{}, fmt.Errorf("mbim: SLOT_INFO_STATUS status=%d", resp.Status)
	}
	return parseSlotInfoStatus(resp.InfoBuffer)
}
