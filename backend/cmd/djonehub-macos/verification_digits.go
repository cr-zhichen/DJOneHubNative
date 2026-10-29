package main

import (
	"strings"
	"unicode"
)

var spokenDigit = map[rune]rune{
	'零': '0', '〇': '0', '洞': '0',
	'一': '1', '壹': '1', '幺': '1', '要': '1',
	'二': '2', '贰': '2', '两': '2',
	'三': '3', '叁': '3',
	'四': '4', '肆': '4',
	'五': '5', '伍': '5',
	'六': '6', '陆': '6',
	'七': '7', '柒': '7',
	'八': '8', '捌': '8',
	'九': '9', '玖': '9',
}

func extractVerificationDigits(text string) string {
	lower := strings.ToLower(text)
	verificationKeyword := strings.Contains(lower, "验证码") || strings.Contains(lower, "见证码") ||
		strings.Contains(lower, "见证马") || strings.Contains(lower, "code")
	if !verificationKeyword {
		return ""
	}

	var candidates []string
	current := strings.Builder{}
	flush := func() {
		if n := current.Len(); n >= 3 && n <= 12 {
			candidates = append(candidates, current.String())
		}
		current.Reset()
	}
	for _, r := range text {
		if r >= '0' && r <= '9' {
			current.WriteRune(r)
			continue
		}
		if d, ok := spokenDigit[r]; ok {
			current.WriteRune(d)
			continue
		}
		if unicode.IsSpace(r) || strings.ContainsRune("-—·", r) {
			continue
		}
		flush()
	}
	flush()
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}
