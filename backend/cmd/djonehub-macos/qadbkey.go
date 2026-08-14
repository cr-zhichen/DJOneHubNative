package main

import (
	"crypto/md5" // #nosec G501 -- legacy QADBKEY interoperability requires Unix MD5-crypt.
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	legacyQADBKeySecret = "SH_adb_quectel"
	legacyQADBKeyLength = 15
	md5CryptMagic       = "$1$"
	md5CryptAlphabet    = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var (
	legacyQADBKeyChallengePattern = regexp.MustCompile(`(?im)^\s*\+QADBKEY:\s*([0-9]{8})\s*$`)
	legacyQADBKeyValuePattern     = regexp.MustCompile(`^[0-9]{8}$`)
)

func parseLegacyQADBKeyChallenge(response string) (string, error) {
	return parseLegacyQADBKeyChallengeResponse(response, true)
}

func parseLegacyQADBKeyChallengeResponse(response string, requireTerminalOK bool) (string, error) {
	if callModeATResponseIsError(response) {
		return "", errors.New("模块不支持旧式 QADBKEY 授权")
	}
	if requireTerminalOK && !callModeATResponseSucceeded(response) {
		return "", errors.New("模块查询 ADB 授权时没有返回 OK")
	}
	matches := legacyQADBKeyChallengePattern.FindAllStringSubmatch(response, 2)
	if len(matches) != 1 || len(matches[0]) != 2 {
		return "", errors.New("模块没有返回可识别的 8 位 QADBKEY 挑战值")
	}
	return matches[0][1], nil
}

func (a *app) readLegacyQADBKeyChallenge() (string, error) {
	response, err := a.runSensitiveATCommand("AT+QADBKEY?", "AT+QADBKEY?", 4*time.Second)
	if err != nil {
		return "", fmt.Errorf("查询模块 ADB 授权：%w", err)
	}
	return parseLegacyQADBKeyChallengeResponse(response, a.modem == nil)
}

// legacyQADBUnlockPassword derives the 15-character response expected by
// QDC507/MDM9x07-era QADBKEY firmware. It is computed locally and must never be
// persisted or included in status/error payloads.
func legacyQADBUnlockPassword(challenge string) (string, error) {
	if !legacyQADBKeyValuePattern.MatchString(challenge) {
		return "", errors.New("QADBKEY 挑战值格式无效")
	}
	hash := md5Crypt([]byte(legacyQADBKeySecret), []byte(challenge))
	prefix := md5CryptMagic + challenge + "$"
	if !strings.HasPrefix(hash, prefix) || len(hash) < len(prefix)+legacyQADBKeyLength {
		return "", errors.New("无法生成 QADBKEY 授权密码")
	}
	return hash[len(prefix) : len(prefix)+legacyQADBKeyLength], nil
}

func md5Crypt(password, salt []byte) string {
	initial := md5.New() // #nosec G401 -- required by the modem's legacy protocol.
	_, _ = initial.Write(password)
	_, _ = initial.Write([]byte(md5CryptMagic))
	_, _ = initial.Write(salt)

	alternate := md5.New() // #nosec G401 -- required by the modem's legacy protocol.
	_, _ = alternate.Write(password)
	_, _ = alternate.Write(salt)
	_, _ = alternate.Write(password)
	alternateSum := alternate.Sum(nil)
	for remaining := len(password); remaining > 0; remaining -= md5.Size {
		count := remaining
		if count > md5.Size {
			count = md5.Size
		}
		_, _ = initial.Write(alternateSum[:count])
	}
	for count := len(password); count > 0; count >>= 1 {
		if count&1 != 0 {
			_, _ = initial.Write([]byte{0})
		} else {
			_, _ = initial.Write(password[:1])
		}
	}
	digest := initial.Sum(nil)

	for round := 0; round < 1000; round++ {
		current := md5.New() // #nosec G401 -- required by the modem's legacy protocol.
		if round&1 != 0 {
			_, _ = current.Write(password)
		} else {
			_, _ = current.Write(digest)
		}
		if round%3 != 0 {
			_, _ = current.Write(salt)
		}
		if round%7 != 0 {
			_, _ = current.Write(password)
		}
		if round&1 != 0 {
			_, _ = current.Write(digest)
		} else {
			_, _ = current.Write(password)
		}
		digest = current.Sum(nil)
	}

	encoded := strings.Builder{}
	encoded.Grow(22)
	writeMD5CryptBase64(&encoded, digest[0], digest[6], digest[12], 4)
	writeMD5CryptBase64(&encoded, digest[1], digest[7], digest[13], 4)
	writeMD5CryptBase64(&encoded, digest[2], digest[8], digest[14], 4)
	writeMD5CryptBase64(&encoded, digest[3], digest[9], digest[15], 4)
	writeMD5CryptBase64(&encoded, digest[4], digest[10], digest[5], 4)
	writeMD5CryptBase64(&encoded, 0, 0, digest[11], 2)
	return md5CryptMagic + string(salt) + "$" + encoded.String()
}

func writeMD5CryptBase64(output *strings.Builder, high, middle, low byte, count int) {
	value := uint32(high)<<16 | uint32(middle)<<8 | uint32(low)
	for index := 0; index < count; index++ {
		output.WriteByte(md5CryptAlphabet[value&0x3f])
		value >>= 6
	}
}
