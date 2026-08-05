package mbim

import (
	"context"
	"encoding/binary"
	"fmt"
)

// UICCAppInfo 包含了从卡槽中读出的独立应用的信息
type UICCAppInfo struct {
	Type uint8 // QMI 定义: 1=SIM, 2=USIM, 3=RUIM, 4=CSIM, 5=ISIM, 6=Unknown
	AID  []byte
}

// QMIUIMApplicationList 封装了一套极致轻量的、无需完整状态机的 QMI over MBIM 隧道逻辑。
// 它通过向 CTL 申请 UIM client ID，然后发送 UIM_GET_CARD_STATUS，最后按照标准的 QMI 报文结构
// 解析出所有卡槽中注册的全部应用(USIM, ISIM, CSIM等)的信息(含类型和长AID)。
func (d *Device) QMIUIMApplicationList(ctx context.Context) ([]UICCAppInfo, error) {
	clientID, err := d.allocUIMClient(ctx)
	if err != nil {
		return nil, err
	}
	defer d.releaseUIMClient(context.Background(), clientID)

	// 2. 发送 UIM_GET_CARD_STATUS (Service=0x0B, MsgId=0x002F)
	// 这是一个无参数的查询，直接给一个空 TLV 即可
	uimReq := buildQMIMessage(0x0B, clientID, 3, 0x002F, nil)
	uimResp, err := d.SendQMI(ctx, uimReq)
	if err != nil {
		return nil, fmt.Errorf("qmi uim get_card_status failed: %w", err)
	}

	// 3. 从庞大的 Card Status TLV (0x10) 中按照规范解析出所有的长 AID
	return parseAllAIDs(uimResp)
}

func (d *Device) allocUIMClient(ctx context.Context) (uint8, error) {
	ctlReq := buildQMIMessage(0x00, 0x00, 1, 0x0022, []byte{0x01, 0x01, 0x00, 0x0B})
	ctlResp, err := d.SendQMI(ctx, ctlReq)
	if err != nil {
		return 0, fmt.Errorf("qmi ctl allocate failed: %w", err)
	}
	clientID, err := extractClientID(ctlResp)
	if err != nil {
		return 0, fmt.Errorf("parse ctl client id: %w", err)
	}
	return clientID, nil
}

func (d *Device) releaseUIMClient(ctx context.Context, clientID uint8) {
	relReq := buildQMIMessage(0x00, 0x00, 2, 0x0023, []byte{0x01, 0x02, 0x00, 0x0B, clientID})
	_, _ = d.SendQMI(ctx, relReq)
}

// SendQMI 专门用于通过 QMI over MBIM 隧道发送底层的 QMUX 报文，并返回响应的 QMUX 报文。
// 此方法要求外部已经完全构建好 QMI 的报文头(包含 IFType 等)，它将其作为透明负载下发。
func (d *Device) SendQMI(ctx context.Context, payload []byte) ([]byte, error) {
	// QMI over MBIM 固定使用 CommandTypeSet
	res, err := d.Command(ctx, UUIDQMI, CIDQMIMsg, CommandTypeSet, payload)
	if err != nil {
		return nil, err
	}
	return res.InfoBuffer, nil
}

// buildQMIMessage 将参数组装为一个原生的、带 QMUX 头和 SDU 头的完整二进制 QMI 帧。
func buildQMIMessage(service, clientID uint8, txID uint16, msgID uint16, tlvs []byte) []byte {
	isCTL := service == 0x00
	var sduLen int
	if isCTL {
		sduLen = 6 + len(tlvs) // CTL: Flags(1) + TxId(1) + MsgId(2) + TlvLen(2)
	} else {
		sduLen = 7 + len(tlvs) // SVC: Flags(1) + TxId(2) + MsgId(2) + TlvLen(2)
	}

	qmuxLen := 5 + sduLen // Length=1(Flags)+1(Service)+1(ClientID)+SDU
	buf := make([]byte, 1+qmuxLen)

	// QMUX Header
	buf[0] = 0x01 // IFType
	binary.LittleEndian.PutUint16(buf[1:3], uint16(qmuxLen))
	buf[3] = 0x00 // CtrlFlags
	buf[4] = service
	buf[5] = clientID

	offset := 6
	buf[offset] = 0x00 // SDU CtrlFlags (Request)
	offset++
	if isCTL {
		buf[offset] = uint8(txID)
		offset++
	} else {
		binary.LittleEndian.PutUint16(buf[offset:offset+2], txID)
		offset += 2
	}
	binary.LittleEndian.PutUint16(buf[offset:offset+2], msgID)
	offset += 2
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(tlvs)))
	offset += 2

	if len(tlvs) > 0 {
		copy(buf[offset:], tlvs)
	}
	return buf
}

// extractClientID 从 CTL Response 中解出分配到的 Client ID
func extractClientID(data []byte) (uint8, error) {
	if len(data) < 12 {
		return 0, fmt.Errorf("ctl resp too short")
	}
	tlvs := data[12:]
	idx := 0
	for idx+3 <= len(tlvs) {
		typ := tlvs[idx]
		l := binary.LittleEndian.Uint16(tlvs[idx+1 : idx+3])
		if idx+3+int(l) > len(tlvs) {
			break
		}
		val := tlvs[idx+3 : idx+3+int(l)]

		// TLV 0x01 holds Service and ClientID
		if typ == 0x01 && len(val) >= 2 {
			return val[1], nil
		}
		idx += 3 + int(l)
	}
	return 0, fmt.Errorf("client id tlv not found")
}

// parseAllAIDs 按照 QMI UIM GET_CARD_STATUS 规范解析 Card Status TLV (0x10)。
// 结构：[8 bytes idx] + num_slots
// 每 slot: [5 bytes slot_info] + num_apps
// 每 app: [6 bytes app_info] + aid_len + aid_value + [7 bytes pin_info]
func parseAllAIDs(data []byte) ([]UICCAppInfo, error) {
	if len(data) < 13 {
		return nil, fmt.Errorf("uim resp too short")
	}
	tlvs := data[13:]
	idx := 0
	var apps []UICCAppInfo

	for idx+3 <= len(tlvs) {
		typ := tlvs[idx]
		l := binary.LittleEndian.Uint16(tlvs[idx+1 : idx+3])
		if idx+3+int(l) > len(tlvs) {
			break
		}
		val := tlvs[idx+3 : idx+3+int(l)]

		if typ == 0x10 {
			// 开始线性解析 Card Status
			vIdx := 0
			if vIdx+8 > len(val) {
				goto nextTlv
			}
			vIdx += 8 // skip gw/1x primary/secondary indexes

			if vIdx >= len(val) {
				goto nextTlv
			}
			numSlots := int(val[vIdx])
			vIdx++

			for s := 0; s < numSlots; s++ {
				if vIdx+5 > len(val) {
					goto nextTlv
				}
				vIdx += 5 // skip card_state, upin_state/retries, upuk_retries, error_code

				if vIdx >= len(val) {
					goto nextTlv
				}
				numApps := int(val[vIdx])
				vIdx++

				for a := 0; a < numApps; a++ {
					if vIdx+6 > len(val) {
						goto nextTlv
					}
					appType := val[vIdx]
					vIdx += 6 // skip app_type/state, pers_state/feature/retries/unblock_retries

					if vIdx >= len(val) {
						goto nextTlv
					}
					aidLen := int(val[vIdx])
					vIdx++

					if vIdx+aidLen > len(val) {
						goto nextTlv
					}
					aid := make([]byte, aidLen)
					copy(aid, val[vIdx:vIdx+aidLen])
					apps = append(apps, UICCAppInfo{Type: appType, AID: aid})
					vIdx += aidLen

					if vIdx+7 > len(val) {
						goto nextTlv
					}
					vIdx += 7 // skip univ_pin, pin1_state/retries, puk1_retries, pin2...
				}
			}
			return apps, nil
		}
	nextTlv:
		idx += 3 + int(l)
	}
	return apps, nil // 就算没找到 TLV，或者 TLV 为空，也返回当前收集到的 apps
}
