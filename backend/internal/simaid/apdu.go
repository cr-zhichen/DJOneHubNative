package simaid

import "fmt"

func APDUStatus(rsp []byte) (byte, byte, bool) {
	if len(rsp) < 2 {
		return 0, 0, false
	}
	return rsp[len(rsp)-2], rsp[len(rsp)-1], true
}

func IsSuccess(sw1, sw2 byte) bool {
	_ = sw2
	return sw1 == 0x90 || sw1 == 0x62 || sw1 == 0x63
}

func IsSelectSuccess(sw1, sw2 byte) bool {
	_ = sw2
	return sw1 == 0x90 || sw1 == 0x61 || sw1 == 0x62 || sw1 == 0x63
}

func ExtractSuccessData(rsp []byte) ([]byte, error) {
	sw1, sw2, ok := APDUStatus(rsp)
	if !ok {
		return nil, fmt.Errorf("APDU 响应过短: %X", rsp)
	}
	if !IsSuccess(sw1, sw2) {
		return nil, fmt.Errorf("SW=%02X%02X", sw1, sw2)
	}
	return rsp[:len(rsp)-2], nil
}
