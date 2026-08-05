package smscodec

import (
	"strings"
	"testing"

	"github.com/warthog618/sms/encoding/tpdu"
)

func TestParseUDHPortsSupports16And8(t *testing.T) {
	udh := tpdu.UserDataHeader{
		{ID: 0x04, Data: []byte{0x23, 0xF0}},
		{ID: 0x05, Data: []byte{0x0B, 0x84, 0x23, 0xF0}},
	}
	ports := parseUDHPorts(udh)
	if !ports.Has16Bit || ports.DestPort16 != 2948 || ports.SrcPort16 != 9200 {
		t.Fatalf("16-bit 端口解析错误: %+v", ports)
	}
	if !ports.Has8Bit || ports.DestPort8 != 0x23 || ports.SrcPort8 != 0xF0 {
		t.Fatalf("8-bit 端口解析错误: %+v", ports)
	}
	gotPort, ok := ports.preferredDestPort()
	if !ok || gotPort != 2948 {
		t.Fatalf("preferredDestPort 错误: ok=%v port=%d", ok, gotPort)
	}
}

func TestParseWSPPushContentTypeToken(t *testing.T) {
	// [TID][PDU Type][HeadersLen=1][Content-Type token=0xB0][WBXML...]
	data := append([]byte{0x01, 0x06, 0x01, 0xB0}, buildTestWBXML()...)
	wsp := parseWSPPush(data)
	if !wsp.Ok {
		t.Fatal("应识别为有效 WSP Push")
	}
	if wsp.ContentType != "application/vnd.wap.connectivity-wbxml" {
		t.Fatalf("content-type 解析错误: %q", wsp.ContentType)
	}
	if len(wsp.Body) == 0 {
		t.Fatal("body 不应为空")
	}
}

func TestClassifyBinarySMS_OmaCPByPort(t *testing.T) {
	msg := buildTestWBXML()
	tp := &tpdu.TPDU{
		UDH: tpdu.UserDataHeader{
			{ID: 0x05, Data: []byte{0x0B, 0x84, 0x23, 0xF0}},
		},
	}
	c := classifyBinarySMS(tp, msg)
	if c.Kind != binaryKindOmaCP {
		t.Fatalf("应识别为 OMA CP，实际: %s", c.Kind)
	}
	out := formatBinaryClassification(c)
	if !strings.Contains(out, "[OMA CP 运营商配置短信]") {
		t.Fatalf("输出缺少 OMA CP 标签: %s", out)
	}
	if !strings.Contains(out, "raw=") {
		t.Fatalf("输出缺少 raw 字段: %s", out)
	}
}

func TestClassifyBinarySMS_WAPSI(t *testing.T) {
	// WSP header 使用文本 content-type，避免依赖 token 映射。
	ct := "application/vnd.wap.sic\x00"
	body := []byte("title\x00https://example.com/si")
	msg := append([]byte{0x21, 0x06, byte(len(ct))}, []byte(ct)...)
	msg = append(msg, body...)

	c := classifyBinarySMS(&tpdu.TPDU{}, msg)
	if c.Kind != binaryKindWAPSI {
		t.Fatalf("应识别为 WAP SI，实际: %s", c.Kind)
	}
	out := formatBinaryClassification(c)
	if !strings.Contains(out, "url=https://example.com/si") {
		t.Fatalf("应提取 URL，实际: %s", out)
	}
}

func TestClassifyBinarySMS_MMSNotification(t *testing.T) {
	ct := "application/vnd.wap.mms-message\x00"
	body := []byte{
		0x8C, 0x82, // X-Mms-Message-Type: m-notification-ind
		0x98, 't', 'x', '1', 0x00, // X-Mms-Transaction-ID
		0x83, 'h', 't', 't', 'p', ':', '/', '/', 'm', '.', 'm', 's', '/', 'n', 0x00, // Content-Location
		0x8E, 0x81, 0x48, // Message-Size (mb_uint32)
	}
	msg := append([]byte{0x31, 0x06, byte(len(ct))}, []byte(ct)...)
	msg = append(msg, body...)

	c := classifyBinarySMS(&tpdu.TPDU{}, msg)
	if c.Kind != binaryKindMMSNotification {
		t.Fatalf("应识别为 MMS Notification，实际: %s", c.Kind)
	}
	out := formatBinaryClassification(c)
	if !strings.Contains(out, "x_mms_transaction_id=tx1") {
		t.Fatalf("应提取 transaction id，实际: %s", out)
	}
	if !strings.Contains(out, "message_size=") {
		t.Fatalf("应提取 message size，实际: %s", out)
	}
}

func TestClassifyBinarySMS_SIMOTA(t *testing.T) {
	// CPL CHL SPI KIC KID ...
	msg := []byte{0x00, 0x05, 0x11, 0x22, 0x33, 0x44, 0x55}
	tp := &tpdu.TPDU{PID: 0x7F}
	c := classifyBinarySMS(tp, msg)
	if c.Kind != binaryKindSIMOTA {
		t.Fatalf("应识别为 SIM OTA，实际: %s", c.Kind)
	}
	out := formatBinaryClassification(c)
	if !strings.Contains(out, "[SIM OTA 23.048]") {
		t.Fatalf("缺少 SIM OTA 标签: %s", out)
	}
	if !strings.Contains(out, "spi=0x1122") {
		t.Fatalf("缺少 SPI 提示: %s", out)
	}
}

func TestClassifyBinarySMS_UnknownFallback(t *testing.T) {
	msg := []byte{0xde, 0xad, 0xbe, 0xef}
	c := classifyBinarySMS(&tpdu.TPDU{}, msg)
	if c.Kind != binaryKindUnknown {
		t.Fatalf("应回退 unknown，实际: %s", c.Kind)
	}
	out := formatBinaryClassification(c)
	if !strings.Contains(out, "[二进制数据]") || !strings.Contains(out, "raw=deadbeef") {
		t.Fatalf("unknown 输出不符合预期: %s", out)
	}
}
