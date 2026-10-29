package main

import "testing"

func TestExtractVerificationDigitsNormalizesChineseSpokenDigits(t *testing.T) {
	cases := map[string]string{
		"验证码是 一 二 三 四，请输入": "1234",
		"您的验证码为幺伍捌零":       "1580",
		"验证码：壹贰叁肆伍陆柒捌玖":    "123456789",
		"code 9072": "9072",
	}
	for input, want := range cases {
		if got := extractVerificationDigits(input); got != want {
			t.Errorf("extractVerificationDigits(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestExtractVerificationDigitsAcceptsCommonASRHomophone(t *testing.T) {
	if got := extractVerificationDigits("见证马为 123456。"); got != "123456" {
		t.Fatalf("extractVerificationDigits(common ASR homophone)=%q, want %q", got, "123456")
	}
}

func TestExtractVerificationDigitsRejectsShortOrUnrelatedText(t *testing.T) {
	for _, input := range []string{"请稍候", "数字 12", "订单 2026 年 10 月"} {
		if got := extractVerificationDigits(input); got != "" {
			t.Errorf("extractVerificationDigits(%q)=%q, want empty", input, got)
		}
	}
}
