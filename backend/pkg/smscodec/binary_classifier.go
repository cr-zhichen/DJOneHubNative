package smscodec

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/warthog618/sms/encoding/tpdu"
)

// 常见 WAP Push / OTA 端口
const (
	wapPushPort16 = 2948
)

// binaryKind 二进制短信分类类型
type binaryKind string

const (
	binaryKindUnknown         binaryKind = "unknown"
	binaryKindOmaCP           binaryKind = "oma_cp"
	binaryKindWAPSI           binaryKind = "wap_si"
	binaryKindWAPSL           binaryKind = "wap_sl"
	binaryKindMMSNotification binaryKind = "mms_notification"
	binaryKindSIMOTA          binaryKind = "sim_ota_23048"
)

// udhPorts 统一承载 UDH 端口寻址解析结果。
// 16-bit (IEI 0x05) 和 8-bit (IEI 0x04) 可能同时存在，优先使用 16-bit。
type udhPorts struct {
	DestPort16 uint16
	SrcPort16  uint16
	Has16Bit   bool

	DestPort8 uint8
	SrcPort8  uint8
	Has8Bit   bool
}

func (p udhPorts) preferredDestPort() (uint16, bool) {
	if p.Has16Bit {
		return p.DestPort16, true
	}
	if p.Has8Bit {
		return uint16(p.DestPort8), true
	}
	return 0, false
}

func parseUDHPorts(udh tpdu.UserDataHeader) udhPorts {
	out := udhPorts{}
	for _, ie := range udh {
		switch ie.ID {
		case 0x05: // 16-bit application port addressing
			if len(ie.Data) >= 4 {
				out.DestPort16 = uint16(ie.Data[0])<<8 | uint16(ie.Data[1])
				out.SrcPort16 = uint16(ie.Data[2])<<8 | uint16(ie.Data[3])
				out.Has16Bit = true
			}
		case 0x04: // 8-bit application port addressing
			if len(ie.Data) >= 2 {
				out.DestPort8 = ie.Data[0]
				out.SrcPort8 = ie.Data[1]
				out.Has8Bit = true
			}
		}
	}
	return out
}

type binarySMSClassification struct {
	Kind            binaryKind
	Label           string
	SummaryLines    []string
	RawHex          string
	Payload         []byte
	ContentType     string
	DestPort        uint16
	HasDestPort     bool
	IsPossEncrypted bool
}

func classifyBinarySMS(t *tpdu.TPDU, msg []byte) binarySMSClassification {
	rawHex := hex.EncodeToString(msg)
	ports := parseUDHPorts(t.UDH)
	destPort, hasDestPort := ports.preferredDestPort()

	c := binarySMSClassification{
		Kind:        binaryKindUnknown,
		Label:       "二进制数据",
		RawHex:      rawHex,
		Payload:     msg,
		DestPort:    destPort,
		HasDestPort: hasDestPort,
	}

	wsp := parseWSPPush(msg)
	if wsp.Ok {
		c.Payload = wsp.Body
		c.ContentType = wsp.ContentType
		if c.ContentType != "" {
			c.SummaryLines = append(c.SummaryLines, "content_type="+c.ContentType)
		}
		c.SummaryLines = append(c.SummaryLines, fmt.Sprintf("wsp_tid=0x%02x pdu=0x%02x", wsp.TransactionID, wsp.PDUType))
	}

	// OMA CP：优先由端口识别，其次由 content-type 识别。
	if (hasDestPort && destPort == wapPushPort16) || isLikelyOMAContentType(c.ContentType) {
		c.Kind = binaryKindOmaCP
		c.Label = "OMA CP 运营商配置短信"
		if cfg, err := DecodeOmaCPFromTPDU(c.Payload); err == nil {
			summary := strings.Split(strings.TrimSpace(FormatOmaCPSummary(cfg)), "\n")
			c.SummaryLines = append(c.SummaryLines, summary...)
		} else {
			c.IsPossEncrypted = true
			c.SummaryLines = append(c.SummaryLines, "wbxml_decode=failed (可能加密/非明文)")
		}
		return c
	}

	// WAP SI / SL
	if isLikelySIContentType(c.ContentType) || isLikelySIWBXML(c.Payload) {
		c.Kind = binaryKindWAPSI
		c.Label = "WAP SI Push"
		addSISLHints(&c, c.Payload)
		return c
	}
	if isLikelySLContentType(c.ContentType) || isLikelySLWBXML(c.Payload) {
		c.Kind = binaryKindWAPSL
		c.Label = "WAP SL Push"
		addSISLHints(&c, c.Payload)
		return c
	}

	// MMS Notification
	if isLikelyMMSContentType(c.ContentType) || isLikelyMMSNotification(c.Payload) {
		c.Kind = binaryKindMMSNotification
		c.Label = "MMS Notification"
		addMMSHints(&c, c.Payload)
		return c
	}

	// SIM OTA (23.048) 分类识别（不解密）
	if isLikelySIMOTA(t, c.Payload, c.ContentType, hasDestPort, destPort) {
		c.Kind = binaryKindSIMOTA
		c.Label = "SIM OTA 23.048"
		if spi, kic, kid, ok := tryParseSIMOTAHeader(c.Payload); ok {
			c.SummaryLines = append(c.SummaryLines,
				fmt.Sprintf("spi=0x%04x kic=0x%02x kid=0x%02x", spi, kic, kid))
		}
		c.IsPossEncrypted = true
		c.SummaryLines = append(c.SummaryLines, "decrypt=not_attempted")
		return c
	}

	return c
}

func formatBinaryClassification(c binarySMSClassification) string {
	var sb strings.Builder
	sb.WriteString("[")
	sb.WriteString(c.Label)
	sb.WriteString("]")
	sb.WriteString("\n")
	if c.HasDestPort {
		sb.WriteString(fmt.Sprintf("dest_port=%d\n", c.DestPort))
	}
	for _, line := range c.SummaryLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	if c.IsPossEncrypted {
		sb.WriteString("security=可能加密\n")
	}
	sb.WriteString("raw=")
	sb.WriteString(c.RawHex)
	return sb.String()
}

type wspPush struct {
	Ok            bool
	TransactionID byte
	PDUType       byte
	ContentType   string
	Body          []byte
}

func parseWSPPush(data []byte) wspPush {
	// 简化实现：按最常见 Push 结构解析
	// [TID][PDU Type][HeadersLen][Headers...][Body...]
	if len(data) < 4 {
		return wspPush{}
	}
	tid := data[0]
	pduType := data[1]
	if pduType != 0x06 && pduType != 0x07 {
		return wspPush{}
	}
	headersLen := int(data[2])
	if headersLen < 0 || 3+headersLen > len(data) {
		return wspPush{}
	}
	headers := data[3 : 3+headersLen]
	body := data[3+headersLen:]
	ct := parseWSPContentType(headers)
	return wspPush{
		Ok:            true,
		TransactionID: tid,
		PDUType:       pduType,
		ContentType:   ct,
		Body:          body,
	}
}

func parseWSPContentType(headers []byte) string {
	if len(headers) == 0 {
		return ""
	}

	// 常见短整型编码：bit7=1，value=低7位
	if headers[0]&0x80 != 0 {
		return mapWSPContentTypeToken(headers[0] & 0x7F)
	}

	// 文本字符串 content-type（null 结尾）
	if isLikelyASCII(headers[0]) {
		ct := readCString(headers)
		return normalizeContentType(ct)
	}

	// Value-length + media-type
	// 这里仅做轻量容错解析，避免误判。
	valueLen := int(headers[0])
	if valueLen > 0 && 1+valueLen <= len(headers) {
		v := headers[1 : 1+valueLen]
		if len(v) > 0 && v[0]&0x80 != 0 {
			return mapWSPContentTypeToken(v[0] & 0x7F)
		}
		return normalizeContentType(readCString(v))
	}

	return ""
}

func mapWSPContentTypeToken(tok byte) string {
	switch tok {
	case 0x2e:
		return "application/vnd.wap.sic"
	case 0x30:
		return "application/vnd.wap.connectivity-wbxml"
	case 0x31:
		return "application/vnd.wap.slc"
	case 0x3e:
		return "application/vnd.wap.mms-message"
	default:
		return fmt.Sprintf("token:0x%02x", tok)
	}
}

func readCString(b []byte) string {
	n := 0
	for n < len(b) && b[n] != 0x00 {
		n++
	}
	return string(b[:n])
}

func normalizeContentType(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func isLikelyASCII(b byte) bool {
	return b >= 0x20 && b <= 0x7e
}

func isLikelyOMAContentType(ct string) bool {
	return ct == "application/vnd.wap.connectivity-wbxml"
}

func isLikelySIContentType(ct string) bool {
	return ct == "application/vnd.wap.sic" || ct == "application/vnd.wap.si"
}

func isLikelySLContentType(ct string) bool {
	return ct == "application/vnd.wap.slc" || ct == "application/vnd.wap.sl"
}

func isLikelyMMSContentType(ct string) bool {
	return ct == "application/vnd.wap.mms-message"
}

func wbxmlPublicID(data []byte) (uint32, bool) {
	if len(data) < 3 {
		return 0, false
	}
	if data[0] < 0x01 || data[0] > 0x03 {
		return 0, false
	}
	pid, _, ok := parseMBUint32At(data, 1)
	return pid, ok
}

func isLikelySIWBXML(data []byte) bool {
	pid, ok := wbxmlPublicID(data)
	// 常见 SI public id
	return ok && (pid == 0x05 || pid == 0x06)
}

func isLikelySLWBXML(data []byte) bool {
	pid, ok := wbxmlPublicID(data)
	// 常见 SL public id
	return ok && (pid == 0x06 || pid == 0x07)
}

func addSISLHints(c *binarySMSClassification, payload []byte) {
	if u := extractURL(payload); u != "" {
		c.SummaryLines = append(c.SummaryLines, "url="+u)
	}
	if hint := extractPrintableHint(payload, 80); hint != "" {
		c.SummaryLines = append(c.SummaryLines, "hint="+hint)
	}
}

func extractURL(data []byte) string {
	s := string(data)
	for _, prefix := range []string{"https://", "http://"} {
		i := strings.Index(s, prefix)
		if i < 0 {
			continue
		}
		j := i
		for j < len(s) {
			ch := s[j]
			if ch == 0 || ch == ' ' || ch == '\r' || ch == '\n' || ch == '"' || ch == '\'' || ch == '<' || ch == '>' {
				break
			}
			j++
		}
		return s[i:j]
	}
	return ""
}

func extractPrintableHint(data []byte, max int) string {
	var sb strings.Builder
	for _, b := range data {
		if (b >= 0x20 && b <= 0x7e) || b == '\n' || b == '\r' || b == '\t' {
			sb.WriteByte(b)
		}
		if sb.Len() >= max {
			break
		}
	}
	return strings.TrimSpace(sb.String())
}

func isLikelyMMSNotification(payload []byte) bool {
	// X-Mms-Message-Type (0x8C) == m-notification-ind (0x82)
	for i := 0; i+1 < len(payload); i++ {
		if payload[i] == 0x8C && payload[i+1] == 0x82 {
			return true
		}
	}
	return false
}

func addMMSHints(c *binarySMSClassification, payload []byte) {
	if txid := extractTaggedCString(payload, 0x98); txid != "" {
		c.SummaryLines = append(c.SummaryLines, "x_mms_transaction_id="+txid)
	}
	if loc := extractTaggedCString(payload, 0x83); loc != "" {
		c.SummaryLines = append(c.SummaryLines, "content_location="+loc)
	}
	if size, ok := extractTaggedUint(payload, 0x8e); ok {
		c.SummaryLines = append(c.SummaryLines, fmt.Sprintf("message_size=%d", size))
	}
	if u := extractURL(payload); u != "" {
		c.SummaryLines = append(c.SummaryLines, "url="+u)
	}
}

func extractTaggedCString(data []byte, tag byte) string {
	for i := 0; i+1 < len(data); i++ {
		if data[i] != tag {
			continue
		}
		v := readCString(data[i+1:])
		if v != "" {
			return v
		}
	}
	return ""
}

func extractTaggedUint(data []byte, tag byte) (uint32, bool) {
	for i := 0; i+1 < len(data); i++ {
		if data[i] != tag {
			continue
		}
		v, _, ok := parseMBUint32At(data, i+1)
		if ok {
			return v, true
		}
	}
	return 0, false
}

func isLikelySIMOTA(t *tpdu.TPDU, payload []byte, ct string, hasDestPort bool, destPort uint16) bool {
	// 不把已识别的 WAP Push 家族误判成 SIM OTA
	if ct != "" {
		if isLikelyOMAContentType(ct) || isLikelySIContentType(ct) || isLikelySLContentType(ct) || isLikelyMMSContentType(ct) {
			return false
		}
	}
	if hasDestPort && destPort == wapPushPort16 {
		return false
	}

	pidVal := byte(t.PID)
	dcsVal := byte(t.DCS)

	// SMS-PP 下载常见 PID=0x7F；DCS class2 也常见于 SIM 下载。
	if pidVal == 0x7f {
		return true
	}
	if isLikelyClass2(dcsVal) {
		_, _, _, ok := tryParseSIMOTAHeader(payload)
		if ok {
			return true
		}
	}
	return false
}

func isLikelyClass2(dcs byte) bool {
	// 轻量判定：当 DCS 指示消息类且 class==2
	// 对常见一般数据编码组生效，避免引入复杂 DCS 全解析。
	if dcs&0x10 == 0 {
		return false
	}
	return dcs&0x03 == 0x02
}

// tryParseSIMOTAHeader 尝试解析 23.048 可见安全头字段（仅分类用途）。
func tryParseSIMOTAHeader(payload []byte) (spi uint16, kic byte, kid byte, ok bool) {
	if len(payload) < 6 {
		return 0, 0, 0, false
	}
	// 常见首字节 CPL、次字节 CHL。CHL 至少应覆盖 SPI/KIC/KID。
	chl := int(payload[1])
	if chl < 5 || 2+chl > len(payload) {
		return 0, 0, 0, false
	}
	spi = uint16(payload[2])<<8 | uint16(payload[3])
	kic = payload[4]
	kid = payload[5]
	return spi, kic, kid, true
}
