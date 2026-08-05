package mbim

import (
	"bytes"
	"unicode/utf16"
)

// NewFakeTransport returns an in-memory Transport that answers each written
// message via reply(written)->(response,send). It is intended for cross-package
// tests that need an opened *Device without hardware.
func NewFakeTransport(reply func(written []byte) ([]byte, bool)) Transport {
	return &scriptedTransport{reply: reply, toRead: make(chan []byte, 8)}
}

type scriptedTransport struct {
	reply  func([]byte) ([]byte, bool)
	toRead chan []byte
}

type TestHeader struct {
	Type          MessageType
	TransactionID uint32
}

func (s *scriptedTransport) WriteMessage(b []byte) error {
	cp := append([]byte(nil), b...)
	if s.reply != nil {
		if out, ok := s.reply(cp); ok {
			s.toRead <- out
			return nil
		}
	}
	if out, ok := defaultDeviceServicesAnswer(cp); ok {
		s.toRead <- out
		return nil
	}
	// MBIMEx 版本协商(CID_VERSION)是设备初始化握手的一部分,会在 OPEN 之后发出。
	// 仅脚本化特定 CID 的 reply 不会应答它;这里默认以"支持 MBIMEx 2.0"回应,
	// 使所有 fake 都能快速通过初始化,而不必每个 reply 都显式处理该 CID。
	if out, ok := defaultVersionAnswer(cp); ok {
		s.toRead <- out
	}
	return nil
}

func defaultVersionAnswer(written []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil || h.Type != MessageTypeCommand || len(written) < 40 {
		return nil, false
	}
	svc := UUID{}
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	if !svc.Equal(UUIDMSBasicConnectExtensions) || cid != CIDMSBasicConnectExtVersion {
		return nil, false
	}
	info := make([]byte, 4)
	le.PutUint16(info[0:], MBIMVersion1_0)
	le.PutUint16(info[2:], MBIMExVersion2_0)
	return buildCommandDone(h.TransactionID, svc, cid, info), true
}

func defaultDeviceServicesAnswer(written []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil || h.Type != MessageTypeCommand || len(written) < 40 {
		return nil, false
	}
	svc := UUID{}
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	if !svc.Equal(UUIDBasicConnect) || cid != CIDBasicConnectDeviceServices {
		return nil, false
	}

	type elemDef struct {
		svc  UUID
		cids []uint32
	}
	elems := []elemDef{
		{UUIDBasicConnect, []uint32{1, 9, 11, 16}},
		{UUIDMSBasicConnectExtensions, []uint32{CIDMSBasicConnectExtVersion}},
	}
	const headFixed = 8
	refList := make([]byte, len(elems)*8)
	var data []byte
	dataStart := headFixed + len(refList)
	for i, e := range elems {
		elem := make([]byte, 28+len(e.cids)*4)
		copy(elem[0:16], e.svc[:])
		le.PutUint32(elem[24:], uint32(len(e.cids)))
		for j, c := range e.cids {
			le.PutUint32(elem[28+j*4:], c)
		}
		off := uint32(dataStart + len(data))
		le.PutUint32(refList[i*8:], off)
		le.PutUint32(refList[i*8+4:], uint32(len(elem)))
		data = append(data, elem...)
	}
	info := make([]byte, dataStart+len(data))
	le.PutUint32(info[0:], uint32(len(elems)))
	le.PutUint32(info[4:], 0)
	copy(info[headFixed:], refList)
	copy(info[dataStart:], data)
	return buildCommandDone(h.TransactionID, svc, cid, info), true
}

func TestDeviceServicesInfo(elems []struct {
	Svc  UUID
	CIDs []uint32
}) []byte {
	const headFixed = 8
	refList := make([]byte, len(elems)*8)
	var data []byte
	dataStart := headFixed + len(refList)
	for i, e := range elems {
		elem := make([]byte, 28+len(e.CIDs)*4)
		copy(elem[0:16], e.Svc[:])
		le.PutUint32(elem[24:], uint32(len(e.CIDs)))
		for j, c := range e.CIDs {
			le.PutUint32(elem[28+j*4:], c)
		}
		off := uint32(dataStart + len(data))
		le.PutUint32(refList[i*8:], off)
		le.PutUint32(refList[i*8+4:], uint32(len(elem)))
		data = append(data, elem...)
	}
	info := make([]byte, dataStart+len(data))
	le.PutUint32(info[0:], uint32(len(elems)))
	le.PutUint32(info[4:], 0)
	copy(info[headFixed:], refList)
	copy(info[dataStart:], data)
	return info
}

func TestQMIAllocateUIMClientResp(clientID uint8) []byte {
	return buildQMIMessage(0x00, 0x00, 1, 0x0022, []byte{0x01, 0x02, 0x00, 0x0B, clientID})
}

func TestQMIRelClientResp() []byte {
	return buildQMIMessage(0x00, 0x00, 2, 0x0023, nil)
}

func TestQMIReadResp(msgID uint16, data []byte, sw1, sw2 byte) []byte {
	card := []byte{0x10, 0x02, 0x00, sw1, sw2}
	rr := []byte{0x11, byte(2 + len(data)), 0x00, byte(len(data)), 0x00}
	rr = append(rr, data...)
	return buildQMIMessage(0x0B, 0x01, 2, msgID, append(card, rr...))
}

// TestQMIReadErrorResp 构造一个仅带 QMI 标准 Result Code TLV(0x02，result=FAILURE)
// 而不带 card_result/read_result 的失败响应，用于验证调用方能正确解析出 qmi_error。
func TestQMIReadErrorResp(msgID uint16, qmiErrorCode uint16) []byte {
	result := []byte{0x02, 0x04, 0x00, 0x01, 0x00, byte(qmiErrorCode), byte(qmiErrorCode >> 8)}
	return buildQMIMessage(0x0B, 0x01, 2, msgID, result)
}

func TestQMIResultSuccessResp(service, clientID uint8, txID, msgID uint16, tlvs []byte) []byte {
	result := []byte{0x02, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00}
	return buildQMIMessage(service, clientID, txID, msgID, append(result, tlvs...))
}

func TestQMIUIMGetCardStatusResp(logicalSlot uint8, presentIndex int, active bool) []byte {
	slotState := byte(0x00)
	if active {
		slotState = 0x01
	}
	cardState := byte(0x00)
	if presentIndex >= 0 {
		cardState = 0x01
	}

	slotCount := byte(2)
	val := []byte{
		0, 0, 0, 0, 0, 0, 0, 0,
		slotCount,
	}
	for i := 0; i < int(slotCount); i++ {
		curCardState := byte(0x00)
		curSlotState := byte(0x00)
		curLogical := byte(0x00)
		if i == presentIndex {
			curCardState = cardState
			curSlotState = slotState
			curLogical = logicalSlot
		}
		val = append(val, curCardState, curSlotState, 0x00, 0x00, curLogical)
		val = append(val, 0x00)
	}
	tlv := append([]byte{0x10, byte(len(val)), byte(len(val) >> 8)}, val...)
	return TestQMIResultSuccessResp(0x0B, 0x21, 3, 0x002F, tlv)
}

func DecodeHeaderForTest(msg []byte) (TestHeader, error) {
	h, err := decodeHeader(msg)
	if err != nil {
		return TestHeader{}, err
	}
	return TestHeader{Type: h.Type, TransactionID: h.TransactionID}, nil
}

func BuildOpenDoneForTest(tx uint32) []byte {
	return buildOpenDone(tx)
}

func BuildCommandDoneForTest(tx uint32, service UUID, cid uint32, info []byte) []byte {
	return buildCommandDone(tx, service, cid, info)
}

func BuildCommandDoneStatusForTest(tx uint32, service UUID, cid uint32, status uint32, info []byte) []byte {
	return buildCommandDoneStatus(tx, service, cid, status, info)
}

func BuildTestSingleAppListInfoForTest(fullAID []byte) []byte {
	return buildTestSingleAppListInfo(fullAID)
}

func BuildUICCFileResponseInfoForTest(sw1, sw2 byte, data []byte) []byte {
	return buildUICCFileResponseInfo(sw1, sw2, data)
}

func ReadU32ForTest(b []byte) uint32 {
	return le.Uint32(b)
}

func ReadU16ForTest(b []byte) uint16 {
	return le.Uint16(b)
}

func (s *scriptedTransport) ReadMessage() ([]byte, error) { return <-s.toRead, nil }
func (s *scriptedTransport) Close() error                 { return nil }

// TestEmitIndication injects an INDICATE_STATUS message into a fake transport.
func TestEmitIndication(tr Transport, service UUID, cid uint32, info []byte) bool {
	s, ok := tr.(*scriptedTransport)
	if !ok {
		return false
	}
	s.toRead <- buildIndicateStatus(0, service, cid, info)
	return true
}

// TestAnswerDeviceCaps answers OPEN and a DEVICE_CAPS query with the given IMEI.
func TestAnswerDeviceCaps(written []byte, imei string) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	switch h.Type {
	case MessageTypeOpen:
		return buildOpenDone(h.TransactionID), true
	case MessageTypeCommand:
		info := buildDeviceCapsInfo(imei)
		return buildCommandDone(h.TransactionID, UUIDBasicConnect, CIDBasicConnectDeviceCaps, info), true
	}
	return nil, false
}

// TestAnswerOpenAndSubscribe answers OPEN and any COMMAND with success,
// reporting whether the command was DEVICE_SERVICE_SUBSCRIBE_LIST.
func TestAnswerOpenAndSubscribe(written []byte) (out []byte, send bool, isSubscribe bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false, false
	}
	switch h.Type {
	case MessageTypeOpen:
		return buildOpenDone(h.TransactionID), true, false
	case MessageTypeCommand:
		cid := le.Uint32(written[36:])
		svc := UUID{}
		copy(svc[:], written[20:36])
		return buildCommandDone(h.TransactionID, svc, cid, nil), true, cid == CIDBasicConnectDeviceServiceSubscribeList
	}
	return nil, false, false
}

// TestAnswerOpenSubscribeAndUSSDPending answers OPEN, SUBSCRIBE, and USSD
// initiate commands. USSD commands receive an empty successful command-done so
// tests can deliver the final response through an indication.
func TestAnswerOpenSubscribeAndUSSDPending(written []byte) (out []byte, send bool, isUSSD bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false, false
	}
	switch h.Type {
	case MessageTypeOpen:
		return buildOpenDone(h.TransactionID), true, false
	case MessageTypeCommand:
		cid := le.Uint32(written[36:])
		svc := UUID{}
		copy(svc[:], written[20:36])
		if svc.Equal(UUIDUSSD) && cid == CIDUSSD {
			info := TestUSSDResponseInfo(USSDRespActionRequired, 1, 0x0F, nil)
			return buildCommandDone(h.TransactionID, svc, cid, info), true, true
		}
		return buildCommandDone(h.TransactionID, svc, cid, nil), true, false
	}
	return nil, false, false
}

// TestAnswerOpenSubscribeAndUSSDTerminal answers a USSD command with an empty
// terminal command-done response.
func TestAnswerOpenSubscribeAndUSSDTerminal(written []byte, response uint32) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	switch h.Type {
	case MessageTypeOpen:
		return buildOpenDone(h.TransactionID), true
	case MessageTypeCommand:
		cid := le.Uint32(written[36:])
		svc := UUID{}
		copy(svc[:], written[20:36])
		if svc.Equal(UUIDUSSD) && cid == CIDUSSD {
			info := TestUSSDResponseInfo(response, 1, 0x0F, nil)
			return buildCommandDone(h.TransactionID, svc, cid, info), true
		}
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	}
	return nil, false
}

// TestUSSDResponseInfo builds an MBIM USSD response/indication info buffer.
func TestUSSDResponseInfo(response, sessionState, dcs uint32, payload []byte) []byte {
	const fixed = 20
	info := make([]byte, fixed+len(payload))
	le.PutUint32(info[0:], response)
	le.PutUint32(info[4:], sessionState)
	le.PutUint32(info[8:], dcs)
	le.PutUint32(info[12:], fixed)
	le.PutUint32(info[16:], uint32(len(payload)))
	copy(info[fixed:], payload)
	return info
}

// TestSubscriberReadyInfo encodes a SUBSCRIBER_READY_STATUS info buffer for
// tests, matching the layout parseSubscriberReady expects (no MSISDN).
func TestSubscriberReadyInfo(readyState uint32, imsi, iccid string) []byte {
	const fixed = 36
	bi, bc := encodeUTF16String(imsi), encodeUTF16String(iccid)
	buf := make([]byte, fixed+len(bi)+len(bc))
	le.PutUint32(buf[0:], readyState)
	off := fixed
	le.PutUint32(buf[4:], uint32(off))
	le.PutUint32(buf[8:], uint32(len(bi)))
	copy(buf[off:], bi)
	off += len(bi)
	le.PutUint32(buf[12:], uint32(off))
	le.PutUint32(buf[16:], uint32(len(bc)))
	copy(buf[off:], bc)
	le.PutUint32(buf[20:], 0)
	le.PutUint32(buf[24:], 0)
	return buf
}

// TestSlotInfoStatusInfo encodes a SLOT_INFO_STATUS info buffer for tests.
func TestSlotInfoStatusInfo(slotIndex, state uint32) []byte {
	b := make([]byte, 8)
	le.PutUint32(b[0:], slotIndex)
	le.PutUint32(b[4:], state)
	return b
}

// TestAnswerRegisterStateWithFailures answers OPEN/SUBSCRIBE normally. The
// first failCount REGISTER_STATE queries get no reply; subsequent queries
// succeed with RegisterState=3 (home).
func TestAnswerRegisterStateWithFailures(failCount int) func(written []byte) ([]byte, bool) {
	var queries int
	return func(written []byte) ([]byte, bool) {
		h, err := decodeHeader(written)
		if err != nil {
			return nil, false
		}
		if h.Type == MessageTypeOpen {
			return buildOpenDone(h.TransactionID), true
		}
		if h.Type != MessageTypeCommand {
			return nil, false
		}
		var svc UUID
		copy(svc[:], written[20:36])
		cid := le.Uint32(written[36:])
		ct := le.Uint32(written[40:])
		switch {
		case svc.Equal(UUIDBasicConnect) && cid == CIDBasicConnectDeviceServiceSubscribeList:
			return buildCommandDone(h.TransactionID, svc, cid, nil), true
		case svc.Equal(UUIDBasicConnect) && cid == CIDBasicConnectRegisterState && ct == uint32(CommandTypeQuery):
			queries++
			if queries <= failCount {
				return nil, false
			}
			info := make([]byte, registerFixedLen)
			le.PutUint32(info[4:], 3)
			return buildCommandDone(h.TransactionID, svc, cid, info), true
		default:
			return buildCommandDone(h.TransactionID, svc, cid, nil), true
		}
	}
}

func TestAnswerConnectAndIPv4Config(written []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	if h.Type == MessageTypeOpen {
		return buildOpenDone(h.TransactionID), true
	}
	if h.Type != MessageTypeCommand {
		return nil, false
	}
	var svc UUID
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	ct := le.Uint32(written[40:])
	switch {
	case svc.Equal(UUIDBasicConnect) && cid == CIDBasicConnectDeviceServiceSubscribeList:
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	case svc.Equal(UUIDBasicConnect) && cid == CIDBasicConnectRegisterState && ct == uint32(CommandTypeQuery):
		info := make([]byte, registerFixedLen)
		le.PutUint32(info[4:], 3)
		return buildCommandDone(h.TransactionID, svc, cid, info), true
	case svc.Equal(UUIDBasicConnect) && cid == CIDBasicConnectConnect && ct == uint32(CommandTypeSet):
		info := make([]byte, 36)
		le.PutUint32(info[4:], ActivationStateActivated)
		le.PutUint32(info[12:], ContextIPTypeIPv4)
		copy(info[16:32], UUIDContextTypeInternet[:])
		return buildCommandDone(h.TransactionID, svc, cid, info), true
	case svc.Equal(UUIDBasicConnect) && cid == CIDBasicConnectIPConfiguration && ct == uint32(CommandTypeQuery):
		const fixed = 60
		addrOff := fixed
		gwOff := addrOff + 8
		dnsOff := gwOff + 4
		info := make([]byte, dnsOff+4)
		le.PutUint32(info[4:], 0x0F)
		le.PutUint32(info[12:], 1)
		le.PutUint32(info[16:], uint32(addrOff))
		le.PutUint32(info[28:], uint32(gwOff))
		le.PutUint32(info[36:], 1)
		le.PutUint32(info[40:], uint32(dnsOff))
		le.PutUint32(info[52:], 1500)
		le.PutUint32(info[addrOff:], 24)
		copy(info[addrOff+4:], []byte{10, 0, 0, 5})
		copy(info[gwOff:], []byte{10, 0, 0, 1})
		copy(info[dnsOff:], []byte{8, 8, 8, 8})
		return buildCommandDone(h.TransactionID, svc, cid, info), true
	default:
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	}
}

func TestAnswerRegistrationSearching(written []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	if h.Type == MessageTypeOpen {
		return buildOpenDone(h.TransactionID), true
	}
	if h.Type != MessageTypeCommand {
		return nil, false
	}
	var svc UUID
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	ct := le.Uint32(written[40:])
	switch {
	case svc.Equal(UUIDBasicConnect) && cid == CIDBasicConnectDeviceServiceSubscribeList:
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	case svc.Equal(UUIDBasicConnect) && cid == CIDBasicConnectRegisterState && ct == uint32(CommandTypeQuery):
		info := make([]byte, registerFixedLen)
		le.PutUint32(info[4:], 2)
		return buildCommandDone(h.TransactionID, svc, cid, info), true
	default:
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	}
}

// TestAnswerUICC answers OPEN, SUBSCRIBE, and UICC open/apdu/close commands.
func TestAnswerUICC(written []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	if h.Type == MessageTypeOpen {
		return buildOpenDone(h.TransactionID), true
	}
	if h.Type != MessageTypeCommand {
		return nil, false
	}
	var svc UUID
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	switch {
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCOpenChannel:
		resp := make([]byte, 16)
		le.PutUint32(resp[4:], 1)
		return buildCommandDone(h.TransactionID, svc, cid, resp), true
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCAPDU:
		resp := make([]byte, 14)
		le.PutUint32(resp[4:], 2)
		le.PutUint32(resp[8:], 12)
		resp[12], resp[13] = 0x90, 0x00
		return buildCommandDone(h.TransactionID, svc, cid, resp), true
	default:
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	}
}

// TestAnswerUICCApplicationListAndReadBinary 答复 OPEN + APPLICATION_LIST(单个
// USIM 应用,完整 fullAID)+ READ_BINARY(CID 9,直读:校验 AID 与 fullAID 完全一致,
// 不一致返回 SW=6A82 File Not Found;一致则返回 efData)。OPEN_CHANNEL/UICC_APDU/
// CLOSE_CHANNEL 一律答复 status=0x9(NoDeviceSupport),用于证明 ReadSIMEF 已不再
// 开逻辑通道——这颗 EM7430 上开通道无论空 AID(status=0x15 InvalidParameters)还是
// 短 AID(status=0x87430002 SelectFailed)都必然失败,READ_BINARY/READ_RECORD 这两个
// 直读 CID 此前从未在真机上验证过(此前调用点全部排在一个必然失败的 AID 解析步骤
// 之后),现在改为唯一真正的读取路径。
func TestAnswerUICCApplicationListAndReadBinary(written []byte, fullAID []byte, efData []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	if h.Type == MessageTypeOpen {
		return buildOpenDone(h.TransactionID), true
	}
	if h.Type != MessageTypeCommand {
		return nil, false
	}
	var svc UUID
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	switch {
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCApplicationList:
		return buildCommandDone(h.TransactionID, svc, cid, buildTestSingleAppListInfo(fullAID)), true
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCReadBinary:
		aid, _, _, _ := uiccReadBinaryFromWritten(written)
		if !bytes.Equal(aid, fullAID) {
			return buildCommandDone(h.TransactionID, svc, cid, buildUICCFileResponseInfo(0x6A, 0x82, nil)), true
		}
		return buildCommandDone(h.TransactionID, svc, cid, buildUICCFileResponseInfo(0x90, 0x00, efData)), true
	case svc.Equal(UUIDMSUICCLowLevelAccess) && (cid == CIDUICCOpenChannel || cid == CIDUICCAPDU || cid == CIDUICCCloseChannel || cid == CIDUICCReadRecord):
		return buildCommandDoneStatus(h.TransactionID, svc, cid, 0x9, nil), true
	default:
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	}
}

// TestAnswerUICCEFDIRReadRecordThenReadBinary 答复 OPEN + APPLICATION_LIST 以
// status=0x9(模拟该 CID 未被固件实现)+ READ_RECORD(CID 10,AID 为空、绝对路径
// 3F00/2F00,模组内部完成选 MF→选 EF_DIR→读记录:记录 1 返回单条 TLV 包装 fullAID
// 的 EF_DIR 记录,记录 ≥2 返回 SW=6A83 表示无更多记录)+ READ_BINARY(CID 9,校验
// AID 与从 EF_DIR 解析出的 fullAID 一致,返回 efData)。OPEN_CHANNEL/UICC_APDU/
// CLOSE_CHANNEL 一律答复 status=0x9,证明整条路径不开逻辑通道。
func TestAnswerUICCEFDIRReadRecordThenReadBinary(written []byte, fullAID []byte, efData []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	if h.Type == MessageTypeOpen {
		return buildOpenDone(h.TransactionID), true
	}
	if h.Type != MessageTypeCommand {
		return nil, false
	}
	var svc UUID
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	switch {
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCApplicationList:
		return buildCommandDoneStatus(h.TransactionID, svc, cid, 0x9, nil), true
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCReadRecord:
		_, _, record := uiccReadRecordFromWritten(written)
		if record != 1 {
			return buildCommandDone(h.TransactionID, svc, cid, buildUICCFileResponseInfo(0x6A, 0x83, nil)), true
		}
		return buildCommandDone(h.TransactionID, svc, cid, buildUICCFileResponseInfo(0x90, 0x00, buildEFDirRecord(fullAID))), true
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCReadBinary:
		aid, _, _, _ := uiccReadBinaryFromWritten(written)
		if !bytes.Equal(aid, fullAID) {
			return buildCommandDone(h.TransactionID, svc, cid, buildUICCFileResponseInfo(0x6A, 0x82, nil)), true
		}
		return buildCommandDone(h.TransactionID, svc, cid, buildUICCFileResponseInfo(0x90, 0x00, efData)), true
	case svc.Equal(UUIDMSUICCLowLevelAccess) && (cid == CIDUICCOpenChannel || cid == CIDUICCAPDU || cid == CIDUICCCloseChannel):
		return buildCommandDoneStatus(h.TransactionID, svc, cid, 0x9, nil), true
	default:
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	}
}

// TestAnswerUICCNoAIDFound 答复 OPEN + APPLICATION_LIST 以 status=0x9 + READ_RECORD
// 记录 1 即返回 SW=6A83(EF_DIR 空,没有任何应用记录)。OPEN_CHANNEL/READ_BINARY/
// UICC_APDU/CLOSE_CHANNEL 一律答复 status=0x9,用于证明 AID 解析失败时 ReadSIMEF
// 直接报错,不会回退到(必然失败的)短 AID 开通道。
func TestAnswerUICCNoAIDFound(written []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	if h.Type == MessageTypeOpen {
		return buildOpenDone(h.TransactionID), true
	}
	if h.Type != MessageTypeCommand {
		return nil, false
	}
	var svc UUID
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	switch {
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCApplicationList:
		return buildCommandDoneStatus(h.TransactionID, svc, cid, 0x9, nil), true
	case svc.Equal(UUIDMSUICCLowLevelAccess) && cid == CIDUICCReadRecord:
		return buildCommandDone(h.TransactionID, svc, cid, buildUICCFileResponseInfo(0x6A, 0x83, nil)), true
	case svc.Equal(UUIDMSUICCLowLevelAccess) && (cid == CIDUICCOpenChannel || cid == CIDUICCAPDU || cid == CIDUICCCloseChannel || cid == CIDUICCReadBinary):
		return buildCommandDoneStatus(h.TransactionID, svc, cid, 0x9, nil), true
	default:
		return buildCommandDone(h.TransactionID, svc, cid, nil), true
	}
}

// buildUICCFileResponseInfo 按 parseUICCFileResponse 期望的布局(Version,SW1,SW2,
// Data ref(offset,size))编码 READ_BINARY/READ_RECORD 的应答 info buffer。
func buildUICCFileResponseInfo(sw1, sw2 byte, data []byte) []byte {
	const fixed = 20
	info := make([]byte, fixed+len(data))
	le.PutUint32(info[0:], 1) // Version
	le.PutUint32(info[4:], uint32(sw1))
	le.PutUint32(info[8:], uint32(sw2))
	le.PutUint32(info[12:], fixed)
	le.PutUint32(info[16:], uint32(len(data)))
	copy(info[fixed:], data)
	return info
}

// uiccReadBinaryFromWritten 按 encodeUICCReadBinary 的布局(固定 44 字节:Version,
// AppId ref(offset,size)@4/8,FilePath ref(offset,size)@12/16,ReadOffset@20,
// ReadSize@24)解析一条 UICC_READ_BINARY 命令的请求字段。
func uiccReadBinaryFromWritten(written []byte) (aid, path []byte, offset, size uint32) {
	if len(written) < 48+44 {
		return nil, nil, 0, 0
	}
	info := written[48:]
	aid = refByteArray(info, 4)
	path = refByteArray(info, 12)
	offset = le.Uint32(info[20:])
	size = le.Uint32(info[24:])
	return aid, path, offset, size
}

// uiccReadRecordFromWritten 按 encodeUICCReadRecord 的布局(固定 40 字节:Version,
// AppId ref(offset,size)@4/8,FilePath ref(offset,size)@12/16,RecordNumber@20)
// 解析一条 UICC_READ_RECORD 命令的请求字段。
func uiccReadRecordFromWritten(written []byte) (aid, path []byte, record uint32) {
	if len(written) < 48+40 {
		return nil, nil, 0
	}
	info := written[48:]
	aid = refByteArray(info, 4)
	path = refByteArray(info, 12)
	record = le.Uint32(info[20:])
	return aid, path, record
}

func refByteArray(info []byte, pairPos int) []byte {
	if pairPos+8 > len(info) {
		return nil
	}
	off := le.Uint32(info[pairPos:])
	size := le.Uint32(info[pairPos+4:])
	if int(off)+int(size) > len(info) {
		return nil
	}
	return info[off : off+size]
}

func buildTestSingleAppListInfo(aid []byte) []byte {
	const fixed = 32
	const ptrStart = 16
	out := make([]byte, ptrStart+8) // header + 1 pointer
	le.PutUint32(out[0:], 1)        // Version
	le.PutUint32(out[4:], 1)        // ApplicationCount
	structOff := len(out)
	blob := make([]byte, fixed+pad4(len(aid)))
	le.PutUint32(blob[0:], 2)                // Type = USIM
	le.PutUint32(blob[4:], fixed)            // AppId offset
	le.PutUint32(blob[8:], uint32(len(aid))) // AppId size
	copy(blob[fixed:], aid)
	out = append(out, blob...)
	le.PutUint32(out[ptrStart:], uint32(structOff))
	le.PutUint32(out[ptrStart+4:], uint32(len(blob)))
	return out
}

func buildOpenDone(tx uint32) []byte {
	b := make([]byte, headerLen+4)
	putHeader(b, MessageTypeOpenDone, uint32(len(b)), tx)
	return b
}

// TestAnswerAuthAKA answers OPEN and AUTH_AKA commands with fixed RES/IK/CK.
func TestAnswerAuthAKA(written []byte) ([]byte, bool) {
	h, err := decodeHeader(written)
	if err != nil {
		return nil, false
	}
	if h.Type == MessageTypeOpen {
		return buildOpenDone(h.TransactionID), true
	}
	if h.Type != MessageTypeCommand {
		return nil, false
	}
	var svc UUID
	copy(svc[:], written[20:36])
	cid := le.Uint32(written[36:])
	if svc.Equal(UUIDAuth) && cid == CIDAuthAKA {
		resp := make([]byte, 66)
		for i := 0; i < 8; i++ {
			resp[i] = byte(0x10 + i)
		}
		le.PutUint32(resp[16:], 8) // ResLen
		resp[20] = 0xAA            // IK[0]
		resp[36] = 0xBB            // CK[0]
		return buildCommandDone(h.TransactionID, svc, cid, resp), true
	}
	return buildCommandDone(h.TransactionID, svc, cid, nil), true
}

func buildDeviceCapsInfo(imei string) []byte {
	idb := encodeUTF16String(imei)
	info := make([]byte, capsFixedLen+len(idb))
	le.PutUint32(info[capsPairDeviceID:], capsFixedLen)
	le.PutUint32(info[capsPairDeviceID+4:], uint32(len(idb)))
	copy(info[capsFixedLen:], idb)
	return info
}

func buildCommandDone(tx uint32, service UUID, cid uint32, info []byte) []byte {
	return buildCommandDoneStatus(tx, service, cid, 0, info)
}

// buildCommandDoneStatus is buildCommandDone with an explicit MBIM Status
// (e.g. 0x9 = MBIM_STATUS_NO_DEVICE_SUPPORT, for simulating a CID the
// firmware doesn't implement at all).
func buildCommandDoneStatus(tx uint32, service UUID, cid uint32, status uint32, info []byte) []byte {
	body := fragHdrLen + uuidLen + 4 + 4 + 4 + len(info)
	b := make([]byte, headerLen+body)
	putHeader(b, MessageTypeCommandDone, uint32(len(b)), tx)
	le.PutUint32(b[12:], 1)
	le.PutUint32(b[16:], 0)
	copy(b[20:36], service[:])
	le.PutUint32(b[36:], cid)
	le.PutUint32(b[40:], status)
	le.PutUint32(b[44:], uint32(len(info)))
	copy(b[48:], info)
	return b
}

// buildEFDirRecord builds a single EF_DIR Application Template (tag 0x61)
// TLV wrapping an Application AID (tag 0x4F), per 3GPP TS 102.221.
func buildEFDirRecord(aid []byte) []byte {
	inner := append([]byte{0x4F, byte(len(aid))}, aid...)
	return append([]byte{0x61, byte(len(inner))}, inner...)
}

func buildIndicateStatus(tx uint32, service UUID, cid uint32, info []byte) []byte {
	body := fragHdrLen + uuidLen + 4 + 4 + len(info)
	b := make([]byte, headerLen+body)
	putHeader(b, MessageTypeIndicateStatus, uint32(len(b)), tx)
	le.PutUint32(b[12:], 1)
	le.PutUint32(b[16:], 0)
	copy(b[20:36], service[:])
	le.PutUint32(b[36:], cid)
	le.PutUint32(b[40:], uint32(len(info)))
	copy(b[44:], info)
	return b
}

func encodeUTF16String(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		le.PutUint16(b[i*2:], c)
	}
	return b
}
