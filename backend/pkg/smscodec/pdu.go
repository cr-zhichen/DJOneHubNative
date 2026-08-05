package smscodec

import (
	"errors"
	"fmt"
	"strings"
	"time"

	smspdu "github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"
	"github.com/warthog618/sms/encoding/ucs2"
)

type SMSEncoding string

const (
	SMSEncodingAuto SMSEncoding = "auto"
	SMSEncodingUCS2 SMSEncoding = "ucs2"
)

type SubmitOptions struct {
	Encoding SMSEncoding
}

func NormalizeSMSEncoding(raw string) (SMSEncoding, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(SMSEncodingAuto):
		return SMSEncodingAuto, nil
	case string(SMSEncodingUCS2):
		return SMSEncodingUCS2, nil
	default:
		return "", fmt.Errorf("unsupported SMS encoding: %s", raw)
	}
}

// IsHexString 判断字符串是否为偶数长度的十六进制编码。
func IsHexString(s string) bool {
	if len(s) < 2 || len(s)%2 != 0 {
		return false
	}
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

// ConcatInfo 长短信分片信息（UDH concatenation header）
type ConcatInfo struct {
	IsConcat bool // 是否为多段短信
	Ref      int  // 引用号（同一条长短信的所有分片共享此值）
	RefBits  int  // 引用号位宽：8 或 16
	Total    int  // 总分片数
	Seq      int  // 当前序号 (1-based)
}

// DecodeDeliverTPDU 解码下行短信 TPDU，返回发送方号码、文本内容、发送时间、和 concat 分片信息。
// 如果 TPDU 包含 UDH concatenation header（长短信分片），concat.IsConcat 为 true。
func DecodeDeliverTPDU(tpduBytes []byte) (sender string, text string, ts time.Time, concat ConcatInfo, err error) {
	if trimmed, ok := TrimDeliverTPDUToDeclaredLength(tpduBytes); ok {
		tpduBytes = trimmed
	}
	if normalized, ok := normalizeDeliverTPDUGSM7SpareBits(tpduBytes); ok {
		tpduBytes = normalized
	}
	t, err := smspdu.Unmarshal(tpduBytes)
	if err != nil {
		return "", "", time.Time{}, ConcatInfo{}, err
	}
	msg, err := smspdu.Decode([]*tpdu.TPDU{t})
	if err != nil {
		return "", "", time.Time{}, ConcatInfo{}, err
	}
	// 检测 UDH 中的 concatenation 信息（长短信分片标识）
	if t.UDH != nil {
		if segments, seqno, mref, ok := t.UDH.ConcatInfo8(); ok && segments > 1 {
			concat = ConcatInfo{IsConcat: true, Ref: mref, RefBits: 8, Total: segments, Seq: seqno}
		} else if segments, seqno, mref, ok := t.UDH.ConcatInfo16(); ok && segments > 1 {
			concat = ConcatInfo{IsConcat: true, Ref: mref, RefBits: 16, Total: segments, Seq: seqno}
		}
	}

	// 检查是否为二进制数据 (比如针对 SIM 卡的 OTA / Class 2 消息)，直接强转会破坏编码导致 webhook 报错
	textStr := string(msg)
	alpha, aErr := t.DCS.Alphabet()
	if aErr == nil && alpha == tpdu.Alpha8Bit {
		classified := classifyBinarySMS(t, msg)
		textStr = formatBinaryClassification(classified)
	}

	// 最终安全保障：滤除任何非法的非 UTF-8 截断内容，防止下游 JSON 序列化崩溃
	textStr = strings.ToValidUTF8(textStr, "")

	if t.SmsType() == tpdu.SmsDeliver {
		return t.OA.Number(), textStr, t.SCTS.Time, concat, nil
	}
	return "", textStr, time.Time{}, concat, nil
}

// IsShortCode 判断号码是否为运营商短号码/服务号码（非标准手机号）
// 短号码特征：无 + 前缀、长度 <= 6 位、纯数字
func IsShortCode(phone string) bool {
	if strings.HasPrefix(phone, "+") {
		return false
	}
	digits := strings.TrimLeft(phone, "0123456789")
	return digits == "" && len(phone) <= 6
}

// BuildSubmitTPDUsWithOptions 编码上行短信为一组 SUBMIT TPDU，并允许调用方指定文本编码策略。
func BuildSubmitTPDUsWithOptions(to, text string, opts SubmitOptions) ([][]byte, []int, error) {
	normalizedTo := strings.TrimSpace(to)
	encoding, err := NormalizeSMSEncoding(string(opts.Encoding))
	if err != nil {
		return nil, nil, err
	}

	msg := []byte(text)
	encoderOptions := []smspdu.EncoderOption{smspdu.To(normalizedTo)}
	if encoding == SMSEncodingUCS2 {
		msg = ucs2.Encode([]rune(text))
		encoderOptions = append(encoderOptions, smspdu.AsUCS2)
	}

	tpdus, err := smspdu.Encode(msg, encoderOptions...)
	if err != nil {
		return nil, nil, err
	}
	if len(tpdus) == 0 {
		return nil, nil, errors.New("TPDU 编码结果为空")
	}

	var bytesList [][]byte
	var lenList []int

	for _, pdu := range tpdus {
		// 修复短号码地址类型：库默认将所有号码设为 TonInternational (0x91)，
		// 但运营商短号码（如 888、10086）应使用 TonUnknown (0x81)
		if IsShortCode(normalizedTo) {
			da := pdu.DA
			da.SetTypeOfNumber(tpdu.TonUnknown)
			da.SetNumberingPlan(tpdu.NpISDN)
			pdu.DA = da
		}

		b, err := pdu.MarshalBinary()
		if err != nil {
			return nil, nil, err
		}
		bytesList = append(bytesList, b)
		lenList = append(lenList, len(b))
	}

	return bytesList, lenList, nil
}
