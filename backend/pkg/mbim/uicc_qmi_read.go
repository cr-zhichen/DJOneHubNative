package mbim

import (
	"context"
	"fmt"
)

// qmiSessionTLV 构造 QMI UIM Session Information TLV(0x01)：
// session_type + aid_len(1) + aid。
//
// session_type 的选择规则（真机验证，EM7430 QMI-over-MBIM 隧道）：
//   - aid 为 nil/空 → session_type=0x00 (Primary GW Provisioning)：
//     用于 MF 级别文件（如 EF_DIR），此时必须在 file TLV 里提供从 MF 到父级的路径。
//   - aid 非空 → session_type=0x04 (Non-provisioning on slot 1)：
//     用于 ADF 子文件（如 ADF_USIM 下的 EF_SPN/EF_AD 等）；
//     session_type=0x00 在 QMI-over-MBIM 隧道里不被 EM7430 支持——无论 AID
//     是否提供，都会被以 qmi_error=0x0030 (INVALID_ARGUMENT) 拒绝。
func qmiSessionTLV(aid []byte) []byte {
	sessionType := byte(0x00) // Primary GW Provisioning（MF 级文件，path 指明位置）
	if len(aid) > 0 {
		sessionType = 0x04 // Non-provisioning on slot 1（ADF 子文件，AID 指明应用）
	}
	val := append([]byte{sessionType, byte(len(aid))}, aid...)
	tlv := []byte{0x01, byte(len(val)), byte(len(val) >> 8)}
	return append(tlv, val...)
}

func qmiFileTLV(fileID uint16, path []byte) []byte {
	val := []byte{byte(fileID), byte(fileID >> 8), byte(len(path))}
	val = append(val, path...)
	tlv := []byte{0x02, byte(len(val)), byte(len(val) >> 8)}
	return append(tlv, val...)
}

func qmiOffsetLenTLV(a, b uint16) []byte {
	return []byte{0x03, 0x04, 0x00, byte(a), byte(a >> 8), byte(b), byte(b >> 8)}
}

func buildQMIReadTransparent(clientID uint8, txID uint16, fileID uint16, aid, path []byte, offset, length uint16) []byte {
	tlvs := append(qmiSessionTLV(aid), qmiFileTLV(fileID, path)...)
	tlvs = append(tlvs, qmiOffsetLenTLV(offset, length)...)
	return buildQMIMessage(0x0B, clientID, txID, 0x0020, tlvs)
}

func buildQMIReadRecord(clientID uint8, txID uint16, fileID uint16, aid, path []byte, record, length uint16) []byte {
	tlvs := append(qmiSessionTLV(aid), qmiFileTLV(fileID, path)...)
	tlvs = append(tlvs, qmiOffsetLenTLV(record, length)...)
	return buildQMIMessage(0x0B, clientID, txID, 0x0021, tlvs)
}

func parseQMIReadResult(frame []byte) (data []byte, sw1, sw2 byte, err error) {
	if len(frame) < 13 {
		return nil, 0, 0, fmt.Errorf("qmi read: 响应过短 %d", len(frame))
	}
	tlvs := frame[13:]
	have := false
	qmiFailed := false
	var qmiErrorCode uint16
	for idx := 0; idx+3 <= len(tlvs); {
		typ := tlvs[idx]
		l := int(le.Uint16(tlvs[idx+1 : idx+3]))
		if idx+3+l > len(tlvs) {
			break
		}
		val := tlvs[idx+3 : idx+3+l]
		switch typ {
		case 0x02:
			// QMI 标准强制 Result Code TLV：result(2,LE) + error(2,LE)。
			// result!=0 时模组只会回这个 TLV，不会带 0x10/0x11。
			if len(val) >= 4 && le.Uint16(val[0:2]) != 0 {
				qmiFailed = true
				qmiErrorCode = le.Uint16(val[2:4])
			}
		case 0x10:
			if len(val) >= 2 {
				sw1, sw2, have = val[0], val[1], true
			}
		case 0x11:
			if len(val) >= 2 {
				n := int(le.Uint16(val[0:2]))
				if 2+n <= len(val) {
					data = val[2 : 2+n]
				}
			}
		}
		idx += 3 + l
	}
	if qmiFailed {
		return nil, 0, 0, fmt.Errorf("qmi read: 请求被模组拒绝，qmi_error=0x%04X", qmiErrorCode)
	}
	if !have && data == nil {
		return nil, 0, 0, fmt.Errorf("qmi read: 响应缺少 card_result/read_result TLV")
	}
	return data, sw1, sw2, nil
}

// QMIReadTransparentEF 读取一个透明 EF。aid 应是目标文件所属应用(如 ADF_USIM)
// 的完整 AID；文件直接挂在 MF 下(如 EF_DIR)时传 nil。
func (d *Device) QMIReadTransparentEF(ctx context.Context, fileID uint16, aid, path []byte, offset, length uint16) ([]byte, byte, byte, error) {
	clientID, err := d.allocUIMClient(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	defer d.releaseUIMClient(context.Background(), clientID)
	resp, err := d.SendQMI(ctx, buildQMIReadTransparent(clientID, 2, fileID, aid, path, offset, length))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("qmi read_transparent EF %04X: %w", fileID, err)
	}
	return parseQMIReadResult(resp)
}

// QMIReadRecordEF 读取一条线性记录 EF。aid 含义同 QMIReadTransparentEF。
func (d *Device) QMIReadRecordEF(ctx context.Context, fileID uint16, aid, path []byte, record, length uint16) ([]byte, byte, byte, error) {
	clientID, err := d.allocUIMClient(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	defer d.releaseUIMClient(context.Background(), clientID)
	resp, err := d.SendQMI(ctx, buildQMIReadRecord(clientID, 2, fileID, aid, path, record, length))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("qmi read_record EF %04X rec %d: %w", fileID, record, err)
	}
	return parseQMIReadResult(resp)
}
