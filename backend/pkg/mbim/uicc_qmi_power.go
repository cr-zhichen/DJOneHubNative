package mbim

import (
	"context"
	"fmt"
)

func buildQMIUIMPowerRequest(clientID uint8, msgID uint16, slot uint8) []byte {
	return buildQMIMessage(0x0B, clientID, 2, msgID, []byte{0x01, 0x01, 0x00, slot})
}

func parseQMIResultOnly(frame []byte) error {
	if len(frame) < 13 {
		return fmt.Errorf("qmi uim power: 响应过短 %d", len(frame))
	}
	tlvs := frame[13:]
	for idx := 0; idx+3 <= len(tlvs); {
		typ := tlvs[idx]
		l := int(le.Uint16(tlvs[idx+1 : idx+3]))
		if idx+3+l > len(tlvs) {
			break
		}
		if typ == 0x02 && l >= 4 {
			if le.Uint16(tlvs[idx+3:idx+5]) != 0 {
				return fmt.Errorf("qmi uim power: 请求被模组拒绝，qmi_error=0x%04X", le.Uint16(tlvs[idx+5:idx+7]))
			}
			return nil
		}
		idx += 3 + l
	}
	return fmt.Errorf("qmi uim power: 响应缺少 result TLV")
}

func (d *Device) UIMPowerOffSIM(ctx context.Context, slot uint8) error {
	clientID, err := d.allocUIMClient(ctx)
	if err != nil {
		return err
	}
	defer d.releaseUIMClient(context.Background(), clientID)
	resp, err := d.SendQMI(ctx, buildQMIUIMPowerRequest(clientID, 0x0030, slot))
	if err != nil {
		return fmt.Errorf("qmi power_off slot=%d: %w", slot, err)
	}
	return parseQMIResultOnly(resp)
}

func (d *Device) UIMPowerOnSIM(ctx context.Context, slot uint8) error {
	clientID, err := d.allocUIMClient(ctx)
	if err != nil {
		return err
	}
	defer d.releaseUIMClient(context.Background(), clientID)
	resp, err := d.SendQMI(ctx, buildQMIUIMPowerRequest(clientID, 0x0031, slot))
	if err != nil {
		return fmt.Errorf("qmi power_on slot=%d: %w", slot, err)
	}
	return parseQMIResultOnly(resp)
}

func (d *Device) QMIUIMGetCardStatus(ctx context.Context) ([]byte, error) {
	clientID, err := d.allocUIMClient(ctx)
	if err != nil {
		return nil, err
	}
	defer d.releaseUIMClient(context.Background(), clientID)
	resp, err := d.SendQMI(ctx, buildQMIMessage(0x0B, clientID, 3, 0x002F, nil))
	if err != nil {
		return nil, fmt.Errorf("qmi uim get_card_status failed: %w", err)
	}
	return resp, nil
}

func ParseActiveSlot(frame []byte) (slot uint8, known bool, source string, err error) {
	if err := parseQMIResultOnly(frame); err != nil {
		return 0, false, "", err
	}
	if len(frame) < 13 {
		return 0, false, "", fmt.Errorf("qmi card status: 响应过短 %d", len(frame))
	}
	tlvs := frame[13:]
	for idx := 0; idx+3 <= len(tlvs); {
		typ := tlvs[idx]
		l := int(le.Uint16(tlvs[idx+1 : idx+3]))
		if idx+3+l > len(tlvs) {
			break
		}
		if typ == 0x10 {
			val := tlvs[idx+3 : idx+3+l]
			if len(val) < 9 {
				return 0, false, "", fmt.Errorf("qmi card status tlv too short")
			}
			vIdx := 8
			numSlots := int(val[vIdx])
			vIdx++
			for s := 0; s < numSlots; s++ {
				if vIdx+6 > len(val) {
					return 0, false, "", fmt.Errorf("qmi card status slot truncated")
				}
				cardState := val[vIdx]
				slotState := val[vIdx+1]
				logicalSlot := val[vIdx+4]
				vIdx += 5
				numApps := int(val[vIdx])
				vIdx++
				if cardState == 0x01 && slotState == 0x01 {
					if logicalSlot != 0 {
						return logicalSlot, true, "uim_slot_status", nil
					}
					return uint8(s + 1), true, "uim_slot_status_index", nil
				}
				for a := 0; a < numApps; a++ {
					if vIdx+7 > len(val) {
						return 0, false, "", fmt.Errorf("qmi card status app truncated")
					}
					aidLen := int(val[vIdx+6])
					vIdx += 7 + aidLen + 7
					if vIdx > len(val) {
						return 0, false, "", fmt.Errorf("qmi card status aid truncated")
					}
				}
			}
			return 0, false, "", nil
		}
		idx += 3 + l
	}
	return 0, false, "", fmt.Errorf("qmi card status tlv not found")
}
