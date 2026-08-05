package simaid

import (
	"bytes"
	"encoding/hex"
	"strings"
)

var (
	usimPrefix = []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02}
	isimPrefix = []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x04}
)

func NormalizeHexAID(aid string) string {
	return strings.ToUpper(strings.TrimSpace(aid))
}

func IsUSIM(aid []byte) bool {
	return len(aid) >= len(usimPrefix) && bytes.Equal(aid[:len(usimPrefix)], usimPrefix)
}

func IsISIM(aid []byte) bool {
	return len(aid) >= len(isimPrefix) && bytes.Equal(aid[:len(isimPrefix)], isimPrefix)
}

func IsUSIMHex(aid string) bool {
	b, err := hex.DecodeString(NormalizeHexAID(aid))
	return err == nil && IsUSIM(b)
}

func IsISIMHex(aid string) bool {
	b, err := hex.DecodeString(NormalizeHexAID(aid))
	return err == nil && IsISIM(b)
}

func AppendUniqueAIDs(dst [][]byte, src ...[]byte) [][]byte {
	for _, aid := range src {
		if len(aid) == 0 {
			continue
		}
		dup := false
		for _, existing := range dst {
			if bytes.Equal(existing, aid) {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, append([]byte(nil), aid...))
		}
	}
	return dst
}

func FilterUSIM(aids [][]byte) [][]byte {
	out := make([][]byte, 0, len(aids))
	for _, aid := range aids {
		if IsUSIM(aid) {
			out = AppendUniqueAIDs(out, aid)
		}
	}
	return out
}

func FilterISIM(aids [][]byte) [][]byte {
	out := make([][]byte, 0, len(aids))
	for _, aid := range aids {
		if IsISIM(aid) {
			out = AppendUniqueAIDs(out, aid)
		}
	}
	return out
}
