package smscodec

import (
	"encoding/hex"
	"strings"
	"testing"
)

// 构造一个标准的 OMA CP WBXML 测试用例（APN 配置）
// 等价 XML：
//
//	<wap-provisioningdoc>
//	  <characteristic type="NAPDEF">
//	    <parm name="NAPID" value="internet"/>
//	    <parm name="NAP-ADDRESS" value="internet.telekom"/>
//	    <parm name="BEARER" value="GSM-GPRS"/>
//	    <parm name="NAP-ADDRTYPE" value="APN"/>
//	  </characteristic>
//	  <characteristic type="APPLICATION">
//	    <parm name="APPID" value="w4"/>
//	    <parm name="NAME" value="Telekom"/>
//	    <parm name="TO-NAPID" value="internet"/>
//	  </characteristic>
//	</wap-provisioningdoc>
func buildTestWBXML() []byte {
	var buf []byte
	// WBXML Header
	buf = append(buf, 0x03) // 版本 1.3
	buf = append(buf, 0x0B) // 公共 ID: OMA CP
	buf = append(buf, 0x6A) // 字符集: UTF-8 (106)
	buf = append(buf, 0x00) // 字符串表长度: 0

	// <wap-provisioningdoc> (tag 0x05, has content)
	buf = append(buf, 0x05|byte(wbxmlHasContent))

	// --- <characteristic type="NAPDEF"> ---
	// tag 0x06 (characteristic), has content + has attributes
	buf = append(buf, 0x06|byte(wbxmlHasContent)|byte(wbxmlHasAttrs))
	// type="NAPDEF" — 使用 ATTRSTART 0xA4 (type=NAPDEF)
	buf = append(buf, 0xA4)
	buf = append(buf, byte(wbxmlEnd)) // 结束属性列表

	// <parm name="NAPID" value="internet"/> (tag 0x07, has attributes, no content)
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x11) // name="NAPID" (ATTRSTART 0x11)
	buf = append(buf, 0x06) // value="" (ATTRSTART 0x06, 需要 STR_I 提供值)
	buf = append(buf, byte(wbxmlStrI))
	buf = append(buf, []byte("internet")...)
	buf = append(buf, 0x00)           // null 终止符
	buf = append(buf, byte(wbxmlEnd)) // 结束属性列表

	// <parm name="NAP-ADDRESS" value="internet.telekom"/>
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x08) // name="NAP-ADDRESS"
	buf = append(buf, 0x06) // value=""
	buf = append(buf, byte(wbxmlStrI))
	buf = append(buf, []byte("internet.telekom")...)
	buf = append(buf, 0x00)
	buf = append(buf, byte(wbxmlEnd))

	// <parm name="BEARER" value="GSM-GPRS"/>
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x10) // name="BEARER"
	buf = append(buf, 0x06) // value=""
	buf = append(buf, 0x73) // ATTRVALUE 0x73 = "GSM-GPRS"
	buf = append(buf, byte(wbxmlEnd))

	// <parm name="NAP-ADDRTYPE" value="APN"/>
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x09) // name="NAP-ADDRTYPE"
	buf = append(buf, 0x06) // value=""
	buf = append(buf, 0x49) // ATTRVALUE 0x49 = "APN"
	buf = append(buf, byte(wbxmlEnd))

	buf = append(buf, byte(wbxmlEnd)) // 结束 characteristic NAPDEF

	// --- <characteristic type="APPLICATION"> ---
	// 切换到 attribute code page 1
	buf = append(buf, 0x06|byte(wbxmlHasContent)|byte(wbxmlHasAttrs))
	buf = append(buf, byte(wbxmlSwitchPage), 0x01) // 切换 attr code page 到 1
	buf = append(buf, 0xA4)                        // type="APPLICATION" (code page 1 的 0xA4)
	buf = append(buf, byte(wbxmlEnd))              // 结束属性列表

	// <parm name="APPID" value="w4"/>
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x1C) // name="APPID" (code page 1)
	buf = append(buf, 0x06) // value=""
	buf = append(buf, 0x91) // ATTRVALUE 0x91 = "w4" (code page 1)
	buf = append(buf, byte(wbxmlEnd))

	// <parm name="NAME" value="Telekom"/>
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x07) // name="NAME" (code page 1)
	buf = append(buf, 0x06) // value=""
	buf = append(buf, byte(wbxmlStrI))
	buf = append(buf, []byte("Telekom")...)
	buf = append(buf, 0x00)
	buf = append(buf, byte(wbxmlEnd))

	// <parm name="TO-NAPID" value="internet"/>
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x11) // name="TO-NAPID" (code page 1)
	buf = append(buf, 0x06) // value=""
	buf = append(buf, byte(wbxmlStrI))
	buf = append(buf, []byte("internet")...)
	buf = append(buf, 0x00)
	buf = append(buf, byte(wbxmlEnd))

	buf = append(buf, byte(wbxmlEnd)) // 结束 characteristic APPLICATION

	buf = append(buf, byte(wbxmlEnd)) // 结束 wap-provisioningdoc

	return buf
}

func TestDecodeWBXMLBasic(t *testing.T) {
	data := buildTestWBXML()
	cfg, err := decodeWBXML(data)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if cfg == nil {
		t.Fatal("解码结果为 nil")
	}

	t.Logf("版本: %s", cfg.Version)
	t.Logf("特征项数量: %d", len(cfg.Characteristics))

	// 应该有一个顶层 wap-provisioningdoc，包含 2 个子 characteristic
	if len(cfg.Characteristics) == 0 {
		t.Fatal("没有解码到任何特征项")
	}

	root := cfg.Characteristics[0]
	if root.Type != "wap-provisioningdoc" {
		t.Fatalf("根元素类型应为 wap-provisioningdoc，实际: %s", root.Type)
	}

	if len(root.Subs) < 2 {
		t.Fatalf("应有至少 2 个子特征，实际: %d", len(root.Subs))
	}

	// 验证 NAPDEF
	napdef := root.Subs[0]
	if napdef.Type != "NAPDEF" {
		t.Errorf("第一个子特征应为 NAPDEF，实际: %s", napdef.Type)
	}
	if napdef.Params["NAPID"] != "internet" {
		t.Errorf("NAPID 应为 internet，实际: %q", napdef.Params["NAPID"])
	}
	if napdef.Params["NAP-ADDRESS"] != "internet.telekom" {
		t.Errorf("NAP-ADDRESS 应为 internet.telekom，实际: %q", napdef.Params["NAP-ADDRESS"])
	}
	if napdef.Params["BEARER"] != "GSM-GPRS" {
		t.Errorf("BEARER 应为 GSM-GPRS，实际: %q", napdef.Params["BEARER"])
	}
	if napdef.Params["NAP-ADDRTYPE"] != "APN" {
		t.Errorf("NAP-ADDRTYPE 应为 APN，实际: %q", napdef.Params["NAP-ADDRTYPE"])
	}

	// 验证 APPLICATION
	app := root.Subs[1]
	if app.Type != "APPLICATION" {
		t.Errorf("第二个子特征应为 APPLICATION，实际: %s", app.Type)
	}
	if app.Params["APPID"] != "w4" {
		t.Errorf("APPID 应为 w4，实际: %q", app.Params["APPID"])
	}
	if app.Params["NAME"] != "Telekom" {
		t.Errorf("NAME 应为 Telekom，实际: %q", app.Params["NAME"])
	}
}

func TestDecodeOmaCPFromTPDU_WithWSPHeader(t *testing.T) {
	// 模拟带 WSP Push header 的数据：[header_len=1] [content_type=0xB0] [WBXML...]
	wbxml := buildTestWBXML()
	// WSP Push header: headers_len=1, content-type=0xB0 (application/vnd.wap.connectivity-wbxml)
	data := append([]byte{0x01, 0xB0}, wbxml...)

	cfg, err := DecodeOmaCPFromTPDU(data)
	if err != nil {
		t.Fatalf("带 WSP header 的 OMA CP 解码失败: %v", err)
	}
	if cfg == nil {
		t.Fatal("解码结果为 nil")
	}
	t.Logf("带 WSP header 解码成功，特征项: %d", len(cfg.Characteristics))
}

func TestDecodeOmaCPFromTPDU_WithLongWSPHeader(t *testing.T) {
	wbxml := buildTestWBXML()
	// 模拟较长 WSP 头，验证 WBXML 起始定位不依赖前 16 字节窗口。
	longWSPHeader := make([]byte, 24)
	for i := range longWSPHeader {
		longWSPHeader[i] = byte(i + 1)
	}
	data := append(longWSPHeader, wbxml...)

	cfg, err := DecodeOmaCPFromTPDU(data)
	if err != nil {
		t.Fatalf("长 WSP header 的 OMA CP 解码失败: %v", err)
	}
	if cfg == nil || len(cfg.Characteristics) == 0 {
		t.Fatal("解码结果为空")
	}
}

func TestDecodeOmaCPFromTPDU_EncryptedFallback(t *testing.T) {
	// 模拟加密的 OMA CP 数据（不含有效 WBXML header）
	data, _ := hex.DecodeString("0048150e221515b00011a8e388e4673a650e13c5cc6d22e1e78eb152d2ef139c27513b6abe5b130adb9b43f8e78eb863e07c27b195cf4571505558a485d9c3dc0c68a5cea1c0b35845c5")
	_, err := DecodeOmaCPFromTPDU(data)
	if err == nil {
		t.Fatal("加密数据应返回错误")
	}
	if !strings.Contains(err.Error(), "加密") {
		t.Errorf("错误信息应提及加密，实际: %s", err.Error())
	}
	t.Logf("加密数据正确返回错误: %v", err)
}

func TestFormatOmaCPSummary(t *testing.T) {
	data := buildTestWBXML()
	cfg, err := decodeWBXML(data)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}

	summary := FormatOmaCPSummary(cfg)
	t.Logf("摘要输出:\n%s", summary)

	// 验证摘要中包含关键信息
	checks := []string{
		"NAPDEF",
		"internet.telekom",
		"GSM-GPRS",
		"APN",
		"APPLICATION",
		"浏览器书签", // w4 的中文名
		"Telekom",
	}
	for _, check := range checks {
		if !strings.Contains(summary, check) {
			t.Errorf("摘要中应包含 %q", check)
		}
	}
}

func TestIsWBXMLHeader(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		expect bool
	}{
		{"有效 OMA CP", []byte{0x03, 0x0B, 0x6A, 0x00}, true},
		{"太短", []byte{0x03}, false},
		{"版本错误", []byte{0x04, 0x0B, 0x6A, 0x00}, false},
		{"公共 ID 错误", []byte{0x03, 0x0C, 0x6A, 0x00}, false},
		{"全零", []byte{0x00, 0x00, 0x00, 0x00}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWBXMLHeader(tt.data)
			if got != tt.expect {
				t.Errorf("isWBXMLHeader(%X) = %v, 预期 %v", tt.data, got, tt.expect)
			}
		})
	}
}

func TestIsWBXMLHeader_MultiBytePublicID(t *testing.T) {
	// publicID 0x0B 的非最短 mb_uint32 编码：0x80 0x0B
	data := []byte{0x03, 0x80, 0x0B, 0x6A, 0x00}
	if !isWBXMLHeader(data) {
		t.Fatalf("多字节 publicID 编码应被识别为合法 WBXML 头: %X", data)
	}
}

func TestDecodeWBXML_AttrValueTokenAmbiguityAndEmptyParmName(t *testing.T) {
	var buf []byte
	// WBXML Header
	buf = append(buf, 0x03, 0x0B, 0x6A, 0x00)
	// <wap-provisioningdoc>
	buf = append(buf, 0x05|byte(wbxmlHasContent))
	// <characteristic type="NAPDEF">
	buf = append(buf, 0x06|byte(wbxmlHasContent)|byte(wbxmlHasAttrs))
	buf = append(buf, 0xA4, byte(wbxmlEnd))

	// <parm name="NAME" value="NAPDEF"/>，value token 0xA4 同时也可能被误解释为 ATTRSTART
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x07) // name="NAME"
	buf = append(buf, 0x06) // value=""
	buf = append(buf, 0xA4) // ATTRVALUE -> "NAPDEF"
	buf = append(buf, byte(wbxmlEnd))

	// <parm value="orphan"/>（无 name），应被忽略，不写入空 key
	buf = append(buf, 0x07|byte(wbxmlHasAttrs))
	buf = append(buf, 0x06) // value=""
	buf = append(buf, byte(wbxmlStrI))
	buf = append(buf, []byte("orphan")...)
	buf = append(buf, 0x00)
	buf = append(buf, byte(wbxmlEnd))

	// 结束 characteristic / doc
	buf = append(buf, byte(wbxmlEnd), byte(wbxmlEnd))

	cfg, err := decodeWBXML(buf)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if len(cfg.Characteristics) == 0 || cfg.Characteristics[0].Type != "wap-provisioningdoc" || len(cfg.Characteristics[0].Subs) == 0 {
		t.Fatalf("解码结构异常: %+v", cfg)
	}

	ch := cfg.Characteristics[0].Subs[0]
	if got := ch.Params["NAME"]; got != "NAPDEF" {
		t.Fatalf("NAME 参数解析错误: got=%q want=%q", got, "NAPDEF")
	}
	if _, ok := ch.Params[""]; ok {
		t.Fatalf("不应出现空参数名 key，params=%v", ch.Params)
	}
}
