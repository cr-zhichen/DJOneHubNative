package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	moduleVoiceCommit  = "0443dfdaf8aec086fd76ba2ee9152fd908114524"
	moduleVoiceVersion = "qdc507-3.18.44-voice-20260712.5"
	moduleVoiceKernel  = "3.18.44"
	moduleVoiceBaseURL = "https://raw.githubusercontent.com/moluncn/mavo/" + moduleVoiceCommit + "/Resources/ModuleVoice/"
	// The AT interface returns well before adbd is ready after CFUN reboot on
	// QDC507. Sending CNXN during that gap can leave the gadget transport
	// silent for the rest of the boot, so require a full USB settling window
	// before the first real ADB handshake.
	callModeADBReenumerationSettleDelay = 35 * time.Second
)

type moduleVoiceFile struct {
	Name   string
	Size   int64
	SHA256 string
	Mode   os.FileMode
}

var moduleVoiceFiles = []moduleVoiceFile{
	{Name: "qdc507_aprv3.ko", Size: 36664, SHA256: "3d82d3dec4f1e323201bba87156df9d41438e08314097353f2607f9117211d4a", Mode: 0o644},
	{Name: "qdc507_voice.ko", Size: 999236, SHA256: "ed3821682d5309969a01c764192c83feff9669c61ef237c69475cd1619cf296c", Mode: 0o644},
	{Name: "mavo-pcm-bridge.armv7", Size: 17860, SHA256: "88d47c15e61d1428a59c821fed804c2e6490e82859a085062f21966b58d167fc", Mode: 0o755},
	{Name: "manifest.json", Size: 729, SHA256: "f4f6c266ced7015d4e61d993a6e31247c26a9e85a8fdf1c6d842c459e1e2970a", Mode: 0o644},
	{Name: "COPYING-GPL-2.0", Size: 18693, SHA256: "af8067302947c01fd9eee72befa54c7e3ef8a48fecde7fd71277f2290b2bf0f7", Mode: 0o644},
	{Name: "MODULE-REPORT.md", Size: 7443, SHA256: "fb9d58336bcfdad8938d7833c113a815c2153d9a04564eb73cddabea737f8be2", Mode: 0o644},
}

type callModeDownloadSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Detail  string `json:"detail"`
	Trusted bool   `json:"trusted"`
}

type callModeVoiceConfiguration struct {
	IMS             int `json:"ims"`
	VoLTECapability int `json:"volte_capability"`
	VoLTEDisabled   int `json:"volte_disabled"`
}

func (c callModeVoiceConfiguration) validate() error {
	if c.IMS != 0 && c.IMS != 1 {
		return fmt.Errorf("IMS 配置值 %d 无效", c.IMS)
	}
	if c.VoLTECapability != 0 && c.VoLTECapability != 1 {
		return fmt.Errorf("VoLTE 能力值 %d 无效", c.VoLTECapability)
	}
	if c.VoLTEDisabled != 0 && c.VoLTEDisabled != 1 {
		return fmt.Errorf("VoLTE 开关值 %d 无效", c.VoLTEDisabled)
	}
	return nil
}

func (c callModeVoiceConfiguration) ready() bool {
	return c.IMS == 1 && c.VoLTECapability == 1 && c.VoLTEDisabled == 0
}

func (c callModeVoiceConfiguration) sameWritableValues(other callModeVoiceConfiguration) bool {
	return c.IMS == other.IMS && c.VoLTEDisabled == other.VoLTEDisabled
}

var callModeDownloadSources = []callModeDownloadSource{
	{ID: "github", Name: "GitHub 原始源", Detail: "直接从 raw.githubusercontent.com 的固定提交下载", Trusted: true},
	{ID: "relay", Name: "国内转发源", Detail: "依次尝试 ghfast.top 与 gh-proxy.com，仍使用同一 SHA-256 校验", Trusted: false},
}

type callModeStatus struct {
	State                                string                   `json:"state"`
	Summary                              string                   `json:"summary"`
	Detail                               string                   `json:"detail,omitempty"`
	ADBEnabled                           bool                     `json:"adb_enabled"`
	InterfacesReady                      bool                     `json:"interfaces_ready"`
	ADBAuthorizationRequired             bool                     `json:"adb_authorization_required"`
	ADBAuthorizationAccepted             bool                     `json:"adb_authorization_accepted"`
	UACEnabled                           bool                     `json:"uac_enabled"`
	VoiceConfigured                      bool                     `json:"voice_configured"`
	RuntimeDownloaded                    bool                     `json:"runtime_downloaded"`
	RuntimeVersion                       string                   `json:"runtime_version"`
	RuntimePath                          string                   `json:"runtime_path"`
	DownloadedBytes                      int64                    `json:"downloaded_bytes"`
	TotalBytes                           int64                    `json:"total_bytes"`
	Source                               string                   `json:"source,omitempty"`
	CanEnable                            bool                     `json:"can_enable"`
	CanDownload                          bool                     `json:"can_download"`
	CanRetry                             bool                     `json:"can_retry"`
	RequiresADBAuthorizationConfirmation bool                     `json:"requires_adb_authorization_confirmation"`
	RequiresUSBConfirmation              bool                     `json:"requires_usb_confirmation"`
	RequiresDownloadConfirmation         bool                     `json:"requires_download_confirmation"`
	BackupPath                           string                   `json:"backup_path,omitempty"`
	LastRestore                          *callModeRestoreNotice   `json:"last_restore,omitempty"`
	UpdatedAt                            time.Time                `json:"updated_at"`
	Sources                              []callModeDownloadSource `json:"sources"`
	Upstream                             string                   `json:"upstream"`
}

func newCallModeStatus(state, summary string) callModeStatus {
	return callModeStatus{
		State:          state,
		Summary:        summary,
		RuntimeVersion: moduleVoiceVersion,
		TotalBytes:     moduleVoiceTotalBytes(),
		Sources:        callModeDownloadSources,
		Upstream:       "https://github.com/moluncn/mavo/tree/" + moduleVoiceCommit + "/Resources/ModuleVoice",
		UpdatedAt:      time.Now(),
	}
}

type usbComposition struct {
	VendorID  int   `json:"vendor_id"`
	ProductID int   `json:"product_id"`
	Flags     []int `json:"flags"`
}

func (c usbComposition) command() string {
	parts := []string{fmt.Sprintf("0x%04X", c.VendorID), fmt.Sprintf("0x%04X", c.ProductID)}
	for _, flag := range c.Flags {
		parts = append(parts, strconv.Itoa(flag))
	}
	return `AT+QCFG="USBCFG",` + strings.Join(parts, ",")
}

func (c usbComposition) hasUAC() bool {
	return len(c.Flags) >= 1 && c.Flags[len(c.Flags)-1] == 1
}

func (c usbComposition) hasADB() bool {
	return len(c.Flags) >= 2 && c.Flags[len(c.Flags)-2] == 1
}

func (c usbComposition) callModeTarget() (usbComposition, error) {
	if err := c.validate(); err != nil {
		return usbComposition{}, err
	}
	target := usbComposition{VendorID: c.VendorID, ProductID: c.ProductID, Flags: append([]int(nil), c.Flags...)}
	target.Flags[len(target.Flags)-2] = 1
	target.Flags[len(target.Flags)-1] = 1
	return target, nil
}

func (c usbComposition) validate() error {
	if c.VendorID <= 0 || c.VendorID > 0xffff || c.ProductID <= 0 || c.ProductID > 0xffff {
		return errors.New("模块 USB VID/PID 超出有效范围；为避免覆盖未知配置，已停止")
	}
	if len(c.Flags) != 7 {
		return fmt.Errorf("模块返回了 %d 个 USB 功能位，预期为 7；为避免覆盖未知配置，已停止", len(c.Flags))
	}
	for _, flag := range c.Flags {
		if flag != 0 && flag != 1 {
			return errors.New("模块 USB 配置包含非布尔功能位；为避免覆盖未知配置，已停止")
		}
	}
	return nil
}

func (c usbComposition) equal(other usbComposition) bool {
	if c.VendorID != other.VendorID || c.ProductID != other.ProductID || len(c.Flags) != len(other.Flags) {
		return false
	}
	for index := range c.Flags {
		if c.Flags[index] != other.Flags[index] {
			return false
		}
	}
	return true
}

// validateUSBReadBackBeforeReboot accepts firmware that reports either the
// active value or the newly saved value for each intentionally changed flag.
// Identity and every untouched flag must remain exact; any third state stops
// the flow before the module is rebooted.
func validateUSBReadBackBeforeReboot(original, target, actual usbComposition) error {
	if err := original.validate(); err != nil {
		return fmt.Errorf("原始 USB 配置无效：%w", err)
	}
	if err := target.validate(); err != nil {
		return fmt.Errorf("目标 USB 配置无效：%w", err)
	}
	if err := actual.validate(); err != nil {
		return fmt.Errorf("回读 USB 配置无效：%w", err)
	}
	if original.VendorID != target.VendorID || original.ProductID != target.ProductID {
		return errors.New("目标 USB VID/PID 与原始配置不一致")
	}
	if actual.VendorID != original.VendorID || actual.ProductID != original.ProductID {
		return errors.New("重启前回读的 USB VID/PID 发生意外变化")
	}
	for index := range original.Flags {
		if original.Flags[index] == target.Flags[index] && actual.Flags[index] != original.Flags[index] {
			return fmt.Errorf("重启前回读的第 %d 个未修改功能位发生变化", index+1)
		}
		if actual.Flags[index] != original.Flags[index] && actual.Flags[index] != target.Flags[index] {
			return fmt.Errorf("重启前回读的第 %d 个功能位不是原值或目标值", index+1)
		}
	}
	return nil
}

func parseUSBComposition(response string) (usbComposition, error) {
	normalized := strings.ReplaceAll(response, "\r", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		marker := `+qcfg: "usbcfg",`
		index := strings.Index(lower, marker)
		if index < 0 {
			continue
		}
		fields := strings.Split(line[index+len(marker):], ",")
		if len(fields) < 4 {
			break
		}
		parse := func(raw string) (int, error) {
			value, err := strconv.ParseInt(strings.TrimSpace(raw), 0, 32)
			return int(value), err
		}
		vendorID, err := parse(fields[0])
		if err != nil {
			return usbComposition{}, fmt.Errorf("解析 USB vendor ID：%w", err)
		}
		productID, err := parse(fields[1])
		if err != nil {
			return usbComposition{}, fmt.Errorf("解析 USB product ID：%w", err)
		}
		flags := make([]int, 0, len(fields)-2)
		for _, field := range fields[2:] {
			value, err := parse(field)
			if err != nil {
				return usbComposition{}, fmt.Errorf("解析 USB 功能位：%w", err)
			}
			flags = append(flags, value)
		}
		return usbComposition{VendorID: vendorID, ProductID: productID, Flags: flags}, nil
	}
	return usbComposition{}, errors.New("模块没有返回可识别的 USBCFG")
}

func parseCallModeIMSConfiguration(response string) (configuration, capability int, err error) {
	normalized := strings.ReplaceAll(response, "\r", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		marker := `+qcfg: "ims",`
		index := strings.Index(lower, marker)
		if index < 0 {
			continue
		}
		fields := strings.Split(line[index+len(marker):], ",")
		if len(fields) < 2 {
			break
		}
		configuration64, parseErr := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 32)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("解析 IMS 配置：%w", parseErr)
		}
		capability64, parseErr := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 32)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("解析 VoLTE 能力：%w", parseErr)
		}
		configuration = int(configuration64)
		capability = int(capability64)
		voice := callModeVoiceConfiguration{IMS: configuration, VoLTECapability: capability}
		if validationErr := voice.validate(); validationErr != nil {
			return 0, 0, validationErr
		}
		return configuration, capability, nil
	}
	return 0, 0, errors.New("模块没有返回可识别的 IMS 配置")
}

func parseCallModeVoLTEDisabled(response string) (int, error) {
	normalized := strings.ReplaceAll(strings.ToLower(response), "_", "/")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		marker := `+qcfg: "volte/disable",`
		index := strings.Index(line, marker)
		if index < 0 {
			continue
		}
		value, parseErr := strconv.ParseInt(strings.TrimSpace(line[index+len(marker):]), 10, 32)
		if parseErr != nil {
			return 0, fmt.Errorf("解析 VoLTE 开关：%w", parseErr)
		}
		configuration := callModeVoiceConfiguration{VoLTEDisabled: int(value)}
		if validationErr := configuration.validate(); validationErr != nil {
			return 0, validationErr
		}
		return int(value), nil
	}
	return 0, errors.New("模块没有返回可识别的 VoLTE 开关")
}

func (a *app) readCallModeVoiceConfiguration() (callModeVoiceConfiguration, error) {
	imsResponse, err := a.runATCommand(`AT+QCFG="ims"`, 5*time.Second)
	if err != nil {
		return callModeVoiceConfiguration{}, fmt.Errorf("读取 IMS 配置：%w", err)
	}
	ims, capability, err := parseCallModeIMSConfiguration(imsResponse)
	if err != nil {
		return callModeVoiceConfiguration{}, err
	}
	volteResponse, err := a.runATCommand(`AT+QCFG="volte_disable"`, 5*time.Second)
	if err != nil {
		return callModeVoiceConfiguration{}, fmt.Errorf("读取 VoLTE 开关：%w", err)
	}
	volteDisabled, err := parseCallModeVoLTEDisabled(volteResponse)
	if err != nil {
		return callModeVoiceConfiguration{}, err
	}
	configuration := callModeVoiceConfiguration{
		IMS:             ims,
		VoLTECapability: capability,
		VoLTEDisabled:   volteDisabled,
	}
	return configuration, configuration.validate()
}

func (a *app) applyCallModeVoiceConfiguration(target callModeVoiceConfiguration) error {
	if err := target.validate(); err != nil {
		return err
	}
	current, err := a.readCallModeVoiceConfiguration()
	if err != nil {
		return err
	}
	if current.VoLTECapability != 1 || target.VoLTECapability != 1 {
		return errors.New("当前模块没有报告可用的 VoLTE 能力")
	}
	if current.VoLTEDisabled != target.VoLTEDisabled {
		response, commandErr := a.runATCommand(
			fmt.Sprintf(`AT+QCFG="volte_disable",%d`, target.VoLTEDisabled),
			8*time.Second,
		)
		if !a.callModeATCommandAccepted(response, commandErr) {
			return fmt.Errorf("模块拒绝更新 VoLTE 开关：%s", firstCallModeDetail(commandErr, response))
		}
	}
	if current.IMS != target.IMS {
		response, commandErr := a.runATCommand(
			fmt.Sprintf(`AT+QCFG="ims",%d`, target.IMS),
			8*time.Second,
		)
		if !a.callModeATCommandAccepted(response, commandErr) {
			return fmt.Errorf("模块拒绝更新 IMS 配置：%s", firstCallModeDetail(commandErr, response))
		}
	}
	actual, err := a.readCallModeVoiceConfiguration()
	if err != nil {
		return err
	}
	if !actual.sameWritableValues(target) || actual.VoLTECapability != target.VoLTECapability {
		return fmt.Errorf(
			"IMS/VoLTE 回读不一致：IMS=%d、VoLTE capability=%d、volte_disabled=%d",
			actual.IMS,
			actual.VoLTECapability,
			actual.VoLTEDisabled,
		)
	}
	return nil
}

func (a *app) submitCallModeADBAuthorization() error {
	challenge, err := a.readLegacyQADBKeyChallenge()
	if err != nil {
		return fmt.Errorf("读取模块 ADB 授权挑战：%w", err)
	}
	password, err := legacyQADBUnlockPassword(challenge)
	challenge = ""
	if err != nil {
		return err
	}
	command := fmt.Sprintf(`AT+QADBKEY="%s"`, password)
	response, commandErr := a.runSensitiveATCommand(command, `AT+QADBKEY="<redacted>"`, 8*time.Second)
	accepted := a.callModeATCommandAccepted(response, commandErr)
	failureDetail := ""
	if !accepted {
		failureDetail = callModeSensitiveATFailureDetail(commandErr, response)
	}
	password = ""
	command = ""
	response = ""
	if !accepted {
		return errors.New(failureDetail)
	}
	return nil
}

func (a *app) runtimeDirectory() string {
	base := a.dataDir
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "DJOneHubNative")
	}
	return filepath.Join(base, "voice-runtime", moduleVoiceVersion)
}

func moduleVoiceTotalBytes() int64 {
	var total int64
	for _, file := range moduleVoiceFiles {
		total += file.Size
	}
	return total
}

func verifyModuleVoiceRuntime(directory string) error {
	for _, expected := range moduleVoiceFiles {
		path := filepath.Join(directory, expected.Name)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("缺少 %s", expected.Name)
		}
		if !info.Mode().IsRegular() || info.Size() != expected.Size {
			return fmt.Errorf("%s 的类型或大小不匹配", expected.Name)
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("读取 %s：%w", expected.Name, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("校验 %s：%w", expected.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("关闭 %s：%w", expected.Name, closeErr)
		}
		if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected.SHA256) {
			return fmt.Errorf("%s 的 SHA-256 不匹配", expected.Name)
		}
	}
	return nil
}

func (a *app) setCallModeStatus(status callModeStatus) {
	status.RuntimeVersion = moduleVoiceVersion
	status.RuntimePath = a.runtimeDirectory()
	status.TotalBytes = moduleVoiceTotalBytes()
	status.Sources = callModeDownloadSources
	status.Upstream = "https://github.com/moluncn/mavo/tree/" + moduleVoiceCommit + "/Resources/ModuleVoice"
	status.UpdatedAt = time.Now()
	a.callModeMu.Lock()
	if status.LastRestore == nil {
		status.LastRestore = a.callMode.LastRestore
	}
	a.callMode = status
	a.callModeMu.Unlock()
}

func (a *app) currentCallModeStatus() callModeStatus {
	a.callModeRefreshMu.Lock()
	defer a.callModeRefreshMu.Unlock()

	a.callModeMu.RLock()
	status := a.callMode
	running := a.callModeOperation
	a.callModeMu.RUnlock()
	if running || status.State == "failed" {
		return status
	}
	if strings.HasPrefix(status.State, "needs_") {
		return status
	}
	if status.State == "ready" && a.isModuleVoicePrepared() {
		return status
	}
	status = a.inspectCallModeUnlocked()
	// USB composition and a verified local download are necessary but not
	// sufficient after an app or module restart: the module-side drivers and
	// calibration service must be deployed again before dialing is allowed.
	if status.State == "ready" && !a.isModuleVoicePrepared() {
		preparing := newCallModeStatus("preparing", "正在部署并验证模块侧语音运行时")
		preparing.ADBEnabled = true
		preparing.UACEnabled = true
		preparing.InterfacesReady = true
		preparing.VoiceConfigured = true
		preparing.RuntimeDownloaded = true
		preparing.DownloadedBytes = moduleVoiceTotalBytes()
		if a.beginCallModeOperation(preparing) {
			go func() {
				defer a.endCallModeOperation()
				a.prepareDownloadedRuntime("", "")
			}()
			return a.callModeSnapshot()
		}
		return a.callModeSnapshot()
	}
	a.setCallModeStatus(status)
	a.callModeMu.RLock()
	status = a.callMode
	a.callModeMu.RUnlock()
	return status
}

func (a *app) isModuleVoicePrepared() bool {
	a.moduleVoiceMu.Lock()
	defer a.moduleVoiceMu.Unlock()
	return a.moduleVoicePrepared
}

func (a *app) inspectCallMode() callModeStatus {
	a.callModeRefreshMu.Lock()
	defer a.callModeRefreshMu.Unlock()
	return a.inspectCallModeUnlocked()
}

func (a *app) inspectCallModeUnlocked() callModeStatus {
	runtimeErr := verifyModuleVoiceRuntime(a.runtimeDirectory())
	runtimeReady := runtimeErr == nil
	response, err := a.runATCommand(`AT+QCFG="USBCFG"`, 4*time.Second)
	if err != nil {
		status := newCallModeStatus("disconnected", "等待 4G 模块连接")
		status.Detail = err.Error()
		status.RuntimeDownloaded = runtimeReady
		status.CanRetry = true
		return status
	}
	composition, err := parseUSBComposition(response)
	if err != nil {
		status := newCallModeStatus("unsupported", "无法识别模块 USB 配置")
		status.Detail = err.Error()
		status.RuntimeDownloaded = runtimeReady
		return status
	}
	status := newCallModeStatus("ready", "通话模式已就绪")
	status.ADBEnabled = composition.hasADB()
	status.UACEnabled = composition.hasUAC()
	status.RuntimeDownloaded = runtimeReady
	status.DownloadedBytes = verifiedRuntimeBytes(a.runtimeDirectory())
	voice, voiceErr := a.readCallModeVoiceConfiguration()
	if voiceErr != nil {
		status.State = "unsupported"
		status.Summary = "无法确认模块 IMS/VoLTE 配置"
		status.Detail = voiceErr.Error()
		status.CanRetry = true
		return status
	}
	status.VoiceConfigured = voice.ready()
	if voice.VoLTECapability != 1 {
		status.State = "unsupported"
		status.Summary = "当前模块没有报告 VoLTE 能力"
		status.Detail = fmt.Sprintf("IMS=%d，VoLTE capability=%d，volte_disabled=%d", voice.IMS, voice.VoLTECapability, voice.VoLTEDisabled)
		status.CanRetry = true
		return status
	}
	if !status.ADBEnabled || !status.UACEnabled {
		if _, targetErr := composition.callModeTarget(); targetErr != nil {
			status.State = "unsupported"
			status.Summary = "当前 USB 配置不能安全修改"
			status.Detail = targetErr.Error()
			return status
		}
		if !status.ADBEnabled {
			if _, authorizationErr := a.readLegacyQADBKeyChallenge(); authorizationErr != nil {
				status.State = "unsupported"
				status.Summary = "无法确认模块 ADB 授权方式"
				status.Detail = authorizationErr.Error() + "；为避免在未知安全状态下修改 USB 配置，已停止。"
				status.CanRetry = true
				return status
			}
			status.State = "needs_adb_authorization"
			status.Summary = "需要持久授权并开启模块 ADB"
			status.Detail = "授权密码只在本机计算，不会上传或保存。USB 配置备份可以关闭 ADB 接口，但不能保证撤销模块的持久授权。"
			status.ADBAuthorizationRequired = true
			status.CanEnable = true
			status.RequiresADBAuthorizationConfirmation = true
			status.RequiresUSBConfirmation = true
			return status
		}
		status.State = "needs_usb"
		status.Summary = "需要开启模块 USB 音频"
		status.Detail = "会保留当前 VID/PID 和其他 USB 功能位，只开启缺少的 UAC 功能；模块随后会重启并短暂断网。"
		status.CanEnable = true
		status.RequiresUSBConfirmation = true
		return status
	}
	if !voice.ready() {
		status.State = "needs_voice"
		status.Summary = "需要启用模块 IMS/VoLTE"
		status.Detail = fmt.Sprintf("当前 IMS=%d、VoLTE capability=%d、volte_disabled=%d；完成后会回读并重启模块。", voice.IMS, voice.VoLTECapability, voice.VoLTEDisabled)
		status.CanEnable = true
		status.RequiresUSBConfirmation = true
		return status
	}
	if adbErr := a.probeModuleADB(); adbErr != nil {
		status.State = "needs_interface_recovery"
		status.Summary = "ADB 接口已配置但没有响应"
		status.Detail = adbErr.Error() + "；可重新提交本机计算的 ADB 授权、受控重启模块并验证，不会扩大 USB 配置。"
		status.ADBAuthorizationRequired = true
		status.CanEnable = true
		status.CanRetry = true
		status.RequiresADBAuthorizationConfirmation = true
		status.RequiresUSBConfirmation = true
		return status
	}
	status.InterfacesReady = true
	if !runtimeReady {
		status.State = "needs_download"
		status.Summary = "需要下载模块侧语音运行时"
		status.Detail = runtimeErr.Error()
		status.CanDownload = true
		status.RequiresDownloadConfirmation = true
		return status
	}
	return status
}

func verifiedRuntimeBytes(directory string) int64 {
	var total int64
	for _, expected := range moduleVoiceFiles {
		path := filepath.Join(directory, expected.Name)
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Size() == expected.Size {
			total += expected.Size
		}
	}
	return total
}

func (a *app) beginCallModeOperation(status callModeStatus) bool {
	a.callModeMu.Lock()
	defer a.callModeMu.Unlock()
	if a.callModeOperation {
		return false
	}
	a.callModeOperation = true
	status.RuntimeVersion = moduleVoiceVersion
	status.RuntimePath = a.runtimeDirectory()
	status.TotalBytes = moduleVoiceTotalBytes()
	status.Sources = callModeDownloadSources
	status.Upstream = "https://github.com/moluncn/mavo/tree/" + moduleVoiceCommit + "/Resources/ModuleVoice"
	status.UpdatedAt = time.Now()
	if status.LastRestore == nil {
		status.LastRestore = a.callMode.LastRestore
	}
	a.callMode = status
	return true
}

func (a *app) endCallModeOperation() {
	a.callModeMu.Lock()
	a.callModeOperation = false
	a.callModeMu.Unlock()
}

func (a *app) callModeSnapshot() callModeStatus {
	a.callModeMu.RLock()
	defer a.callModeMu.RUnlock()
	return a.callMode
}

func (a *app) callModeStatusAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.currentCallModeStatus())
}

func (a *app) callModeEnableAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm                 bool `json:"confirm"`
		ConfirmADBAuthorization bool `json:"confirm_adb_authorization"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, "需要确认后才会修改模块 USB 配置")
		return
	}
	if call := a.currentCallState(); call.State != "idle" {
		writeError(w, http.StatusConflict, "通话、呼叫或状态同步进行中，不能授权或修改模块 USB 配置")
		return
	}
	status := newCallModeStatus("enabling_usb", "正在读取并备份模块 USB 配置")
	if !a.beginCallModeOperation(status) {
		writeError(w, http.StatusConflict, "通话模式正在执行其他操作")
		return
	}
	go a.runCallModeEnable(body.ConfirmADBAuthorization)
	writeJSON(w, http.StatusAccepted, a.callModeSnapshot())
}

func (a *app) runCallModeEnable(confirmADBAuthorization bool) {
	defer a.endCallModeOperation()
	response, err := a.runATCommand(`AT+QCFG="USBCFG"`, 5*time.Second)
	if err != nil {
		a.failCallMode("无法读取模块 USB 配置", err.Error())
		return
	}
	original, err := parseUSBComposition(response)
	if err != nil {
		a.failCallMode("无法识别模块 USB 配置", err.Error())
		return
	}
	target, err := original.callModeTarget()
	if err != nil {
		a.failCallMode("当前 USB 配置不能安全修改", err.Error())
		return
	}
	originalVoice, err := a.readCallModeVoiceConfiguration()
	if err != nil {
		a.failCallMode("无法确认模块 IMS/VoLTE 配置", err.Error())
		return
	}
	if originalVoice.VoLTECapability != 1 {
		a.failCallMode(
			"当前模块没有报告 VoLTE 能力",
			fmt.Sprintf("IMS=%d，VoLTE capability=%d，volte_disabled=%d", originalVoice.IMS, originalVoice.VoLTECapability, originalVoice.VoLTEDisabled),
		)
		return
	}
	targetVoice := originalVoice
	targetVoice.IMS = 1
	targetVoice.VoLTEDisabled = 0
	usbNeedsChange := !original.equal(target)
	voiceNeedsChange := !originalVoice.sameWritableValues(targetVoice)
	forceRecoveryReboot := false
	if !usbNeedsChange && !voiceNeedsChange {
		if adbErr := a.probeModuleADB(); adbErr == nil {
			a.finishCallModeAfterUSB("")
			return
		} else {
			forceRecoveryReboot = true
		}
	}
	var authorizationPassword string
	authorizationRequired := !original.hasADB() || forceRecoveryReboot
	if authorizationRequired {
		if !confirmADBAuthorization {
			a.failCallMode("尚未确认模块 ADB 授权", "请在通话页面阅读授权影响并重新确认；模块配置尚未修改。")
			return
		}
		challenge, challengeErr := a.readLegacyQADBKeyChallenge()
		if challengeErr != nil {
			a.failCallMode("无法读取模块 ADB 授权挑战", challengeErr.Error())
			return
		}
		authorizationPassword, err = legacyQADBUnlockPassword(challenge)
		if err != nil {
			a.failCallMode("无法生成模块 ADB 授权密码", err.Error())
			return
		}
	}
	identity, err := a.currentCallModeModuleIdentity()
	if err != nil {
		a.failCallMode("无法确认当前模块身份", err.Error())
		return
	}
	backupPath, err := a.saveCallModeUSBBackupWithIdentity(original, originalVoice, identity, callModeBackupReasonBeforeCallMode)
	if err != nil {
		a.failCallMode("无法保存通话配置备份", err.Error())
		return
	}
	if call := a.currentCallState(); call.State != "idle" {
		a.failCallModeWithBackup("通话模式准备已安全停止", "检测到通话、呼叫或未知通话状态；模块配置尚未修改。", backupPath)
		return
	}
	authorizationPerformed := false
	if authorizationPassword != "" {
		status := newCallModeStatus("authorizing_adb", "正在持久授权模块 ADB")
		status.ADBAuthorizationRequired = true
		status.UACEnabled = original.hasUAC()
		status.BackupPath = backupPath
		status.Detail = "授权密码仅用于当前 AT 交互，不会写入日志、状态或备份文件。"
		a.setCallModeStatus(status)
		authorizationCommand := fmt.Sprintf(`AT+QADBKEY="%s"`, authorizationPassword)
		authorizationResponse, authorizationErr := a.runSensitiveATCommand(
			authorizationCommand,
			`AT+QADBKEY="<redacted>"`,
			8*time.Second,
		)
		authorizationAccepted := a.callModeATCommandAccepted(authorizationResponse, authorizationErr)
		authorizationFailureSummary := ""
		authorizationFailureDetail := ""
		if !authorizationAccepted {
			authorizationFailureSummary = "模块拒绝 ADB 持久授权"
			if authorizationErr != nil || !callModeATResponseIsError(authorizationResponse) {
				authorizationFailureSummary = "无法确认模块 ADB 持久授权结果"
			}
			authorizationFailureDetail = callModeSensitiveATFailureDetail(authorizationErr, authorizationResponse)
		}
		authorizationPassword = ""
		authorizationCommand = ""
		authorizationResponse = ""
		if !authorizationAccepted {
			a.failCallModeWithBackup(
				authorizationFailureSummary,
				authorizationFailureDetail,
				backupPath,
			)
			return
		}
		authorizationPerformed = true
	}
	status := newCallModeStatus("enabling_usb", "正在准备模块通话接口")
	status.BackupPath = backupPath
	status.ADBAuthorizationAccepted = authorizationPerformed
	status.ADBEnabled = target.hasADB()
	status.UACEnabled = target.hasUAC()
	status.VoiceConfigured = !voiceNeedsChange
	if usbNeedsChange {
		if authorizationPerformed {
			status.Detail = "模块已接受 ADB 持久授权；正在仅修改 ADB/UAC 功能位，并保留当前 VID/PID 与其余功能位。"
		} else {
			status.Detail = "正在仅修改缺少的 ADB/UAC 功能位，并保留当前 VID/PID 与其余功能位。"
		}
		a.setCallModeStatus(status)
		if call := a.currentCallState(); call.State != "idle" {
			a.failCallModeWithBackup("通话模式准备已安全停止", "检测到通话、呼叫或未知通话状态；模块 USB 配置尚未修改。", backupPath)
			return
		}
		writeResponse, writeErr := a.runATCommand(target.command(), 8*time.Second)
		if !a.callModeATCommandAccepted(writeResponse, writeErr) {
			a.failCallModeWithBackup("模块拒绝新的 USB 配置", firstCallModeDetail(writeErr, writeResponse), backupPath)
			return
		}
		readBack, readErr := a.runATCommand(`AT+QCFG="USBCFG"`, 5*time.Second)
		actual, parseErr := parseUSBComposition(readBack)
		var validationErr error
		if readErr == nil && parseErr == nil {
			validationErr = validateUSBReadBackBeforeReboot(original, target, actual)
		}
		if readErr != nil || parseErr != nil || validationErr != nil {
			a.failCallModeWithBackup("USB 配置回读出现意外变化，模块尚未重启", firstCallModeDetail(readErr, parseErr, validationErr, readBack), backupPath)
			return
		}
	}
	if voiceNeedsChange {
		status = newCallModeStatus("enabling_voice", "正在启用模块 IMS/VoLTE")
		status.ADBAuthorizationAccepted = authorizationPerformed
		status.ADBEnabled = true
		status.UACEnabled = true
		status.BackupPath = backupPath
		status.Detail = "正在启用 IMS 并确认 VoLTE 未被禁用；写入后会完整回读。"
		a.setCallModeStatus(status)
		if call := a.currentCallState(); call.State != "idle" {
			a.failCallModeWithBackup("通话模式准备已安全停止", "检测到通话、呼叫或未知通话状态；IMS/VoLTE 配置尚未修改。", backupPath)
			return
		}
		if voiceErr := a.applyCallModeVoiceConfiguration(targetVoice); voiceErr != nil {
			a.failCallModeWithBackup("模块 IMS/VoLTE 配置未通过回读", voiceErr.Error(), backupPath)
			return
		}
	}
	if call := a.currentCallState(); call.State != "idle" {
		a.failCallModeWithBackup(
			"模块配置已保存，重启已暂停",
			"写入后检测到通话、呼叫或未知通话状态；请先结束呼叫并重新准备，下一次模块重启才会应用配置。",
			backupPath,
		)
		return
	}
	status = newCallModeStatus("restarting", "模块正在重启并重新枚举")
	status.ADBAuthorizationAccepted = authorizationPerformed
	status.ADBEnabled = true
	status.UACEnabled = true
	status.VoiceConfigured = true
	status.BackupPath = backupPath
	if forceRecoveryReboot {
		status.Detail = "ADB/UAC 与 IMS/VoLTE 配置已正确，但 ADB 没有响应；正在受控重启并重新验证。"
	} else {
		status.Detail = "网络、短信和模块音频接口会短暂中断，通常需要 20–60 秒恢复。"
	}
	a.setCallModeStatus(status)
	rebootResponse, rebootErr := a.runATCommand("AT+CFUN=1,1", 5*time.Second)
	if rebootErr == nil && callModeATResponseIsError(rebootResponse) {
		a.failCallModeWithBackup("模块拒绝重启命令", rebootResponse, backupPath)
		return
	}
	adbProbeNotBefore := time.Now().Add(callModeADBReenumerationSettleDelay)
	a.markUSBATDetached("call mode USB composition reboot")
	a.resetModuleVoiceState()
	status = newCallModeStatus("verifying_usb", "正在等待模块重连并验证接口")
	status.ADBAuthorizationAccepted = authorizationPerformed
	status.ADBEnabled = true
	status.UACEnabled = true
	status.VoiceConfigured = true
	status.BackupPath = backupPath
	status.Detail = "将核对同一模块、完整 USB 配置、IMS/VoLTE 回读和真实 ADB root 接口。"
	a.setCallModeStatus(status)

	var lastErr error
	postRebootAuthorizationComplete := !authorizationPerformed
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		check, err := a.runATCommand(`AT+QCFG="USBCFG"`, 4*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		composition, err := parseUSBComposition(check)
		if err != nil {
			lastErr = err
			continue
		}
		if !composition.equal(target) {
			lastErr = errors.New("模块已连接，但完整 USB 配置与目标值不一致")
			continue
		}
		voice, voiceErr := a.readCallModeVoiceConfiguration()
		if voiceErr != nil {
			lastErr = voiceErr
			continue
		}
		if !voice.ready() {
			lastErr = fmt.Errorf("模块已连接，但 IMS/VoLTE 尚未就绪：IMS=%d、VoLTE capability=%d、volte_disabled=%d", voice.IMS, voice.VoLTECapability, voice.VoLTEDisabled)
			continue
		}
		confirmedIdentity, identityErr := a.currentCallModeModuleIdentity()
		if identityErr != nil {
			lastErr = identityErr
			continue
		}
		if confirmedIdentity.IMEI != identity.IMEI {
			lastErr = errors.New("模块重连后的 IMEI 与配置前不一致")
			continue
		}
		if remaining := time.Until(adbProbeNotBefore); remaining > 0 {
			lastErr = fmt.Errorf("模块已重连，正在等待 ADB daemon 稳定（约 %.0f 秒）", remaining.Seconds())
			continue
		}
		if !postRebootAuthorizationComplete {
			status.Detail = "模块已重连；正在使用新的挑战值重新提交 ADB 授权，然后验证 root 通道。"
			status.ADBAuthorizationAccepted = true
			a.setCallModeStatus(status)
			if authorizationErr := a.submitCallModeADBAuthorization(); authorizationErr != nil {
				a.failCallModeWithBackup("模块重启后未接受 ADB 授权", authorizationErr.Error(), backupPath)
				return
			}
			postRebootAuthorizationComplete = true
			adbProbeNotBefore = time.Now().Add(2 * time.Second)
			lastErr = errors.New("模块已重新接受 ADB 授权，正在等待 daemon 应用")
			continue
		}
		if adbErr := a.probeModuleADB(); adbErr == nil {
			a.finishCallModeAfterUSB(backupPath)
			return
		} else {
			lastErr = fmt.Errorf("模块配置已保存，但 ADB root 接口尚未就绪：%w", adbErr)
			continue
		}
	}
	status = newCallModeStatus("needs_interface_recovery", "模块重连后仍未完成通话接口验证")
	status.Detail = firstCallModeDetail(lastErr, rebootErr)
	status.ADBEnabled = true
	status.UACEnabled = true
	status.VoiceConfigured = true
	status.RuntimeDownloaded = verifyModuleVoiceRuntime(a.runtimeDirectory()) == nil
	status.DownloadedBytes = verifiedRuntimeBytes(a.runtimeDirectory())
	status.CanEnable = true
	status.CanRetry = true
	status.RequiresUSBConfirmation = true
	status.BackupPath = backupPath
	a.setCallModeStatus(status)
}

func (a *app) finishCallModeAfterUSB(backupPath string) {
	if err := verifyModuleVoiceRuntime(a.runtimeDirectory()); err != nil {
		status := newCallModeStatus("needs_download", "需要下载模块侧语音运行时")
		status.ADBEnabled = true
		status.UACEnabled = true
		status.InterfacesReady = true
		status.VoiceConfigured = true
		status.Detail = "ADB、UAC 与 IMS/VoLTE 已完成真实验证。请选择下载渠道；下载后会自动校验、部署并执行一次语音路由自检。"
		status.CanDownload = true
		status.RequiresDownloadConfirmation = true
		status.BackupPath = backupPath
		a.setCallModeStatus(status)
		return
	}
	a.prepareDownloadedRuntime(backupPath, "")
}

func (a *app) callModeDownloadAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm bool   `json:"confirm"`
		Source  string `json:"source"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, "需要确认后才会下载第三方运行时")
		return
	}
	if body.Source != "github" && body.Source != "relay" {
		writeError(w, http.StatusBadRequest, "下载来源必须是 github 或 relay")
		return
	}
	inspection := a.inspectCallMode()
	if inspection.State != "needs_download" {
		a.setCallModeStatus(inspection)
		detail := inspection.Summary
		if strings.TrimSpace(inspection.Detail) != "" {
			detail += "：" + inspection.Detail
		}
		writeError(w, http.StatusConflict, detail)
		return
	}
	status := newCallModeStatus("downloading", "正在下载模块侧语音运行时")
	status.ADBEnabled = true
	status.UACEnabled = true
	status.InterfacesReady = true
	status.VoiceConfigured = true
	status.Source = body.Source
	if !a.beginCallModeOperation(status) {
		writeError(w, http.StatusConflict, "通话模式正在执行其他操作")
		return
	}
	go a.runCallModeDownload(body.Source)
	writeJSON(w, http.StatusAccepted, a.callModeSnapshot())
}

func (a *app) callModeRetryAPI(w http.ResponseWriter, _ *http.Request) {
	inspection := a.inspectCallMode()
	if inspection.State != "ready" {
		a.setCallModeStatus(inspection)
		writeJSON(w, http.StatusOK, a.callModeSnapshot())
		return
	}
	status := newCallModeStatus("preparing", "正在重新部署并验证模块侧语音运行时")
	status.ADBEnabled = true
	status.UACEnabled = true
	status.InterfacesReady = true
	status.VoiceConfigured = true
	status.RuntimeDownloaded = true
	status.DownloadedBytes = moduleVoiceTotalBytes()
	if !a.beginCallModeOperation(status) {
		writeError(w, http.StatusConflict, "通话模式正在执行其他操作")
		return
	}
	go func() {
		defer a.endCallModeOperation()
		a.resetModuleVoiceState()
		a.prepareDownloadedRuntime("", "")
	}()
	writeJSON(w, http.StatusAccepted, a.callModeSnapshot())
}

func (a *app) runCallModeDownload(source string) {
	defer a.endCallModeOperation()
	directory := a.runtimeDirectory()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		a.failCallMode("无法创建运行时目录", err.Error())
		return
	}
	var completed int64
	for _, file := range moduleVoiceFiles {
		if verifyModuleVoiceFile(directory, file) == nil {
			completed += file.Size
			a.updateCallModeDownloadProgress(source, file.Name, completed)
			continue
		}
		if err := a.downloadModuleVoiceFile(source, directory, file, completed); err != nil {
			a.failCallMode("下载运行时失败", err.Error())
			return
		}
		completed += file.Size
		a.updateCallModeDownloadProgress(source, file.Name, completed)
	}
	if err := verifyModuleVoiceRuntime(directory); err != nil {
		a.failCallMode("运行时完整性校验失败", err.Error())
		return
	}
	a.prepareDownloadedRuntime("", source)
}

func (a *app) prepareDownloadedRuntime(backupPath, source string) {
	status := newCallModeStatus("preparing", "正在部署并验证模块侧语音运行时")
	status.ADBEnabled = true
	status.UACEnabled = true
	status.InterfacesReady = true
	status.VoiceConfigured = true
	status.RuntimeDownloaded = true
	status.DownloadedBytes = moduleVoiceTotalBytes()
	status.Source = source
	status.BackupPath = backupPath
	a.setCallModeStatus(status)
	if err := a.prepareModuleVoiceRuntime(a.runtimeDirectory()); err != nil {
		a.failCallModeWithBackup("模块侧语音运行时准备失败", err.Error(), backupPath)
		return
	}
	if err := a.selfTestModuleVoiceRoute(a.runtimeDirectory()); err != nil {
		a.failCallModeWithBackup("模块通话音频路由自检失败", err.Error(), backupPath)
		return
	}
	status = newCallModeStatus("ready", "通话模式已就绪")
	status.ADBEnabled = true
	status.UACEnabled = true
	status.InterfacesReady = true
	status.VoiceConfigured = true
	status.RuntimeDownloaded = true
	status.DownloadedBytes = moduleVoiceTotalBytes()
	status.Source = source
	status.BackupPath = backupPath
	status.Detail = "ADB、UAC、IMS/VoLTE、QDC507 驱动与 D4/UAC 路由启停自检均已通过。"
	a.setCallModeStatus(status)
}

func (a *app) updateCallModeDownloadProgress(source, fileName string, bytes int64) {
	status := newCallModeStatus("downloading", "正在下载模块侧语音运行时")
	status.ADBEnabled = true
	status.UACEnabled = true
	status.InterfacesReady = true
	status.VoiceConfigured = true
	status.Source = source
	status.DownloadedBytes = bytes
	status.Detail = "已完成 " + fileName
	a.setCallModeStatus(status)
}

func (a *app) downloadModuleVoiceFile(source, directory string, expected moduleVoiceFile, completed int64) error {
	urls := moduleVoiceURLs(source, expected.Name)
	var failures []string
	for _, rawURL := range urls {
		if err := a.downloadModuleVoiceFileFromURL(rawURL, source, directory, expected, completed); err == nil {
			return nil
		} else {
			failures = append(failures, err.Error())
		}
	}
	return errors.New(strings.Join(failures, "；"))
}

func moduleVoiceURLs(source, name string) []string {
	original := moduleVoiceBaseURL + name
	if source == "relay" {
		return []string{"https://ghfast.top/" + original, "https://gh-proxy.com/" + original}
	}
	return []string{original}
}

func (a *app) downloadModuleVoiceFileFromURL(rawURL, source, directory string, expected moduleVoiceFile, completed int64) error {
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("下载重定向次数过多")
			}
			if req.URL.Scheme != "https" {
				return errors.New("运行时下载不允许跳转到非 HTTPS 地址")
			}
			return nil
		},
	}
	return a.downloadModuleVoiceFileWithClient(client, rawURL, source, directory, expected, completed)
}

func (a *app) downloadModuleVoiceFileWithClient(client *http.Client, rawURL, source, directory string, expected moduleVoiceFile, completed int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "DJOneHub/voice-runtime-downloader")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%s：%w", rawURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s：HTTP %d", rawURL, response.StatusCode)
	}
	if response.Request.URL.Scheme != "https" {
		return fmt.Errorf("%s：最终地址不是 HTTPS", rawURL)
	}
	if response.ContentLength >= 0 && response.ContentLength != expected.Size {
		return fmt.Errorf("%s：服务器返回大小 %d，预期 %d", expected.Name, response.ContentLength, expected.Size)
	}
	temporary, err := os.CreateTemp(directory, "."+expected.Name+"-*.part")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	reader := io.LimitReader(response.Body, expected.Size+1)
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash, &callModeProgressWriter{
		onWrite: func(current int64) {
			status := newCallModeStatus("downloading", "正在下载模块侧语音运行时")
			status.ADBEnabled = true
			status.UACEnabled = true
			status.InterfacesReady = true
			status.VoiceConfigured = true
			status.Source = source
			status.DownloadedBytes = completed + current
			status.Detail = "正在下载 " + expected.Name
			a.setCallModeStatus(status)
		},
	}), reader)
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("下载 %s：%w", expected.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("保存 %s：%w", expected.Name, closeErr)
	}
	if written != expected.Size {
		return fmt.Errorf("%s 下载大小为 %d，预期 %d", expected.Name, written, expected.Size)
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualHash, expected.SHA256) {
		return fmt.Errorf("%s 的 SHA-256 不匹配，已拒绝保存", expected.Name)
	}
	if err := os.Chmod(temporaryPath, expected.Mode); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, expected.Name)); err != nil {
		return err
	}
	return nil
}

type callModeProgressWriter struct {
	written int64
	onWrite func(int64)
	last    time.Time
}

func (w *callModeProgressWriter) Write(payload []byte) (int, error) {
	w.written += int64(len(payload))
	if w.onWrite != nil && (w.last.IsZero() || time.Since(w.last) >= 150*time.Millisecond) {
		w.onWrite(w.written)
		w.last = time.Now()
	}
	return len(payload), nil
}

func verifyModuleVoiceFile(directory string, expected moduleVoiceFile) error {
	path := filepath.Join(directory, expected.Name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.Size {
		return errors.New("文件不存在或大小不匹配")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected.SHA256) {
		return errors.New("SHA-256 不匹配")
	}
	return nil
}

func (a *app) callModeInterfacesReady() (bool, error) {
	response, err := a.runATCommand(`AT+QCFG="USBCFG"`, 4*time.Second)
	if err != nil {
		return false, err
	}
	composition, err := parseUSBComposition(response)
	if err != nil {
		return false, err
	}
	if !composition.hasADB() || !composition.hasUAC() {
		return false, nil
	}
	voice, err := a.readCallModeVoiceConfiguration()
	if err != nil {
		return false, err
	}
	if !voice.ready() {
		return false, fmt.Errorf(
			"IMS/VoLTE 尚未就绪：IMS=%d、VoLTE capability=%d、volte_disabled=%d",
			voice.IMS,
			voice.VoLTECapability,
			voice.VoLTEDisabled,
		)
	}
	if err := a.probeModuleADB(); err != nil {
		return false, err
	}
	return true, nil
}

func (a *app) callModeReadyForCall() error {
	a.callModeMu.RLock()
	operationRunning := a.callModeOperation
	a.callModeMu.RUnlock()
	if operationRunning {
		return errors.New("模块配置或通话模式正在变更，请等待操作完成")
	}
	ready, err := a.callModeInterfacesReady()
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("请先在通话页面开启通话模式")
	}
	if err := verifyModuleVoiceRuntime(a.runtimeDirectory()); err != nil {
		return errors.New("请先在通话页面下载并准备语音运行时")
	}
	if !a.isModuleVoicePrepared() {
		return errors.New("模块侧语音运行时尚未部署完成，请等待通话页面显示已就绪")
	}
	return nil
}

func (a *app) callAudioStartAPI(w http.ResponseWriter, _ *http.Request) {
	if err := a.callModeReadyForCall(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := a.startModuleVoiceRoute(a.runtimeDirectory()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"started": true, "runtime_version": moduleVoiceVersion})
}

func (a *app) callAudioStopAPI(w http.ResponseWriter, _ *http.Request) {
	if err := a.stopModuleVoiceRoute(a.runtimeDirectory()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
}

func (a *app) failCallMode(summary, detail string) {
	a.failCallModeWithBackup(summary, detail, "")
}

func (a *app) failCallModeWithBackup(summary, detail, backupPath string) {
	a.callModeMu.RLock()
	previous := a.callMode
	a.callModeMu.RUnlock()
	status := newCallModeStatus("failed", summary)
	status.Detail = detail
	if previous.ADBAuthorizationAccepted {
		status.Detail = "模块已接受 ADB 持久授权；USB 配置备份只能关闭接口，不能保证撤销授权。\n" + detail
	}
	status.CanRetry = true
	status.BackupPath = firstNonEmptyCallMode(backupPath, previous.BackupPath)
	status.ADBEnabled = previous.ADBEnabled
	status.InterfacesReady = previous.InterfacesReady
	status.ADBAuthorizationAccepted = previous.ADBAuthorizationAccepted
	status.UACEnabled = previous.UACEnabled
	status.VoiceConfigured = previous.VoiceConfigured
	status.Source = previous.Source
	status.RuntimeDownloaded = verifyModuleVoiceRuntime(a.runtimeDirectory()) == nil
	status.DownloadedBytes = verifiedRuntimeBytes(a.runtimeDirectory())
	a.setCallModeStatus(status)
}

func firstNonEmptyCallMode(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func callModeATResponseIsError(response string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(response, "\r", "\n"), "\n") {
		line = strings.ToUpper(strings.TrimSpace(line))
		if line == "ERROR" || strings.HasPrefix(line, "+CME ERROR:") || strings.HasPrefix(line, "+CMS ERROR:") {
			return true
		}
	}
	return false
}

func callModeATResponseSucceeded(response string) bool {
	if callModeATResponseIsError(response) {
		return false
	}
	for _, line := range strings.Split(strings.ReplaceAll(response, "\r", "\n"), "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "OK") {
			return true
		}
	}
	return false
}

func (a *app) callModeATCommandAccepted(response string, err error) bool {
	if err != nil || callModeATResponseIsError(response) {
		return false
	}
	// Manager.ExecuteAT only returns after a terminal OK and intentionally
	// strips that terminator from the response. Direct USB AT returns the raw
	// terminal line, so it must still be present there.
	if a.modem != nil {
		return true
	}
	return callModeATResponseSucceeded(response)
}

func callModeSensitiveATFailureDetail(err error, response string) string {
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		return "AT 交互未返回可确认结果；模块可能已经接受持久授权。USB 配置备份只能关闭接口，不能保证撤销授权。技术原因：" + err.Error()
	}
	if callModeATResponseIsError(response) {
		return "模块明确返回 ERROR；授权未确认，且授权密码未写入日志或错误详情"
	}
	return "模块没有返回可确认的 OK；模块可能已经接受持久授权。USB 配置备份只能关闭接口，不能保证撤销授权；授权密码未写入日志或错误详情。"
}

func firstCallModeDetail(values ...any) string {
	for _, value := range values {
		switch typed := value.(type) {
		case error:
			if typed != nil && strings.TrimSpace(typed.Error()) != "" {
				return typed.Error()
			}
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		}
	}
	return "未知错误"
}
