package main

// 从增强版 1.2.10（rogerbush007-a11y/DJOneHub-mac-enhanced, commit 5b15eae）
// 移植的「连接模式」功能：把模块 USB 配置在 Mac 完整模式（UAC 开，含通话音频）
// 与 iPhone/iPad 模式（UAC 关，仅上网+短信）之间切换。
//
// 关键设计（保持与上游一致）：
//   - 切到 mobile 只写 usbcfg 的 UAC 位并保存意图，不重启模块；拔出插到
//     iPhone/iPad 时新配置自然生效。
//   - 切到 mobile 前先通过 ADB 给模块装网络唤醒保活，这样 iOS 侧不需要
//     常驻 App 也能保持数据通路。
//   - 意图（usb-profile-intent.json）只由本安装显式写入：模块重新插回 Mac、
//     AT 桥重建时，若发现 armed 且 UAC 未开，则自动恢复 Mac 完整模式。
//     新安装绝不根据「非 UAC 的 USB 组合」猜测意图——那可能是出厂状态，
//     也可能是其他工具配置的。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type usbProfileStatus struct {
	Mode           string `json:"mode"`
	UACEnabled     bool   `json:"uac_enabled"`
	Configuration  string `json:"configuration"`
	NeedsReconnect bool   `json:"needs_reconnect"`
	Message        string `json:"message,omitempty"`
}

type usbConfig struct{ fields []string }

func (c usbConfig) uacEnabled() bool { return len(c.fields) == 9 && c.fields[8] == "1" }

func (c usbConfig) withUAC(enabled bool) string {
	fields := append([]string(nil), c.fields...)
	if enabled {
		fields[8] = "1"
	} else {
		fields[8] = "0"
	}
	return `AT+QCFG="usbcfg",` + strings.Join(fields, ",")
}

func parseUSBConfig(resp string) (usbConfig, error) {
	re := regexp.MustCompile(`(?im)^\s*\+QCFG:\s*"usbcfg"\s*,\s*([^\r\n]+)`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return usbConfig{}, errors.New("模块未返回可识别的 USBCFG 配置")
	}
	fields := strings.Split(match[1], ",")
	if len(fields) != 9 {
		return usbConfig{}, fmt.Errorf("USBCFG 功能位数量异常（%d），已拒绝写入", len(fields))
	}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
		if fields[i] == "" {
			return usbConfig{}, errors.New("USBCFG 含空字段，已拒绝写入")
		}
	}
	if fields[8] != "0" && fields[8] != "1" {
		return usbConfig{}, fmt.Errorf("USBCFG UAC 位异常（%s），已拒绝写入", fields[8])
	}
	return usbConfig{fields: fields}, nil
}

func profileStatus(config usbConfig, raw string, needsReconnect bool, message string) usbProfileStatus {
	mode := "mac"
	if !config.uacEnabled() {
		mode = "mobile"
	}
	return usbProfileStatus{Mode: mode, UACEnabled: config.uacEnabled(), Configuration: raw, NeedsReconnect: needsReconnect, Message: message}
}

type usbProfileIntent struct {
	MobileArmed bool `json:"mobile_armed"`
}

func (a *app) loadUSBProfileIntentLocked() error {
	if a.usbProfileIntentLoaded {
		return nil
	}
	if a.usbProfileIntentPath == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		a.usbProfileIntentPath = filepath.Join(configDir, "DJOneHubNative", "usb-profile-intent.json")
	}
	var intent usbProfileIntent
	data, err := os.ReadFile(a.usbProfileIntentPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &intent); err != nil {
			return err
		}
	}
	a.usbProfileMobileArmed = intent.MobileArmed
	a.usbProfileIntentLoaded = true
	return nil
}

func (a *app) persistUSBProfileIntentLocked() error {
	if err := a.loadUSBProfileIntentLocked(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.usbProfileIntentPath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(usbProfileIntent{MobileArmed: a.usbProfileMobileArmed}, "", "  ")
	if err != nil {
		return err
	}
	temporary := a.usbProfileIntentPath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, a.usbProfileIntentPath)
}

func (a *app) usbProfile(w http.ResponseWriter, _ *http.Request) {
	config, raw, err := a.readUSBProfile()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profileStatus(config, raw, false, ""))
}

func (a *app) readUSBProfile() (usbConfig, string, error) {
	response, err := a.runATCommand(`AT+QCFG="usbcfg"`, 8*time.Second)
	if err != nil {
		return usbConfig{}, "", err
	}
	config, err := parseUSBConfig(response)
	if err != nil {
		return usbConfig{}, "", err
	}
	return config, strings.TrimSpace(response), nil
}

// setUSBProfile 只修改 Quectel 文档中的 UAC 位。mobile 模式保存配置但不重启；
// 拔出插到 iPhone/iPad 后生效。恢复 Mac 模式则写入后立即重启模块。
func (a *app) setUSBProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if mode != "mobile" && mode != "mac" {
		writeError(w, http.StatusBadRequest, "mode must be mobile or mac")
		return
	}
	a.usbProfileMu.Lock()
	defer a.usbProfileMu.Unlock()
	// 给模块保留网络唤醒保活，iPhone/iPad 侧就不需要常驻 App。
	// 必须在改持久 USB 配置之前、Mac 侧 ADB 仍可用时装好。
	if mode == "mobile" {
		if err := enableModuleNetworkWake(); err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("启用模块网络唤醒失败: %v", err))
			return
		}
	} else if err := disableModuleNetworkWake(); err != nil {
		// 绝不阻塞 Mac 模式恢复。USB 音频回来后 helper 自己也会空转，
		// 即使这里的 ADB 暂时不可用。
		log.Printf("disable module network wake: %v", err)
	}
	config, raw, err := a.readUSBProfile()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	wantUAC := mode == "mac"
	if config.uacEnabled() == wantUAC {
		a.usbProfileMobileArmed = mode == "mobile"
		if err := a.persistUSBProfileIntentLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("保存连接模式意图失败: %v", err))
			return
		}
		message := "当前已是 Mac 完整模式"
		if mode == "mobile" {
			message = "当前已是 iPhone/iPad 模式；拔插到移动设备后生效"
		}
		writeJSON(w, http.StatusOK, profileStatus(config, raw, false, message))
		return
	}
	if mode == "mobile" {
		// 切走 UAC 之前先停掉模块语音路由，避免 UAC 设备消失的瞬间
		// 宿主音频重新配置时报错。失败只记日志：UAC 位仍需写入。
		if err := a.stopModuleVoiceRoute(a.runtimeDirectory()); err != nil {
			log.Printf("stop module voice route before switching USB profile: %v", err)
		}
		if _, err := a.runATCommand("AT+QPCMV=0", 5*time.Second); err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("关闭当前语音流失败: %v", err))
			return
		}
	}
	if _, err := a.runATCommand(config.withUAC(wantUAC), 8*time.Second); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("写入 USB 配置失败: %v", err))
		return
	}
	updated := usbConfig{fields: append(append([]string(nil), config.fields[:8]...), map[bool]string{true: "1", false: "0"}[wantUAC])}
	if mode == "mobile" {
		a.usbProfileMobileArmed = true
		if err := a.persistUSBProfileIntentLocked(); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("保存 iPhone/iPad 模式意图失败: %v", err))
			return
		}
		writeJSON(w, http.StatusAccepted, profileStatus(updated, raw, true, "已保存 iPhone/iPad 模式；现在直接拔出并连接移动设备即可"))
		return
	}
	a.usbProfileMobileArmed = false
	if err := a.persistUSBProfileIntentLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("保存 Mac 模式意图失败: %v", err))
		return
	}
	if _, err := a.runATCommand("AT+CFUN=1,1", 3*time.Second); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("已写入 Mac 模式，但模块重启失败: %v", err))
		return
	}
	writeJSON(w, http.StatusAccepted, profileStatus(updated, raw, true, "已恢复 Mac 完整模式，模块正在重新连接"))
}

// autoEnableMacAudioProfile 只在本安装显式保存过 iPhone/iPad 模式意图时才
// 自动恢复 UAC。新装的 App 绝不能从「非 UAC 的 USB 组合」推断意图：那可能
// 是出厂模块，也可能是其他工具配置的结果。
func (a *app) autoEnableMacAudioProfile() (bool, error) {
	if a.usbAT == nil {
		return false, nil
	}
	a.usbProfileMu.Lock()
	err := a.loadUSBProfileIntentLocked()
	armed := a.usbProfileMobileArmed
	a.usbProfileMu.Unlock()
	if err != nil {
		return false, err
	}
	if !armed {
		return false, nil
	}
	response, err := a.usbAT.Command(`AT+QCFG="usbcfg"`, 8*time.Second)
	if err != nil {
		return false, err
	}
	config, err := parseUSBConfig(response)
	if err != nil {
		return false, err
	}
	if config.uacEnabled() {
		return false, nil
	}
	if _, err := a.usbAT.Command(config.withUAC(true), 8*time.Second); err != nil {
		return false, err
	}
	a.usbProfileMu.Lock()
	a.usbProfileMobileArmed = false
	if err := a.persistUSBProfileIntentLocked(); err != nil {
		a.usbProfileMu.Unlock()
		return false, err
	}
	a.usbProfileMu.Unlock()
	if _, err := a.usbAT.Command("AT+CFUN=1,1", 3*time.Second); err != nil {
		return false, err
	}
	log.Printf("module arrived in iPhone/iPad mode; restoring Mac USB Audio and re-enumerating")
	a.markUSBATDetached("switching to Mac complete USB profile")
	return true, nil
}
