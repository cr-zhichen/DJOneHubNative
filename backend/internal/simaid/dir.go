package simaid

import "fmt"

func ReadDirectoryAIDs(transmit func([]byte) ([]byte, error)) ([][]byte, error) {
	if transmit == nil {
		return nil, fmt.Errorf("transmit 为空")
	}
	if err := selectFileWithTransmit(transmit, "MF", []byte{0x00, 0xA4, 0x00, 0x04, 0x02, 0x3F, 0x00}); err != nil {
		return nil, err
	}
	var efDIRSelectRsp []byte
	if err := selectFileWithTransmit(func(apdu []byte) ([]byte, error) {
		rsp, err := transmit(apdu)
		if err == nil {
			efDIRSelectRsp = append([]byte(nil), rsp...)
		}
		return rsp, err
	}, "EF_DIR", []byte{0x00, 0xA4, 0x00, 0x04, 0x02, 0x2F, 0x00}); err != nil {
		return nil, err
	}
	fcpData, err := ExtractSuccessData(efDIRSelectRsp)
	if err != nil {
		return nil, fmt.Errorf("选择 EF_DIR 失败: %w", err)
	}
	recordLen, recordCount := ParseLinearFixedMetaFromFCP(fcpData)
	if recordLen < 0 || recordLen > 0xFF {
		recordLen = 0
	}
	maxRecords := 32
	if recordCount > 0 && recordCount < maxRecords {
		maxRecords = recordCount
	}

	var aids [][]byte
	var lastErr error
	for record := 1; record <= maxRecords; record++ {
		readCmd := []byte{0x00, 0xB2, byte(record), 0x04, byte(recordLen)}
		rsp, err := transmit(readCmd)
		if err != nil {
			lastErr = err
			continue
		}
		sw1, sw2, ok := APDUStatus(rsp)
		if !ok {
			lastErr = fmt.Errorf("EF_DIR 记录 %d 响应过短: %X", record, rsp)
			continue
		}
		if sw1 == 0x6A && (sw2 == 0x83 || sw2 == 0x82) {
			break
		}
		if !IsSuccess(sw1, sw2) {
			lastErr = fmt.Errorf("读取 EF_DIR 记录 %d 失败: SW=%02X%02X", record, sw1, sw2)
			continue
		}
		aids = AppendUniqueAIDs(aids, CollectTLVValues(rsp[:len(rsp)-2], 0x4F)...)
	}
	if len(aids) == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("EF_DIR 未发现 AID: %w", lastErr)
		}
		return nil, fmt.Errorf("EF_DIR 未发现 AID")
	}
	return aids, nil
}

func selectFileWithTransmit(transmit func([]byte) ([]byte, error), name string, apdu []byte) error {
	rsp, err := transmit(apdu)
	if err != nil {
		return fmt.Errorf("选择 %s 失败: %w", name, err)
	}
	sw1, sw2, ok := APDUStatus(rsp)
	if !ok {
		return fmt.Errorf("选择 %s 失败: APDU 响应过短: %X", name, rsp)
	}
	if !IsSelectSuccess(sw1, sw2) {
		return fmt.Errorf("选择 %s 失败: SW=%02X%02X", name, sw1, sw2)
	}
	return nil
}

func ParseLinearFixedMetaFromFCP(fcp []byte) (recordLen int, recordCount int) {
	if len(fcp) < 2 {
		return 0, 0
	}
	data := fcp
	if fcp[0] == 0x62 {
		total := int(fcp[1])
		if total > len(fcp)-2 {
			total = len(fcp) - 2
		}
		data = fcp[2 : 2+total]
	}

	for i := 0; i < len(data); {
		if i+2 > len(data) {
			break
		}
		tag := data[i]
		i++
		l := int(data[i])
		i++
		if l&0x80 != 0 {
			n := l & 0x7F
			if n <= 0 || i+n > len(data) {
				break
			}
			l = 0
			for j := 0; j < n; j++ {
				l = (l << 8) | int(data[i+j])
			}
			i += n
		}
		if i+l > len(data) {
			break
		}
		v := data[i : i+l]
		i += l

		if tag == 0x82 && len(v) >= 5 {
			recordLen = (int(v[len(v)-3]) << 8) | int(v[len(v)-2])
			recordCount = int(v[len(v)-1])
			if recordLen > 0 {
				return recordLen, recordCount
			}
		}
	}
	return recordLen, recordCount
}
