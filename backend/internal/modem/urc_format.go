package modem

import (
	"fmt"
	"strings"
)

type urcLogLevel int

const (
	urcLogDebug urcLogLevel = iota
	urcLogInfo
	urcLogWarn
)

type urcFormatResult struct {
	Level       urcLogLevel
	Key         string
	Msg         string
	Fields      []any
	CMTIIndex   string
	CMTIStorage string
}

func urcKey(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "^") || strings.HasPrefix(s, "$") {
		if i := strings.IndexByte(s, ':'); i > 0 {
			return s[:i]
		}
		if j := strings.IndexAny(s, " ,"); j > 0 {
			return s[:j]
		}
		return s
	}
	// 无前缀但含空格的标准 URC，需要返回完整字符串作为 Key
	switch s {
	case "NO CARRIER", "NO ANSWER", "SMS Ready", "Call Ready", "NORMAL POWER DOWN":
		return s
	}
	if j := strings.IndexByte(s, ' '); j > 0 {
		return s[:j]
	}
	return s
}

func parseCMTI(line string) (string, string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "+CMTI:") {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, "+CMTI:"))
	parts := strings.Split(rest, ",")
	if len(parts) < 2 {
		return "", "", false
	}
	storage := strings.Trim(strings.TrimSpace(parts[0]), "\"")
	index := strings.TrimSpace(parts[1])
	if storage == "" || index == "" {
		return "", "", false
	}
	return storage, index, true
}

func parseURCAfterColon(line string) string {
	if i := strings.IndexByte(line, ':'); i >= 0 {
		return strings.TrimSpace(line[i+1:])
	}
	return ""
}

func parseCommaFields(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(parts[i]), "\"")
	}
	return parts
}

func (m *Manager) formatURC(line string) urcFormatResult {
	s := strings.TrimSpace(line)
	if s == "" {
		return urcFormatResult{Level: urcLogDebug, Key: "", Msg: "URC"}
	}

	key := urcKey(s)
	out := urcFormatResult{
		Level:  urcLogDebug,
		Key:    key,
		Msg:    "URC",
		Fields: []any{"type", key},
	}

	switch key {
	case "+CMTI":
		st, idx, ok := parseCMTI(s)
		if ok {
			out.Level = urcLogInfo
			out.Msg = "URC: 新短信通知"
			out.Fields = append(out.Fields, "storage", st, "index", idx)
			out.CMTIIndex = idx
			out.CMTIStorage = st
			return out
		}
		out.Level = urcLogInfo
		out.Msg = "URC: 新短信通知"
		out.Fields = append(out.Fields, "raw", s)
		return out

	case "+CREG", "+CGREG", "+CEREG":
		rest := parseURCAfterColon(s)
		fields := parseCommaFields(rest)
		stat := -1
		if len(fields) >= 2 {
			if v, ok := parseInt(fields[1]); ok {
				stat = v
			}
		}
		out.Level = urcLogInfo
		out.Msg = "URC: 注册状态变更"
		out.Fields = append(out.Fields, "domain", strings.TrimPrefix(key, "+"), "stat", stat)
		if stat >= 0 && key == "+CREG" {
			out.Fields = append(out.Fields, "stat_text", m.getRegStatusText(stat))
		}
		if len(fields) >= 4 {
			out.Fields = append(out.Fields, "lac", fields[2], "cell_id", fields[3])
		}
		if len(fields) >= 5 {
			out.Fields = append(out.Fields, "act", fields[4])
		}
		return out

	case "+CPIN":
		rest := parseURCAfterColon(s)
		out.Level = urcLogInfo
		out.Msg = "URC: SIM 状态"
		out.Fields = append(out.Fields, "state", strings.Trim(strings.TrimSpace(rest), "\""))
		return out

	case "+QSIMSTAT":
		rest := parseURCAfterColon(s)
		fields := parseCommaFields(rest)
		inserted := -1
		if len(fields) >= 2 {
			if v, ok := parseInt(fields[1]); ok {
				inserted = v
			}
		}
		out.Level = urcLogInfo
		out.Msg = "URC: SIM 插拔"
		out.Fields = append(out.Fields, "inserted", inserted)
		return out

	case "+QIURC", "+QIND":
		rest := parseURCAfterColon(s)
		fields := extractQuotedFields(rest)
		name := ""
		if len(fields) > 0 {
			name = fields[0]
		}
		out.Level = urcLogInfo
		out.Msg = "URC: Quectel 事件"
		if name != "" {
			out.Fields = append(out.Fields, "event", name)
		}
		out.Fields = append(out.Fields, "raw", s)
		return out

	case "+CUSD":
		rest := parseURCAfterColon(s)
		fields := parseCommaFields(rest)
		n := -1
		dcs := -1
		text := ""
		if len(fields) >= 1 {
			n, _ = parseInt(fields[0])
		}
		if len(fields) >= 2 {
			text = fields[1]
		}
		if len(fields) >= 3 {
			dcs, _ = parseInt(fields[2])
		}
		out.Level = urcLogInfo
		out.Msg = "URC: USSD"
		out.Fields = append(out.Fields, "n", n, "dcs", dcs, "text", text)
		return out

	case "+CLIP":
		rest := parseURCAfterColon(s)
		fields := extractQuotedFields(rest)
		number := ""
		if len(fields) > 0 {
			number = fields[0]
		}
		out.Level = urcLogInfo
		out.Msg = "URC: 来电显示"
		if number != "" {
			out.Fields = append(out.Fields, "number", number)
		}
		out.Fields = append(out.Fields, "raw", s)
		return out

	case "+QPCMV":
		rest := parseURCAfterColon(s)
		out.Level = urcLogInfo
		out.Msg = "URC: PCM 流控"
		out.Fields = append(out.Fields, "state", strings.TrimSpace(rest))
		return out

	default:
		switch s {
		case "RING", "RDY", "SMS Ready", "Call Ready", "NORMAL POWER DOWN", "NO CARRIER", "BUSY", "NO ANSWER":
			out.Level = urcLogInfo
			out.Msg = "URC: 事件"
			out.Fields = append(out.Fields, "raw", s)
			return out
		}
		out.Level = urcLogDebug
		out.Msg = "URC: 未分类"
		out.Fields = append(out.Fields, "raw", s)
		return out
	}
}

func parseInt(s string) (int, bool) {
	v := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &v); err != nil {
		return 0, false
	}
	return v, true
}
