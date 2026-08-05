package simaid

import "fmt"

func SelectAIDWithTransmit(aids [][]byte, match func([]byte) bool, transmit func([]byte) ([]byte, error)) ([]byte, bool, error) {
	if transmit == nil {
		return nil, false, fmt.Errorf("transmit 为空")
	}
	var lastErr error
	for _, aid := range aids {
		if match != nil && !match(aid) {
			continue
		}
		if len(aid) == 0 || len(aid) > 0xFF {
			continue
		}
		for _, p2 := range []byte{0x04, 0x00} {
			apdu := append([]byte{0x00, 0xA4, 0x04, p2, byte(len(aid))}, aid...)
			rsp, err := transmit(apdu)
			if err != nil {
				lastErr = err
				continue
			}
			sw1, sw2, ok := APDUStatus(rsp)
			if !ok {
				lastErr = fmt.Errorf("APDU 响应过短: %X", rsp)
				continue
			}
			if IsSelectSuccess(sw1, sw2) {
				return append([]byte(nil), aid...), true, nil
			}
			lastErr = fmt.Errorf("SW=%02X%02X", sw1, sw2)
		}
	}
	if lastErr != nil {
		return nil, false, nil
	}
	return nil, false, nil
}
