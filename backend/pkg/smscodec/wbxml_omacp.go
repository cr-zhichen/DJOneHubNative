package smscodec

import (
	"fmt"
	"strings"

	"github.com/warthog618/sms/encoding/tpdu"
)

// WAP Push 端口常量
const (
	WAPPushOmaCPPort uint16 = 2948 // OMA Client Provisioning 目标端口
)

// ── OMA CP 解码后的数据结构 ──────────────────────────────────────

// OmaCPCharacteristic 表示 OMA CP 配置文档中的一个特征项（如 NAPDEF、APPLICATION 等）
type OmaCPCharacteristic struct {
	Type   string                // 特征类型：NAPDEF, APPLICATION, PXLOGICAL, ACCESS 等
	Params map[string]string     // parm 参数映射：name → value
	Subs   []OmaCPCharacteristic // 嵌套子特征
}

// OmaCPConfig 解码后的完整 OMA CP 配置文档
type OmaCPConfig struct {
	Version         string                // WBXML 版本
	Characteristics []OmaCPCharacteristic // 顶层特征列表
}

// ── UDH 端口检测 ─────────────────────────────────────────────────

// extractUDHDestPort16 从 UDH 中提取 16 位目标端口号
// UDH IE ID 0x05 = Application Port Addressing (16-bit)
func extractUDHDestPort16(udh tpdu.UserDataHeader) (uint16, bool) {
	for _, ie := range udh {
		if ie.ID == 0x05 && len(ie.Data) >= 4 {
			return uint16(ie.Data[0])<<8 | uint16(ie.Data[1]), true
		}
	}
	return 0, false
}

// IsOmaCPMessage 检测 TPDU 是否为 OMA CP 配置短信（通过 UDH 目标端口 2948 判断）
func IsOmaCPMessage(udh tpdu.UserDataHeader) bool {
	port, ok := parseUDHPorts(udh).preferredDestPort()
	return ok && port == WAPPushOmaCPPort
}

// ── 公开 API ─────────────────────────────────────────────────────

// DecodeOmaCPFromTPDU 尝试从 TPDU 用户数据中解码 OMA CP 配置。
// data 是 UDH 已剥离后的用户数据（可能包含 WSP Push header + WBXML body）。
func DecodeOmaCPFromTPDU(data []byte) (*OmaCPConfig, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("OMA CP 数据过短 (%d bytes)", len(data))
	}

	// 尝试多个偏移量定位 WBXML 起始位置
	wbxmlData, err := findWBXMLStart(data)
	if err != nil {
		return nil, err
	}

	return decodeWBXML(wbxmlData)
}

// FormatOmaCPSummary 将解码后的 OMA CP 配置格式化为人类可读摘要
func FormatOmaCPSummary(cfg *OmaCPConfig) string {
	if cfg == nil || len(cfg.Characteristics) == 0 {
		return "(空配置)"
	}
	var sb strings.Builder
	for i, c := range cfg.Characteristics {
		if i > 0 {
			sb.WriteString("\n")
		}
		formatCharacteristic(&sb, &c, 0)
	}
	return sb.String()
}

// ── WBXML 定位与解析 ─────────────────────────────────────────────

// findWBXMLStart 在数据中定位 WBXML 文档起始位置。
// 支持以下场景：
//  1. 直接是 WBXML（无 WSP 包装）
//  2. WSP Push header + WBXML
//  3. 扫描已知 WBXML 签名
func findWBXMLStart(data []byte) ([]byte, error) {
	// 场景 1: 直接是 WBXML（版本 0x01-0x03，公共 ID 匹配 OMA CP）
	if isWBXMLHeader(data) {
		return data, nil
	}

	// 场景 2: 跳过 WSP Push header，寻找 WBXML
	// WSP Push 最小格式：[headers_len] [content_type] ... [WBXML]
	// 实网中 WSP 头可能超过 16 字节，需全量扫描后续偏移。
	for offset := 1; offset < len(data); offset++ {
		if isWBXMLHeader(data[offset:]) {
			return data[offset:], nil
		}
	}

	return nil, fmt.Errorf("未找到 WBXML 文档头（可能是加密的 OMA CP 配置短信）")
}

// isWBXMLHeader 检测字节流起始是否为合法的 WBXML 文档头
func isWBXMLHeader(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	version := data[0]
	// WBXML 版本：0x01(1.0), 0x02(1.1), 0x03(1.3)
	if version < 0x01 || version > 0x03 {
		return false
	}
	publicID, next, ok := parseMBUint32At(data, 1)
	if !ok || publicID != 0x0B {
		return false
	}

	// 尝试继续解析 charset 和 strTableLen，避免误把随机数据识别为 WBXML。
	_, next, ok = parseMBUint32At(data, next)
	if !ok {
		return false
	}
	strTableLen, next, ok := parseMBUint32At(data, next)
	if !ok {
		return false
	}
	return uint64(next)+uint64(strTableLen) <= uint64(len(data))
}

// parseMBUint32At 从 data[start:] 解析一个 WBXML mb_u_int32。
func parseMBUint32At(data []byte, start int) (value uint32, next int, ok bool) {
	if start < 0 || start >= len(data) {
		return 0, start, false
	}
	var result uint32
	pos := start
	for i := 0; i < 5; i++ {
		if pos >= len(data) {
			return 0, pos, false
		}
		b := data[pos]
		pos++
		result = (result << 7) | uint32(b&0x7F)
		if b&0x80 == 0 {
			return result, pos, true
		}
	}
	return 0, pos, false
}

// ── WBXML 解码器 ─────────────────────────────────────────────────

// wbxml 全局 token 常量
const (
	wbxmlSwitchPage byte = 0x00
	wbxmlEnd        byte = 0x01
	wbxmlStrI       byte = 0x03
	wbxmlLiteral    byte = 0x04
	wbxmlStrT       byte = 0x83
	wbxmlOpaque     byte = 0xC3
)

// wbxml tag token 标志位
const (
	wbxmlHasContent byte = 0x40
	wbxmlHasAttrs   byte = 0x80
)

// wbxmlReader WBXML 字节流读取器
type wbxmlReader struct {
	data     []byte
	pos      int
	strTable []byte // 字符串表
	tagPage  int    // 当前 tag code page
	attrPage int    // 当前 attribute code page
}

func (r *wbxmlReader) remaining() int { return len(r.data) - r.pos }
func (r *wbxmlReader) eof() bool      { return r.pos >= len(r.data) }

func (r *wbxmlReader) readByte() (byte, error) {
	if r.eof() {
		return 0, fmt.Errorf("WBXML: 意外的文件结束 (pos=%d)", r.pos)
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

// readMBUint32 读取 WBXML 多字节无符号整数（mb_u_int32）
func (r *wbxmlReader) readMBUint32() (uint32, error) {
	var result uint32
	for i := 0; i < 5; i++ {
		b, err := r.readByte()
		if err != nil {
			return 0, err
		}
		result = (result << 7) | uint32(b&0x7F)
		if b&0x80 == 0 {
			return result, nil
		}
	}
	return 0, fmt.Errorf("WBXML: mb_uint32 编码超长")
}

// readStrI 读取 inline null-terminated 字符串
func (r *wbxmlReader) readStrI() (string, error) {
	start := r.pos
	for r.pos < len(r.data) && r.data[r.pos] != 0x00 {
		r.pos++
	}
	if r.pos >= len(r.data) {
		return string(r.data[start:]), nil // 容错：未找到 null 终止符
	}
	s := string(r.data[start:r.pos])
	r.pos++ // 跳过 null 终止符
	return s, nil
}

// readStrT 从字符串表中按偏移读取字符串
func (r *wbxmlReader) readStrT(offset uint32) string {
	if int(offset) >= len(r.strTable) {
		return ""
	}
	end := int(offset)
	for end < len(r.strTable) && r.strTable[end] != 0x00 {
		end++
	}
	return string(r.strTable[offset:end])
}

// readBytes 读取指定长度的原始字节
func (r *wbxmlReader) readBytes(n int) ([]byte, error) {
	if r.pos+n > len(r.data) {
		return nil, fmt.Errorf("WBXML: 数据不足 (need %d, have %d)", n, r.remaining())
	}
	b := make([]byte, n)
	copy(b, r.data[r.pos:r.pos+n])
	r.pos += n
	return b, nil
}

// decodeWBXML 解码完整的 WBXML 文档为 OMA CP 配置
func decodeWBXML(data []byte) (*OmaCPConfig, error) {
	r := &wbxmlReader{data: data}

	// 1. 读取 WBXML header
	version, err := r.readByte()
	if err != nil {
		return nil, fmt.Errorf("WBXML header 读取失败: %w", err)
	}

	publicID, err := r.readMBUint32()
	if err != nil {
		return nil, fmt.Errorf("WBXML publicID 读取失败: %w", err)
	}
	if publicID != 0x0B {
		return nil, fmt.Errorf("WBXML publicID=0x%X 不是 OMA CP (预期 0x0B)", publicID)
	}

	charset, err := r.readMBUint32()
	if err != nil {
		return nil, fmt.Errorf("WBXML charset 读取失败: %w", err)
	}
	_ = charset

	// 2. 读取字符串表
	strTableLen, err := r.readMBUint32()
	if err != nil {
		return nil, fmt.Errorf("WBXML 字符串表长度读取失败: %w", err)
	}
	if strTableLen > 0 {
		r.strTable, err = r.readBytes(int(strTableLen))
		if err != nil {
			return nil, fmt.Errorf("WBXML 字符串表读取失败: %w", err)
		}
	}

	// 3. 解析 body
	cfg := &OmaCPConfig{
		Version: fmt.Sprintf("%d.%d", (version>>4)+1, version&0x0F),
	}

	for !r.eof() {
		chars, err := r.parseElement()
		if err != nil {
			break // 容错：部分解码也返回结果
		}
		if chars != nil {
			cfg.Characteristics = append(cfg.Characteristics, *chars)
		}
	}

	if len(cfg.Characteristics) == 0 {
		return nil, fmt.Errorf("WBXML 解码未得到任何配置项")
	}

	return cfg, nil
}

// parseElement 解析一个 WBXML 元素（tag + attributes + content）
// 返回 OmaCPCharacteristic（如果是 characteristic 或 parm 元素）
func (r *wbxmlReader) parseElement() (*OmaCPCharacteristic, error) {
	if r.eof() {
		return nil, fmt.Errorf("EOF")
	}

	b, err := r.readByte()
	if err != nil {
		return nil, err
	}

	// 处理全局 token
	switch b {
	case wbxmlSwitchPage:
		page, err := r.readByte()
		if err != nil {
			return nil, err
		}
		r.tagPage = int(page)
		return r.parseElement() // 递归解析下一个元素
	case wbxmlEnd:
		return nil, fmt.Errorf("END") // 信号：当前层级结束
	}

	// 解析 tag token
	hasContent := b&wbxmlHasContent != 0
	hasAttrs := b&wbxmlHasAttrs != 0
	tagID := b & 0x3F

	tagName := resolveTagName(r.tagPage, tagID)

	// 解析 attributes
	attrs := make(map[string]string)
	if hasAttrs {
		r.parseAttributes(attrs)
	}

	// 解析 content
	var children []OmaCPCharacteristic
	var textContent strings.Builder
	if hasContent {
		for !r.eof() {
			peek := r.data[r.pos]
			if peek == wbxmlEnd {
				r.pos++ // 消费 END token
				break
			}
			if peek == wbxmlStrI {
				r.pos++
				s, _ := r.readStrI()
				textContent.WriteString(s)
				continue
			}
			if peek == wbxmlStrT {
				r.pos++
				offset, _ := r.readMBUint32()
				textContent.WriteString(r.readStrT(offset))
				continue
			}
			if peek == wbxmlOpaque {
				r.pos++
				length, _ := r.readMBUint32()
				r.readBytes(int(length)) // 跳过 opaque 数据
				continue
			}
			if peek == wbxmlSwitchPage {
				r.pos++
				page, _ := r.readByte()
				r.tagPage = int(page)
				continue
			}
			// 嵌套元素
			child, err := r.parseElement()
			if err != nil {
				break
			}
			if child != nil {
				children = append(children, *child)
			}
		}
	}

	// 构建返回结构
	switch tagName {
	case "characteristic":
		c := &OmaCPCharacteristic{
			Type:   attrs["type"],
			Params: make(map[string]string),
			Subs:   children,
		}
		// 收集子元素中的 parm 参数
		for i := range children {
			if children[i].Type == "__parm__" {
				for k, v := range children[i].Params {
					c.Params[k] = v
				}
			}
		}
		// 过滤掉已合并的 __parm__ 子元素
		filtered := make([]OmaCPCharacteristic, 0, len(children))
		for _, sub := range children {
			if sub.Type != "__parm__" {
				filtered = append(filtered, sub)
			}
		}
		c.Subs = filtered
		return c, nil

	case "parm":
		name := attrs["name"]
		value := attrs["value"]
		if name == "" && textContent.Len() > 0 {
			name = textContent.String()
		}
		if strings.TrimSpace(name) == "" {
			// 无参数名的 parm 无法稳定映射，直接忽略以避免污染空 key。
			return nil, nil
		}
		return &OmaCPCharacteristic{
			Type:   "__parm__",
			Params: map[string]string{name: value},
		}, nil

	case "wap-provisioningdoc":
		// 顶层文档元素：直接返回子元素
		if len(children) > 0 {
			return &OmaCPCharacteristic{
				Type: "wap-provisioningdoc",
				Subs: children,
			}, nil
		}
		return nil, nil

	default:
		return nil, nil
	}
}

// parseAttributes 解析 WBXML 属性列表，填充到 attrs map 中
func (r *wbxmlReader) parseAttributes(attrs map[string]string) {
	var currentAttrName string
	var currentValue strings.Builder

	flushAttr := func() {
		if currentAttrName != "" {
			attrs[currentAttrName] = strings.TrimSpace(currentValue.String())
		}
		currentAttrName = ""
		currentValue.Reset()
	}

	for !r.eof() {
		b, err := r.readByte()
		if err != nil {
			break
		}

		// END token 结束属性列表
		if b == wbxmlEnd {
			flushAttr()
			return
		}

		// SWITCH_PAGE 切换 attribute code page
		if b == wbxmlSwitchPage {
			page, err := r.readByte()
			if err != nil {
				break
			}
			r.attrPage = int(page)
			continue
		}

		// STR_I inline string → 追加到当前属性值
		if b == wbxmlStrI {
			s, _ := r.readStrI()
			currentValue.WriteString(s)
			continue
		}

		// STR_T string table reference → 追加到当前属性值
		if b == wbxmlStrT {
			offset, _ := r.readMBUint32()
			currentValue.WriteString(r.readStrT(offset))
			continue
		}

		// OPAQUE data → 跳过
		if b == wbxmlOpaque {
			length, _ := r.readMBUint32()
			r.readBytes(int(length))
			continue
		}

		// WBXML attribute value 支持 token 串联；当处于某属性值上下文时，优先解释 ATTRVALUE。
		if currentAttrName != "" {
			if value := resolveAttrValue(r.attrPage, b); value != "" && !strings.HasPrefix(value, "[0x") {
				currentValue.WriteString(value)
				continue
			}
		}

		// 尝试匹配 ATTRSTART（包括 0xA0 以上被复用为 type 属性入口的 token）
		if entryName, entryVal := resolveAttrStart(r.attrPage, b); entryName != "" && !strings.HasPrefix(entryName, "attr_") {
			flushAttr() // 先保存上一个属性
			currentAttrName = entryName
			currentValue.Reset()
			currentValue.WriteString(entryVal)
			continue
		}

		// 尝试解释为 ATTRVALUE（如果 token 存在于 value 映射表中）
		if value := resolveAttrValue(r.attrPage, b); value != "" && !strings.HasPrefix(value, "[0x") {
			currentValue.WriteString(value)
			continue
		}
	}

	flushAttr()
}

// ── OMA CP Token 映射表 ─────────────────────────────────────────

// resolveTagName 解析 tag token 为 XML tag 名
func resolveTagName(codePage int, tagID byte) string {
	if names, ok := omaCPTagTable[codePage]; ok {
		if name, ok := names[tagID]; ok {
			return name
		}
	}
	return fmt.Sprintf("tag_0x%02X", tagID)
}

// tag token 映射表（所有 code page）
var omaCPTagTable = map[int]map[byte]string{
	0: {
		0x05: "wap-provisioningdoc",
		0x06: "characteristic",
		0x07: "parm",
	},
	1: {
		0x06: "characteristic",
		0x07: "parm",
	},
}

// attrTokenEntry 属性 token 条目
type attrTokenEntry struct {
	name  string // XML 属性名（如 "name", "value", "type"）
	value string // 属性值或值前缀（空表示需要后续 token 提供值）
}

// resolveAttrStart 解析 ATTRSTART token 为属性名和初始值
func resolveAttrStart(codePage int, token byte) (attrName, attrValue string) {
	var table map[byte]attrTokenEntry
	switch codePage {
	case 0:
		table = omaCPAttrStartPage0
	case 1:
		table = omaCPAttrStartPage1
	}
	if table != nil {
		if entry, ok := table[token]; ok {
			return entry.name, entry.value
		}
	}
	return fmt.Sprintf("attr_0x%02X", token), ""
}

// resolveAttrValue 解析 ATTRVALUE token 为属性值字符串
func resolveAttrValue(codePage int, token byte) string {
	var table map[byte]string
	switch codePage {
	case 0:
		table = omaCPAttrValuePage0
	case 1:
		table = omaCPAttrValuePage1
	}
	if table != nil {
		if v, ok := table[token]; ok {
			return v
		}
	}
	return fmt.Sprintf("[0x%02X]", token)
}

// ── Code Page 0: 基础配置属性 ──────────────────────────────────

// ATTRSTART tokens (code page 0)
// 参考 OMA-WAP-ProvCont-v1_1 Section 7.1
var omaCPAttrStartPage0 = map[byte]attrTokenEntry{
	// parm 的 name/value 属性
	0x05: {"name", ""},
	0x06: {"value", ""},
	0x07: {"name", "NAME"},
	0x08: {"name", "NAP-ADDRESS"},
	0x09: {"name", "NAP-ADDRTYPE"},
	0x0A: {"name", "CALLTYPE"},
	0x0B: {"name", "VALIDUNTIL"},
	0x0C: {"name", "AUTHTYPE"},
	0x0D: {"name", "AUTHNAME"},
	0x0E: {"name", "AUTHSECRET"},
	0x0F: {"name", "LINGER"},
	0x10: {"name", "BEARER"},
	0x11: {"name", "NAPID"},
	0x12: {"name", "COUNTRY"},
	0x13: {"name", "NETWORK"},
	0x14: {"name", "INTERNET"},
	0x15: {"name", "PROXY-ID"},
	0x16: {"name", "PROXY-PROVIDER-ID"},
	0x17: {"name", "DOMAIN"},
	0x18: {"name", "PROVURL"},
	0x19: {"name", "PXAUTH-TYPE"},
	0x1A: {"name", "PXAUTH-ID"},
	0x1B: {"name", "PXAUTH-PW"},
	0x1C: {"name", "STARTPAGE"},
	0x1D: {"name", "BASAUTH-ID"},
	0x1E: {"name", "BASAUTH-PW"},
	0x1F: {"name", "PUSHENABLED"},
	0x20: {"name", "PXADDR"},
	0x21: {"name", "PXADDRTYPE"},
	0x22: {"name", "TO-NAPID"},
	0x23: {"name", "PORTNBR"},
	0x24: {"name", "SERVICE"},
	0x25: {"name", "LINKSPEED"},
	0x26: {"name", "DNLINKSPEED"},
	0x27: {"name", "LOCAL-ADDR"},
	0x28: {"name", "LOCAL-ADDRTYPE"},
	0x29: {"name", "CONTEXT-ALLOW"},
	0x2A: {"name", "TRUST"},
	0x2B: {"name", "MASTER"},
	0x2C: {"name", "SID"},
	0x2D: {"name", "SOC"},
	0x2E: {"name", "WSP-VERSION"},
	0x2F: {"name", "PHYSICAL-PROXY-ID"},
	0x30: {"name", "CLIENT-ID"},
	0x31: {"name", "DELIVERY-ERR-SDU"},
	0x32: {"name", "DELIVERY-ORDER"},
	0x33: {"name", "TRAFFIC-CLASS"},
	0x34: {"name", "MAX-SDU-SIZE"},
	0x35: {"name", "MAX-BITRATE-UPLINK"},
	0x36: {"name", "MAX-BITRATE-DNLINK"},
	0x37: {"name", "RESIDUAL-BER"},
	0x38: {"name", "SDU-ERROR-RATIO"},
	0x39: {"name", "TRAFFIC-HANDL-PRIO"},
	0x3A: {"name", "TRANSFER-DELAY"},
	0x3B: {"name", "GUARANTEED-BITRATE-UPLINK"},
	0x3C: {"name", "GUARANTEED-BITRATE-DNLINK"},
	0x3D: {"name", "PXADDR-FQDN"},
	0x3E: {"name", "PROXY-PW"},
	0x3F: {"name", "PPGAUTH-TYPE"},
}

// ATTRVALUE tokens (code page 0) — 出现在 value > 0x80 区间或特殊位置
var omaCPAttrValuePage0 = map[byte]string{
	// 地址类型值
	0x45: "IPV4",
	0x46: "IPV6",
	0x47: "E164",
	0x48: "ALPHA",
	0x49: "APN",
	0x4A: "SCODE",
	0x4B: "TETRA-ITSI",
	0x4C: "MAN",
	// 呼叫类型值
	0x50: "ANALOG-MODEM",
	0x51: "V.120",
	0x52: "V.110",
	0x53: "X.31",
	0x54: "BIT-TRANSPARENT",
	0x55: "DIRECT-ASYNCHRONOUS-DATA-SERVICE",
	// 认证类型值
	0x60: "PAP",
	0x61: "CHAP",
	0x62: "HTTP-BASIC",
	0x63: "HTTP-DIGEST",
	0x64: "WTLS-SS",
	0x65: "MD5",
	// 承载类型值
	0x6A: "GSM-USSD",
	0x6B: "GSM-SMS",
	0x6C: "ANSI-136-GUTS",
	0x6D: "IS-95-CDMA-SMS",
	0x6E: "IS-95-CDMA-CSD",
	0x6F: "IS-95-CDMA-PACKET",
	0x70: "ANSI-136-CSD",
	0x71: "ANSI-136-GPRS",
	0x72: "GSM-CSD",
	0x73: "GSM-GPRS",
	0x74: "AMPS-CDPD",
	0x75: "PDC-CSD",
	0x76: "PDC-PACKET",
	0x77: "IDEN-SMS",
	0x78: "IDEN-CSD",
	0x79: "IDEN-PACKET",
	0x7A: "FLEX/REFLEX",
	0x7B: "PHS-SMS",
	0x7C: "PHS-CSD",
	0x7D: "TETRA-SDS",
	0x7E: "TETRA-PACKET",
	0x7F: "ANSI-136-GHOST",
	0x80: "MOBITEX-MPAK",
	0x81: "CDMA2000-1X-SIMPLE-IP",
	0x82: "CDMA2000-1X-MOBILE-IP",
	0x85: "AUTOBAUDING",
	// 服务类型值
	0x8A: "CL-WSP",
	0x8B: "CO-WSP",
	0x8C: "CL-SEC-WSP",
	0x8D: "CO-SEC-WSP",
	0x8E: "CL-SEC-WTA",
	0x8F: "CO-SEC-WTA",
	0x90: "OTA-HTTP-TO",
	0x91: "OTA-HTTP-TLS-TO",
	0x92: "OTA-HTTP-PO",
	0x93: "OTA-HTTP-TLS-PO",
	// characteristic type 值
	0xA0: "PXLOGICAL",
	0xA1: "PXPHYSICAL",
	0xA2: "PORT",
	0xA3: "VALIDITY",
	0xA4: "NAPDEF",
	0xA5: "BOOTSTRAP",
	0xA6: "VENDORCONFIG",
	0xA7: "CLIENTIDENTITY",
	0xA8: "PXAUTHINFO",
	0xA9: "NAPAUTHINFO",
	0xAA: "ACCESS",
}

// ── Code Page 1: APPLICATION 属性 ─────────────────────────────

// ATTRSTART tokens (code page 1)
var omaCPAttrStartPage1 = map[byte]attrTokenEntry{
	0x05: {"name", ""},
	0x06: {"value", ""},
	0x07: {"name", "NAME"},
	0x08: {"name", "INTERNET"},
	0x10: {"name", "STARTPAGE"},
	0x11: {"name", "TO-NAPID"},
	0x12: {"name", "PORTNBR"},
	0x13: {"name", "SERVICE"},
	0x14: {"name", "AACCEPT"},
	0x15: {"name", "AAUTHDATA"},
	0x16: {"name", "AAUTHLEVEL"},
	0x17: {"name", "AAUTHNAME"},
	0x18: {"name", "AAUTHSECRET"},
	0x19: {"name", "AAUTHTYPE"},
	0x1A: {"name", "ADDR"},
	0x1B: {"name", "ADDRTYPE"},
	0x1C: {"name", "APPID"},
	0x1D: {"name", "APROTOCOL"},
	0x1E: {"name", "PROVIDER-ID"},
	0x1F: {"name", "TO-PROXY"},
	0x20: {"name", "URI"},
	0x21: {"name", "RULE"},
}

// ATTRVALUE tokens (code page 1)
var omaCPAttrValuePage1 = map[byte]string{
	0x45: "IPV4",
	0x46: "IPV6",
	0x50: "APSERVICE",
	0x51: "OTA-HTTP-TO",
	0x52: "OTA-HTTP-TLS-TO",
	0x53: "OTA-HTTP-PO",
	0x54: "OTA-HTTP-TLS-PO",
	// 常见 APPID 端口号
	0x55: "25",
	0x56: "143",
	0x57: "993",
	0x58: "110",
	0x59: "995",
	0x5A: "119",
	0x5B: "563",
	0x5C: "209",
	0x60: ",",
	0x61: "HTTP-",
	0x62: "BASIC",
	0x63: "DIGEST",
	// APPID 应用标识
	0x90: "w2",
	0x91: "w4",
	0x92: "w5",
	0x93: "w7",
	// characteristic type 值
	0xA0: "PORT",
	0xA1: "CLIENTIDENTITY",
	0xA2: "APPADDR",
	0xA3: "APPAUTH",
	0xA4: "APPLICATION",
	0xA5: "RESOURCE",
}

// 上面的 ATTRVALUE token 0xA0-0xA5 同时用作 type 属性值，
// 需要特殊处理：当它们出现在 characteristic 的 type 属性上下文中时解析为 type 值。
func init() {
	// 将 code page 0 的 type 值也添加到 attrStart 表（作为 type="VALUE" 的起始 token）
	for token, value := range omaCPAttrValuePage0 {
		if token >= 0xA0 && token <= 0xAA {
			omaCPAttrStartPage0[token] = attrTokenEntry{"type", value}
		}
	}
	// code page 1 同理
	for token, value := range omaCPAttrValuePage1 {
		if token >= 0xA0 && token <= 0xA5 {
			omaCPAttrStartPage1[token] = attrTokenEntry{"type", value}
		}
	}
}

// ── 配置摘要格式化 ───────────────────────────────────────────────

// appIDNames 常见 APPID 的人类可读名称
var appIDNames = map[string]string{
	"w2":              "WAP 浏览器",
	"w4":              "浏览器书签",
	"w5":              "MMS 彩信",
	"w7":              "SyncML DM",
	"25":              "SMTP 邮件",
	"110":             "POP3 邮件",
	"143":             "IMAP4 邮件",
	"ap0004":          "MMS",
	"ap0005":          "SyncML DM",
	"APSERVICE":       "通用应用服务",
	"OTA-HTTP-TO":     "OTA HTTP",
	"OTA-HTTP-TLS-TO": "OTA HTTPS",
}

// formatCharacteristic 递归格式化一个配置特征项
func formatCharacteristic(sb *strings.Builder, c *OmaCPCharacteristic, indent int) {
	prefix := strings.Repeat("  ", indent)

	// 跳过文档根元素，直接输出子元素
	if c.Type == "wap-provisioningdoc" {
		for i, sub := range c.Subs {
			if i > 0 {
				sb.WriteString("\n")
			}
			formatCharacteristic(sb, &sub, indent)
		}
		return
	}

	sb.WriteString(prefix)
	sb.WriteString("📋 ")
	sb.WriteString(c.Type)

	// 为 APPLICATION 类型添加 APPID 说明
	if c.Type == "APPLICATION" {
		if appID, ok := c.Params["APPID"]; ok {
			if name, ok := appIDNames[appID]; ok {
				sb.WriteString(fmt.Sprintf(" (%s)", name))
			}
		}
	}
	sb.WriteString("\n")

	// 输出参数
	for name, value := range c.Params {
		sb.WriteString(prefix)
		sb.WriteString("  ")
		sb.WriteString(name)
		sb.WriteString(": ")
		sb.WriteString(value)
		sb.WriteString("\n")
	}

	// 递归输出子特征
	for _, sub := range c.Subs {
		formatCharacteristic(sb, &sub, indent+1)
	}
}
