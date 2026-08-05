package modem

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// 各种 SIM 卡基本数据文件 (EF) 的文件 ID 常量
const (
	efIMSI = 0x6F07 // IMSI
	efAD   = 0x6FAD // Administrative Data, byte 4 carries MNC length
	efGID1 = 0x6F3E // Group Identifier 1
	efGID2 = 0x6F3F // Group Identifier 2
	efPNN  = 0x6FC5 // PLMN Network Name
	efOPL  = 0x6FC6 // Operator PLMN List
	efSST  = 0x6F38 // SIM Service Table (GSM)
	efUST  = 0x6F38 // USIM Service Table (3G/4G/5G)
)

const (
	PLMNSourceIMSI_EFAD     = "imsi_efad"
	PLMNSourceIMSIHeuristic = "imsi_heuristic"
)

// SIMMetadata 封装了解析出来的 SIM 卡元数据，用于运营商特征识别。
type SIMMetadata struct {
	NativeMCC       string           // SIM 卡内置的移动国家代码
	NativeMNC       string           // SIM 卡内置的移动网络代码
	GID1            string           // 组标识符 1 的十六进制字符串
	GID2            string           // 组标识符 2 的十六进制字符串
	PNN             []PNNRecord      // PLMN 名字记录列表
	OPL             []OPLRecord      // 运营商 PLMN 与小区 LAC 关联记录列表
	SIMServiceTable *SIMServiceTable // 包含该卡片使能的增值服务表
}

func MNCLengthFromEFAD(efAD []byte) (int, bool) {
	if len(efAD) < 4 {
		return 0, false
	}
	switch efAD[3] {
	case 0x02, 0x03:
		return int(efAD[3]), true
	default:
		return 0, false
	}
}

func HomeMCCMNCFromIMSIAndEFAD(imsi string, efAD []byte) (mcc, mnc string, mncLen int, source string, err error) {
	imsi = strings.TrimSpace(imsi)
	if len(imsi) < 5 {
		return "", "", 0, "", fmt.Errorf("IMSI 长度不足")
	}
	if resolvedLen, ok := MNCLengthFromEFAD(efAD); ok {
		if len(imsi) < 3+resolvedLen {
			return "", "", 0, "", fmt.Errorf("IMSI 与 EF-AD MNC 长度不匹配")
		}
		return imsi[:3], imsi[3 : 3+resolvedLen], resolvedLen, PLMNSourceIMSI_EFAD, nil
	}

	mcc, mnc = parseMCCMNCFromIMSI(imsi)
	if mcc == "" || mnc == "" {
		return "", "", 0, "", fmt.Errorf("无法从 IMSI 解析 MCC/MNC")
	}
	return mcc, mnc, len(mnc), PLMNSourceIMSIHeuristic, nil
}

func parseMCCMNCFromIMSI(imsi string) (mcc, mnc string) {
	imsi = strings.TrimSpace(imsi)
	if len(imsi) < 5 {
		return "", ""
	}
	mcc = imsi[:3]
	mncLen := 2
	switch mcc {
	case "302", "308":
		mncLen = 3
	case "310", "311", "312", "313", "314", "315", "316", "332", "318", "319", "334", "350":
		mncLen = 3
	case "338", "348", "342", "344", "346", "354", "356", "358", "360", "362", "364", "365", "366", "368", "370", "372", "374", "376":
		mncLen = 3
	case "405", "406":
		mncLen = 3
	case "716", "722", "730", "732", "736", "740", "744", "746", "748", "750":
		mncLen = 3
	}
	if len(imsi) < 3+mncLen {
		return mcc, imsi[3:]
	}
	return mcc, imsi[3 : 3+mncLen]
}

// trimSIMPadding 辅助去除 SIM 二进制块末尾的无效填充字符 (0xFF 或 0x00)
func trimSIMPadding(data []byte) []byte {
	end := len(data)
	for end > 0 && (data[end-1] == 0xFF || data[end-1] == 0x00) {
		end--
	}
	return data[:end]
}

// simRawHex 将数据去除填充并转换为大写十六进制字符串
func simRawHex(data []byte) string {
	data = trimSIMPadding(data)
	if len(data) == 0 {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(data))
}

// DecodePNNRecord 解码 EF_PNN (PLMN Network Name) 单条 TLV 格式的记录内容。
// tag 0x43 对应网络全名，tag 0x45 对应网络缩写
func DecodePNNRecord(record int, data []byte) (PNNRecord, bool) {
	data = trimPNNTLVRecord(data)
	raw := simRawHex(data)
	if len(data) == 0 {
		return PNNRecord{}, false
	}
	out := PNNRecord{Record: record, RawHex: raw}
	for i := 0; i+2 <= len(data); {
		tag := data[i]
		length := int(data[i+1])
		i += 2
		if i+length > len(data) {
			break
		}
		value := data[i : i+length]
		i += length
		switch tag {
		case 0x43:
			if name, err := decodePNNNetworkName(value); err == nil {
				out.FullName = name
			}
		case 0x45:
			if name, err := decodePNNNetworkName(value); err == nil {
				out.ShortName = name
			}
		}
	}
	return out, out.FullName != "" || out.ShortName != "" || out.RawHex != ""
}

// trimPNNTLVRecord 获取并裁剪有效的 TLV 记录边界，防止数据越界
func trimPNNTLVRecord(data []byte) []byte {
	length := pnnTLVLength(data)
	if length == 0 {
		return nil
	}
	return data[:length]
}

// pnnTLVLength 探测 PNN 中 TLV 字段的物理总长度
func pnnTLVLength(data []byte) int {
	data = trimSIMPadding(data)
	end := 0
	for i := 0; i+2 <= len(data); {
		tag := data[i]
		if tag == 0x00 || tag == 0xFF {
			break
		}
		if tag != 0x43 && tag != 0x45 {
			break
		}
		length := int(data[i+1])
		if length == 0 || i+2+length > len(data) {
			break
		}
		i += 2 + length
		end = i
	}
	return end
}

// DecodeOPLRecord 解码单条 EF_OPL (Operator PLMN List) 定长数据记录
func DecodeOPLRecord(record int, data []byte) (OPLRecord, bool) {
	raw := simRawHex(data)
	data = trimSIMPadding(data)
	if len(data) < 8 {
		return OPLRecord{}, false
	}
	out := OPLRecord{
		Record:    record,
		PLMN:      decodeOPLPLMN(data[:3]),
		LACStart:  binary.BigEndian.Uint16(data[3:5]),
		LACEnd:    binary.BigEndian.Uint16(data[5:7]),
		PNNRecord: int(data[7]),
		RawHex:    raw,
	}
	return out, out.PLMN != "" || out.RawHex != ""
}

// NativeMCCMNCFromOPLRecords 从已解码的 OPL 列表中提取第一条有效的运营商 HPLMN (MCC + MNC)
func NativeMCCMNCFromOPLRecords(records []OPLRecord) (mcc string, mnc string, ok bool) {
	for _, rec := range records {
		plmn := strings.TrimSpace(rec.PLMN)
		if len(plmn) != 5 && len(plmn) != 6 {
			continue
		}
		if !isExactDecimalPLMN(plmn) {
			continue
		}
		return plmn[:3], plmn[3:], true
	}
	return "", "", false
}

// isExactDecimalPLMN 判断 PLMN 是否纯粹由 0-9 数字字符串构成
func isExactDecimalPLMN(plmn string) bool {
	for i := 0; i < len(plmn); i++ {
		if plmn[i] < '0' || plmn[i] > '9' {
			return false
		}
	}
	return true
}

// DecodeSIMServiceTable 解密并解码 SIM/USIM 服务使能表数据，解析哪些特性编号被使能（按字节位 1-based 计算位置）
func DecodeSIMServiceTable(kind string, data []byte) *SIMServiceTable {
	raw := simRawHex(data)
	data = trimSIMPadding(data)
	if len(data) == 0 {
		return nil
	}
	enabled := make([]int, 0)
	for i, b := range data {
		for bit := 0; bit < 8; bit++ {
			if b&(1<<bit) != 0 {
				enabled = append(enabled, i*8+bit+1)
			}
		}
	}
	return &SIMServiceTable{Kind: kind, RawHex: raw, EnabledServices: enabled}
}

// decodeSIMAlphaIdentifier 包含解码常见各种字符标志符（如 SPN，APN 等）的复合解码函数，支持 GSM, UCS2, ASCII 格式
func decodeSIMAlphaIdentifier(data []byte) (string, error) {
	data = trimSIMPadding(data)
	if len(data) == 0 {
		return "", fmt.Errorf("SIM alpha identifier empty")
	}
	switch data[0] {
	case 0x80:
		return decodeSPNUCS2(data[1:])
	case 0x81:
		return decodeSPNCompressedUCS2(data, 1)
	case 0x82:
		return decodeSPNCompressedUCS2(data, 2)
	default:
		if isPrintableASCII(data) {
			return strings.TrimSpace(string(data)), nil
		}
		return decodeSPNGSM(data)
	}
}

// decodePNNNetworkName 根据 3GPP TS 31.102 规范解码 PNN 中的网络名称字符串，支持 GSM7 压缩格式与 UCS2 格式
func decodePNNNetworkName(data []byte) (string, error) {
	data = trimSIMPadding(data)
	if len(data) == 0 {
		return "", fmt.Errorf("PNN network name empty")
	}
	if len(data) >= 2 {
		info := data[0]
		coding := (info >> 4) & 0x07
		payload := data[1:]
		switch coding {
		case 0:
			// coding = 000 表示 GSM 7-bit 压缩格式，同时备用位通过低3位传递
			return decodePackedGSM7(payload, int(info&0x07))
		case 1:
			// coding = 001 表示标准双字节 UCS2
			return decodeSPNUCS2(payload)
		}
	}
	return decodeSIMAlphaIdentifier(data)
}

// decodePackedGSM7 解密 GSM 7-bit 压缩排列的 Septets 并还原成普通 UTF-8 字符串
func decodePackedGSM7(data []byte, spareBits int) (string, error) {
	if spareBits < 0 || spareBits > 7 {
		spareBits = 0
	}
	septets := (len(data)*8 - spareBits) / 7
	if septets <= 0 {
		return "", fmt.Errorf("GSM7 payload empty")
	}
	unpacked := make([]byte, 0, septets)
	buf := 0
	bits := 0
	for _, b := range data {
		buf |= int(b) << bits
		bits += 8
		for bits >= 7 && len(unpacked) < septets {
			unpacked = append(unpacked, byte(buf&0x7F))
			buf >>= 7
			bits -= 7
		}
	}
	decoded, err := decodeSPNGSM(unpacked)
	if err != nil {
		return "", err
	}
	decoded = strings.TrimSpace(strings.ReplaceAll(decoded, "\x00", ""))
	if decoded == "" {
		return "", fmt.Errorf("GSM7 network name empty")
	}
	return decoded, nil
}

// decodeOPLPLMN 将 OPL 记录中保存的前 3 字节的 BCD 格式 PLMN 转换为 "46000" 或 "46001" 等十进制字符串（若第3个半字节为F即表示5位格式的卡片）
func decodeOPLPLMN(data []byte) string {
	if len(data) < 3 {
		return ""
	}
	nibbles := []byte{data[0] & 0x0F, data[0] >> 4, data[1] & 0x0F, data[2] & 0x0F, data[2] >> 4, data[1] >> 4}
	var b strings.Builder
	for _, n := range nibbles {
		if n == 0x0F {
			b.WriteByte('x')
			continue
		}
		if n > 9 {
			return ""
		}
		b.WriteByte('0' + n)
	}
	return strings.TrimRight(b.String(), "x")
}
