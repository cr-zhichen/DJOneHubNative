// Package callnumber validates the dial strings shared by the app and CLI.
package callnumber

import (
	"errors"
	"strings"
)

type DialString struct {
	Original string
	Number   string
	PostDial string
}

// Parse keeps pauses out of ATD. Each comma in PostDial is a two-second pause
// after the call is connected; only DTMF digits may follow the first comma.
func Parse(value string) (DialString, error) {
	value = strings.TrimSpace(value)
	number, suffix, hasPause := strings.Cut(value, ",")
	digits := strings.TrimPrefix(number, "+")
	if len(number) > 32 || digits == "" || strings.Trim(digits, "0123456789*#") != "" {
		return DialString{}, errors.New("主号码只能包含数字、开头的 +、* 和 #，且最长 32 位")
	}
	postDial := ""
	if hasPause {
		postDial = "," + suffix
		if len(postDial) > 64 || strings.Trim(suffix, "0123456789*#,") != "" || strings.Trim(suffix, ",") == "" {
			return DialString{}, errors.New("逗号后请输入分机按键（数字、*、#）；每个逗号暂停 2 秒，后缀最长 64 位")
		}
	}
	return DialString{Original: value, Number: number, PostDial: postDial}, nil
}
