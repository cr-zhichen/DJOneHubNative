package mbim

import (
	"context"
	"fmt"
)

// Well-known MBIM_STATUS values for the Microsoft-defined UICC Low Level
// Access service. Source: MBIM 1.0 Microsoft extension (also documented in
// libmbim's mbim-errors.h). These surface when an eUICC's internal RESET
// (typically triggered by ES10x EnableProfile/DisableProfile with refresh=true)
// invalidates a previously opened logical channel — the MBIM analogue of QMI's
// QMI_ERR_CARD_CALL_CONTROL_REF_FAILED (0x0030).
const (
	StatusMSNoLogicalChannels     uint32 = 0x87430001
	StatusMSSelectFailed          uint32 = 0x87430002
	StatusMSInvalidLogicalChannel uint32 = 0x87430003
)

// StatusError wraps a non-zero MBIM_STATUS so callers can use errors.As to
// inspect the specific status code instead of parsing the error string.
type StatusError struct {
	Op     string
	Status uint32
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("mbim: %s status=0x%x", e.Op, e.Status)
}

func encodeUICCOpenChannel(aid []byte, selectP2Arg, channelGroup uint32) []byte {
	const fixed = 16
	info := make([]byte, fixed+pad4(len(aid)))
	le.PutUint32(info[0:], uint32(len(aid)))
	// libmbim(_mbim_struct_builder_append_byte_array, swapped_offset_length=true)
	// 写 offset=0(不是 fixed)当 buffer 为空——空 AID 时不指向任何变长区数据。
	if len(aid) > 0 {
		le.PutUint32(info[4:], fixed)
	}
	le.PutUint32(info[8:], selectP2Arg)
	le.PutUint32(info[12:], channelGroup)
	copy(info[fixed:], aid)
	return info
}

func encodeUICCAPDU(channel uint32, command []byte) []byte {
	const fixed = 20
	info := make([]byte, fixed+pad4(len(command)))
	le.PutUint32(info[0:], channel)
	le.PutUint32(info[4:], 0)
	le.PutUint32(info[8:], 0)
	le.PutUint32(info[12:], uint32(len(command)))
	le.PutUint32(info[16:], fixed)
	copy(info[fixed:], command)
	return info
}

// UICCOpenChannel opens a logical channel to the given AID.
func UICCOpenChannel(ctx context.Context, d *Device, aid []byte) (uint32, error) {
	resp, err := d.Command(ctx, UUIDMSUICCLowLevelAccess, CIDUICCOpenChannel, CommandTypeSet, encodeUICCOpenChannel(aid, 0, 0))
	if err != nil {
		return 0, err
	}
	if resp.Status != 0 {
		return 0, &StatusError{Op: "UICC_OPEN_CHANNEL", Status: resp.Status}
	}
	if len(resp.InfoBuffer) < 8 {
		return 0, fmt.Errorf("mbim: UICC_OPEN_CHANNEL response too short len=%d", len(resp.InfoBuffer))
	}
	r := newInfoReader(resp.InfoBuffer)
	uiccStatus, _ := r.u32At(0)
	channel, _ := r.u32At(4)
	// status 是卡对 SELECT 的状态字:新固件返回 SW1(如 0x90=成功),旧固件返回 0。
	// 只在确属错误状态时失败,0x90/0x91/0x61/0x62/0x63 等成功/告警都接受,与 mbimcli 一致。
	if !uiccOpenChannelStatusOK(uiccStatus) {
		return 0, fmt.Errorf("mbim: UICC open channel status=0x%x", uiccStatus)
	}
	return channel, nil
}

// uiccOpenChannelStatusOK 判定 OPEN_CHANNEL 的 status(SELECT 的 SW)是否表示成功。
// 兼容两种上报形态:仅 SW1(0x90)与完整 SW1SW2(0x9000)。
func uiccOpenChannelStatusOK(status uint32) bool {
	if status == 0 { // 旧固件:0 表示成功
		return true
	}
	sw1 := status
	if status > 0xFF { // 完整 SW1SW2 → 取高字节 SW1
		sw1 = (status >> 8) & 0xFF
	}
	switch sw1 {
	case 0x90, 0x91, // 正常成功
		0x61,       // 还有更多数据
		0x62, 0x63: // 告警,命令已完成
		return true
	}
	return false
}

// UICCAPDU transmits an APDU on a channel and returns response bytes.
func UICCAPDU(ctx context.Context, d *Device, channel uint32, command []byte) ([]byte, error) {
	resp, err := d.Command(ctx, UUIDMSUICCLowLevelAccess, CIDUICCAPDU, CommandTypeSet, encodeUICCAPDU(channel, command))
	if err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, &StatusError{Op: "UICC_APDU", Status: resp.Status}
	}
	if len(resp.InfoBuffer) < 4 {
		return nil, fmt.Errorf("mbim: UICC_APDU response too short len=%d", len(resp.InfoBuffer))
	}
	r := newInfoReader(resp.InfoBuffer)
	status, _ := r.u32At(0)
	data, err := r.uiccByteArrayAt(4)
	if err != nil {
		return nil, err
	}
	// MBIM 把卡的 SW 放在 Status 字段、Response 只含数据;LPA 需要完整 R-APDU(数据+SW),
	// 故把 SW 追加回去。status==0 视为 SW 已内嵌在数据里(兼容把 SW 放进 Response 的模组),
	// 不追加。
	if status != 0 {
		data = append(data, uiccStatusWordBytes(status)...)
	}
	return data, nil
}

// uiccStatusWordBytes 把 MBIM Status 字段还原成 [SW1, SW2] 两字节,兼容两种上报形态:
// 仅 SW1(如 0x90)与完整 SW1SW2(如 0x9000)。
func uiccStatusWordBytes(status uint32) []byte {
	if status <= 0xFF { // 仅 SW1,SW2 隐含为 0x00
		return []byte{byte(status), 0x00}
	}
	return []byte{byte((status >> 8) & 0xFF), byte(status & 0xFF)}
}

// UICCCloseChannel closes a logical channel.
func UICCCloseChannel(ctx context.Context, d *Device, channel uint32) error {
	info := make([]byte, 8)
	le.PutUint32(info[0:], channel)
	le.PutUint32(info[4:], 0)
	resp, err := d.Command(ctx, UUIDMSUICCLowLevelAccess, CIDUICCCloseChannel, CommandTypeSet, info)
	if err != nil {
		return err
	}
	if resp.Status != 0 {
		return &StatusError{Op: "UICC_CLOSE_CHANNEL", Status: resp.Status}
	}
	return nil
}
