package modem

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/warthog618/sms/encoding/gsm7"
)

// DecodeEFSPN 解密并解码来自 SIM 卡 EF_SPN (Elementary File - Service Provider Name) 的原始二进制数据。
// 该文件结构可能包含 UCS2、压缩 UCS2、ASCII 或 GSM 7-bit 格式的服务提供商名称。
func DecodeEFSPN(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("EF_SPN data empty")
	}
	name := data
	if len(name) > 1 {
		name = name[1:] // 跳过第一个字节（指示在 HPLMN/RPLMN 下的显示要求）
	}
	name = trimSPNPadding(name)
	if len(name) == 0 {
		return "", fmt.Errorf("EF_SPN name empty")
	}

	var (
		decoded string
		err     error
	)
	switch name[0] {
	case 0x80:
		// 0x80 表示标准的 16-bit UCS2 编码方式
		decoded, err = decodeSPNUCS2(name[1:])
	case 0x81:
		// 0x81 表示带 1 字节基准的压缩 UCS2 编码方式
		decoded, err = decodeSPNCompressedUCS2(name, 1)
	case 0x82:
		// 0x82 表示带 2 字节基准的压缩 UCS2 编码方式
		decoded, err = decodeSPNCompressedUCS2(name, 2)
	default:
		// 默认检测，若是可打印 ASCII 字符集则直接转换，否则走 GSM 7-bit 编码解析
		if isPrintableASCII(name) {
			decoded = string(name)
		} else {
			decoded, err = decodeSPNGSM(name)
		}
	}
	if err != nil {
		return "", err
	}
	decoded = strings.TrimSpace(strings.ReplaceAll(decoded, "\x00", ""))
	if decoded == "" {
		return "", fmt.Errorf("EF_SPN name empty")
	}
	return decoded, nil
}

// trimSPNPadding 去除 SIM 卡记录中填充的无效尾部字符（如常用的 0xFF 和 0x00）
func trimSPNPadding(data []byte) []byte {
	end := len(data)
	for end > 0 && (data[end-1] == 0xFF || data[end-1] == 0x00) {
		end--
	}
	return data[:end]
}

// decodeSPNUCS2 解码标准的 Big-Endian 16-bit UCS2 文本
func decodeSPNUCS2(data []byte) (string, error) {
	data = trimSPNPadding(data)
	if len(data) == 0 {
		return "", fmt.Errorf("UCS2 SPN empty")
	}
	if len(data)%2 != 0 {
		return "", fmt.Errorf("UCS2 SPN odd length: %d", len(data))
	}
	runes := make([]uint16, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		runes = append(runes, binary.BigEndian.Uint16(data[i:i+2]))
	}
	return string(utf16.Decode(runes)), nil
}

// decodeSPNCompressedUCS2 根据基准页解压并解码 8-bit 指针压缩的 UCS2 数据（符合 TS 31.101 技术规范）
func decodeSPNCompressedUCS2(data []byte, baseBytes int) (string, error) {
	if len(data) < 2+baseBytes {
		return "", fmt.Errorf("compressed UCS2 SPN too short")
	}
	count := int(data[1]) // 第 2 个字节记录待解码的字符数量
	payloadStart := 2 + baseBytes
	if len(data) < payloadStart {
		return "", fmt.Errorf("compressed UCS2 SPN missing payload")
	}
	payload := data[payloadStart:]
	if count < len(payload) {
		payload = payload[:count]
	}

	var base uint16
	if baseBytes == 1 {
		base = uint16(data[2]) << 7
	} else {
		base = binary.BigEndian.Uint16(data[2:4])
	}

	var b strings.Builder
	for _, c := range payload {
		if c < 0x80 {
			// 小于 0x80 的直接作为标准的 GSM 7-bit 字符编码解析
			decoded, err := decodeSPNGSM([]byte{c})
			if err != nil {
				return "", err
			}
			b.WriteString(decoded)
			continue
		}
		// 大于等于 0x80 的通过基准偏移还原为双字节 UCS2 字符
		b.WriteRune(rune(base + uint16(c&0x7F)))
	}
	return b.String(), nil
}

// decodeSPNGSM 解码 GSM 7-bit 缺省字符集编码的文本并验证生成的 UTF-8 是否合法
func decodeSPNGSM(data []byte) (string, error) {
	decoded, err := gsm7.Decode(data)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(decoded) {
		return "", fmt.Errorf("GSM SPN decoded invalid UTF-8")
	}
	return string(decoded), nil
}

// isPrintableASCII 检查数据是否全是可打印的 ASCII 字符 (0x20 到 0x7E)
func isPrintableASCII(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	for _, b := range data {
		if b < 0x20 || b > 0x7E {
			return false
		}
	}
	return true
}
