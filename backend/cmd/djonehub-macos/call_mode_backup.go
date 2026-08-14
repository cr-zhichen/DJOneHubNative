package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	callModeBackupSchemaVersion               = 3
	callModeBackupPreviousSchemaVersion       = 2
	callModeBackupKind                        = "djonehub-usb-composition-backup"
	callModeBackupReasonBeforeCallMode        = "before_call_mode"
	callModeBackupReasonBeforeRestore         = "before_restore"
	callModeBackupMaximumSize           int64 = 64 << 10
)

type callModeModuleIdentity struct {
	IMEI     string `json:"imei"`
	Firmware string `json:"firmware,omitempty"`
}

type callModeUSBBackup struct {
	SchemaVersion  int                         `json:"schema_version,omitempty"`
	Kind           string                      `json:"kind,omitempty"`
	Reason         string                      `json:"reason,omitempty"`
	SavedAt        time.Time                   `json:"saved_at"`
	Module         callModeModuleIdentity      `json:"module,omitempty"`
	USB            usbComposition              `json:"usb"`
	Voice          *callModeVoiceConfiguration `json:"voice,omitempty"`
	RestoreCommand string                      `json:"restore_command"`
}

type callModeUSBBackupSummary struct {
	ID            string    `json:"id"`
	FileName      string    `json:"file_name"`
	SchemaVersion int       `json:"schema_version"`
	SavedAt       time.Time `json:"saved_at"`
	Reason        string    `json:"reason"`
	ModuleIMEI    string    `json:"module_imei,omitempty"`
	Firmware      string    `json:"firmware,omitempty"`
	VendorID      string    `json:"vendor_id,omitempty"`
	ProductID     string    `json:"product_id,omitempty"`
	Flags         []int     `json:"flags,omitempty"`
	ADBEnabled    bool      `json:"adb_enabled"`
	UACEnabled    bool      `json:"uac_enabled"`
	VoiceIncluded bool      `json:"voice_included"`
	IMSConfigured bool      `json:"ims_configured"`
	Imported      bool      `json:"imported"`
	Valid         bool      `json:"valid"`
	Restorable    bool      `json:"restorable"`
	Detail        string    `json:"detail,omitempty"`
}

type callModeRestoreNotice struct {
	BackupID      string    `json:"backup_id"`
	BackupSavedAt time.Time `json:"backup_saved_at"`
	RestoredAt    time.Time `json:"restored_at"`
	SafetyBackup  string    `json:"safety_backup_id,omitempty"`
	Changed       bool      `json:"changed"`
}

func (a *app) callModeBackupDirectory() string {
	base := a.dataDir
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "DJOneHubNative")
	}
	return filepath.Join(base, "module-backups")
}

func (a *app) currentCallModeModuleIdentity() (callModeModuleIdentity, error) {
	response, err := a.runATCommand("AT+CGSN", 4*time.Second)
	if err != nil {
		return callModeModuleIdentity{}, fmt.Errorf("读取模块 IMEI：%w", err)
	}
	imei := parseUSBATIMEI(response)
	if !validCallModeIMEI(imei) {
		return callModeModuleIdentity{}, errors.New("模块没有返回可识别的 15 位 IMEI")
	}
	firmwareResponse, _ := a.runATCommand("ATI", 4*time.Second)
	return callModeModuleIdentity{IMEI: imei, Firmware: parseUSBATFirmware(firmwareResponse)}, nil
}

func validCallModeIMEI(value string) bool {
	if len(value) != 15 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (a *app) saveCallModeUSBBackup(composition usbComposition, reason string) (string, error) {
	identity, err := a.currentCallModeModuleIdentity()
	if err != nil {
		return "", err
	}
	voice, err := a.readCallModeVoiceConfiguration()
	if err != nil {
		return "", err
	}
	return a.saveCallModeUSBBackupWithIdentity(composition, voice, identity, reason)
}

func (a *app) saveCallModeUSBBackupWithIdentity(composition usbComposition, voice callModeVoiceConfiguration, identity callModeModuleIdentity, reason string) (string, error) {
	voiceCopy := voice
	backup := callModeUSBBackup{
		SchemaVersion:  callModeBackupSchemaVersion,
		Kind:           callModeBackupKind,
		Reason:         reason,
		SavedAt:        time.Now(),
		Module:         identity,
		USB:            composition,
		Voice:          &voiceCopy,
		RestoreCommand: composition.command(),
	}
	return writeCallModeUSBBackup(a.callModeBackupDirectory(), backup)
}

func writeCallModeUSBBackup(directory string, backup callModeUSBBackup) (string, error) {
	if err := validateCallModeUSBBackup(backup, true); err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return "", err
	}
	payload = append(payload, '\n')
	reasonName := "call-mode"
	if backup.Reason == callModeBackupReasonBeforeRestore {
		reasonName = "restore"
	}
	imeiSuffix := backup.Module.IMEI[len(backup.Module.IMEI)-4:]
	fileName := fmt.Sprintf(
		"usb-before-%s-%s-%s.json",
		reasonName,
		backup.SavedAt.Format("20060102-150405.000000000"),
		imeiSuffix,
	)
	path := filepath.Join(directory, fileName)
	temporary, err := os.CreateTemp(directory, ".usb-backup-*.tmp")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", err
	}
	return path, nil
}

func validateCallModeUSBBackup(backup callModeUSBBackup, requireRestorable bool) error {
	if backup.SavedAt.IsZero() {
		return errors.New("备份缺少保存时间")
	}
	if err := backup.USB.validate(); err != nil {
		return fmt.Errorf("备份 USB 配置无效：%w", err)
	}
	if backup.RestoreCommand != backup.USB.command() {
		return errors.New("备份中的还原命令与结构化 USB 配置不一致")
	}
	if backup.SchemaVersion == 0 {
		if requireRestorable {
			return errors.New("旧版备份不包含模块身份，只能导出，不能自动还原")
		}
		return nil
	}
	if (backup.SchemaVersion != callModeBackupSchemaVersion && backup.SchemaVersion != callModeBackupPreviousSchemaVersion) || backup.Kind != callModeBackupKind {
		return fmt.Errorf("不支持的备份格式版本 %d", backup.SchemaVersion)
	}
	if backup.Reason != callModeBackupReasonBeforeCallMode && backup.Reason != callModeBackupReasonBeforeRestore {
		return errors.New("备份原因字段无效")
	}
	if !validCallModeIMEI(backup.Module.IMEI) {
		return errors.New("备份缺少有效的模块 IMEI")
	}
	if backup.SchemaVersion == callModeBackupSchemaVersion {
		if backup.Voice == nil {
			return errors.New("备份缺少 IMS/VoLTE 配置")
		}
		if err := backup.Voice.validate(); err != nil {
			return fmt.Errorf("备份 IMS/VoLTE 配置无效：%w", err)
		}
	}
	return nil
}

func validateCallModeBackupForModule(backup callModeUSBBackup, identity callModeModuleIdentity, current usbComposition) error {
	if err := validateCallModeUSBBackup(backup, true); err != nil {
		return err
	}
	if !validCallModeIMEI(identity.IMEI) || identity.IMEI != backup.Module.IMEI {
		return errors.New("备份所属模块与当前连接模块的 IMEI 不一致")
	}
	if current.VendorID != backup.USB.VendorID || current.ProductID != backup.USB.ProductID {
		return errors.New("备份与当前模块的 USB VID/PID 不一致")
	}
	return nil
}

func validCallModeBackupID(id string) bool {
	if id == "" || len(id) > 220 || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return false
	}
	if !strings.HasSuffix(id, ".json") ||
		(!strings.HasPrefix(id, "usb-before-call-mode-") &&
			!strings.HasPrefix(id, "usb-before-restore-") &&
			!strings.HasPrefix(id, "usb-imported-")) {
		return false
	}
	for _, character := range id {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("-._", character) {
			continue
		}
		return false
	}
	return true
}

func readCallModeBackupFile(directory, id string) ([]byte, os.FileInfo, error) {
	if !validCallModeBackupID(id) {
		return nil, nil, errors.New("备份文件标识无效")
	}
	path := filepath.Join(directory, id)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > callModeBackupMaximumSize {
		return nil, nil, errors.New("备份文件类型或大小无效")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return payload, info, nil
}

func decodeCallModeUSBBackup(payload []byte) (callModeUSBBackup, error) {
	var backup callModeUSBBackup
	if err := json.Unmarshal(payload, &backup); err != nil {
		return callModeUSBBackup{}, err
	}
	return backup, nil
}

func callModeBackupSummary(id string, info os.FileInfo, payload []byte) callModeUSBBackupSummary {
	summary := callModeUSBBackupSummary{ID: id, FileName: id, SavedAt: info.ModTime()}
	backup, err := decodeCallModeUSBBackup(payload)
	if err != nil {
		summary.Detail = "无法解析备份：" + err.Error()
		return summary
	}
	summary.SchemaVersion = backup.SchemaVersion
	summary.SavedAt = backup.SavedAt
	summary.Reason = backup.Reason
	summary.ModuleIMEI = backup.Module.IMEI
	summary.Firmware = backup.Module.Firmware
	summary.VendorID = fmt.Sprintf("0x%04X", backup.USB.VendorID)
	summary.ProductID = fmt.Sprintf("0x%04X", backup.USB.ProductID)
	summary.Flags = append([]int(nil), backup.USB.Flags...)
	summary.ADBEnabled = backup.USB.hasADB()
	summary.UACEnabled = backup.USB.hasUAC()
	summary.VoiceIncluded = backup.Voice != nil
	summary.IMSConfigured = backup.Voice != nil && backup.Voice.ready()
	summary.Imported = strings.HasPrefix(id, "usb-imported-")
	if err := validateCallModeUSBBackup(backup, false); err != nil {
		summary.Detail = err.Error()
		return summary
	}
	summary.Valid = true
	if backup.SchemaVersion == 0 {
		summary.Detail = "旧版备份不包含模块身份，仅支持导出保存"
		return summary
	}
	summary.Restorable = true
	if backup.SchemaVersion == callModeBackupPreviousSchemaVersion {
		summary.Detail = "此备份只包含 USB 配置；还原时不会更改 IMS/VoLTE。"
	}
	return summary
}

func (a *app) listCallModeUSBBackups() ([]callModeUSBBackupSummary, error) {
	directory := a.callModeBackupDirectory()
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []callModeUSBBackupSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	backups := make([]callModeUSBBackupSummary, 0, len(entries))
	for _, entry := range entries {
		id := entry.Name()
		if !validCallModeBackupID(id) {
			continue
		}
		payload, info, err := readCallModeBackupFile(directory, id)
		if err != nil {
			continue
		}
		backups = append(backups, callModeBackupSummary(id, info, payload))
	}
	sort.SliceStable(backups, func(left, right int) bool {
		return backups[left].SavedAt.After(backups[right].SavedAt)
	})
	return backups, nil
}

func (a *app) callModeBackupsAPI(w http.ResponseWriter, _ *http.Request) {
	backups, err := a.listCallModeUSBBackups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取模块配置备份失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": backups})
}

func (a *app) callModeBackupExportAPI(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	payload, info, err := readCallModeBackupFile(a.callModeBackupDirectory(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "读取备份失败："+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, id))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, id, info.ModTime(), bytes.NewReader(payload))
}

func writeImportedCallModeUSBBackup(directory string, payload []byte) (string, error) {
	if len(payload) == 0 || int64(len(payload)) > callModeBackupMaximumSize {
		return "", errors.New("备份文件为空或超过 64 KB")
	}
	backup, err := decodeCallModeUSBBackup(payload)
	if err != nil {
		return "", fmt.Errorf("解析备份：%w", err)
	}
	if err := validateCallModeUSBBackup(backup, false); err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}

	identitySuffix := "legacy"
	if validCallModeIMEI(backup.Module.IMEI) {
		identitySuffix = backup.Module.IMEI[len(backup.Module.IMEI)-4:]
	}
	baseName := fmt.Sprintf(
		"usb-imported-%s-%s",
		time.Now().Format("20060102-150405.000000000"),
		identitySuffix,
	)
	temporary, err := os.CreateTemp(directory, ".usb-import-*.tmp")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}

	for index := 1; index <= 100; index++ {
		fileName := baseName + ".json"
		if index > 1 {
			fileName = fmt.Sprintf("%s-%d.json", baseName, index)
		}
		path := filepath.Join(directory, fileName)
		if err := os.Link(temporaryPath, path); err == nil {
			return path, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("无法为导入备份分配唯一文件名")
}

func (a *app) callModeBackupImportAPI(w http.ResponseWriter, r *http.Request) {
	reader := http.MaxBytesReader(w, r.Body, callModeBackupMaximumSize)
	payload, err := io.ReadAll(reader)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取导入备份失败："+err.Error())
		return
	}
	path, err := writeImportedCallModeUSBBackup(a.callModeBackupDirectory(), payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "导入备份失败："+err.Error())
		return
	}
	storedPayload, info, err := readCallModeBackupFile(a.callModeBackupDirectory(), filepath.Base(path))
	if err != nil {
		_ = os.Remove(path)
		writeError(w, http.StatusInternalServerError, "验证导入备份失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"backup": callModeBackupSummary(filepath.Base(path), info, storedPayload),
	})
}

func (a *app) callModeBackupDeleteAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm  bool   `json:"confirm"`
		BackupID string `json:"backup_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, "需要确认后才会删除配置备份")
		return
	}
	body.BackupID = strings.TrimSpace(body.BackupID)
	if _, _, err := readCallModeBackupFile(a.callModeBackupDirectory(), body.BackupID); err != nil {
		writeError(w, http.StatusNotFound, "读取待删除备份失败："+err.Error())
		return
	}
	a.callModeMu.RLock()
	operationRunning := a.callModeOperation
	a.callModeMu.RUnlock()
	if operationRunning {
		writeError(w, http.StatusConflict, "通话模式正在执行其他操作，不能删除配置备份")
		return
	}
	if err := os.Remove(filepath.Join(a.callModeBackupDirectory(), body.BackupID)); err != nil {
		writeError(w, http.StatusInternalServerError, "删除配置备份失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":   true,
		"backup_id": body.BackupID,
	})
}

func (a *app) callModeRestoreAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm  bool   `json:"confirm"`
		BackupID string `json:"backup_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, "需要确认后才会还原模块 USB 配置")
		return
	}
	body.BackupID = strings.TrimSpace(body.BackupID)
	payload, _, err := readCallModeBackupFile(a.callModeBackupDirectory(), body.BackupID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取备份失败："+err.Error())
		return
	}
	backup, err := decodeCallModeUSBBackup(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "解析备份失败："+err.Error())
		return
	}
	if err := validateCallModeUSBBackup(backup, true); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	call := a.currentCallState()
	if call.State != "idle" {
		writeError(w, http.StatusConflict, "通话或呼叫进行中，不能还原模块 USB 配置")
		return
	}
	status := newCallModeStatus("restoring_usb", "正在核对备份与当前模块")
	status.BackupPath = filepath.Join(a.callModeBackupDirectory(), body.BackupID)
	if !a.beginCallModeOperation(status) {
		writeError(w, http.StatusConflict, "通话模式正在执行其他操作")
		return
	}
	go a.runCallModeRestore(body.BackupID)
	writeJSON(w, http.StatusAccepted, a.callModeSnapshot())
}

func (a *app) runCallModeRestore(backupID string) {
	defer a.endCallModeOperation()
	directory := a.callModeBackupDirectory()
	payload, _, err := readCallModeBackupFile(directory, backupID)
	if err != nil {
		a.failCallMode("读取还原备份失败", err.Error())
		return
	}
	backup, err := decodeCallModeUSBBackup(payload)
	if err != nil {
		a.failCallMode("解析还原备份失败", err.Error())
		return
	}
	if call := a.currentCallState(); call.State != "idle" {
		a.failCallMode("无法还原模块配置", "检测到通话或呼叫状态，已停止还原")
		return
	}
	identity, err := a.currentCallModeModuleIdentity()
	if err != nil {
		a.failCallMode("无法确认当前模块身份", err.Error())
		return
	}
	response, err := a.runATCommand(`AT+QCFG="USBCFG"`, 5*time.Second)
	if err != nil {
		a.failCallMode("无法读取当前 USB 配置", err.Error())
		return
	}
	current, err := parseUSBComposition(response)
	if err != nil {
		a.failCallMode("无法识别当前 USB 配置", err.Error())
		return
	}
	if err := current.validate(); err != nil {
		a.failCallMode("当前 USB 配置不能安全修改", err.Error())
		return
	}
	currentVoice, err := a.readCallModeVoiceConfiguration()
	if err != nil {
		a.failCallMode("无法读取当前 IMS/VoLTE 配置", err.Error())
		return
	}
	if err := validateCallModeBackupForModule(backup, identity, current); err != nil {
		a.failCallMode("备份与当前模块不匹配", err.Error())
		return
	}
	backupPath := filepath.Join(directory, backupID)
	usbChanged := current.command() != backup.USB.command()
	voiceChanged := backup.Voice != nil && !currentVoice.sameWritableValues(*backup.Voice)
	if !usbChanged && !voiceChanged {
		a.finishCallModeRestore(backupID, backupPath, backup, "", false)
		return
	}
	a.moduleVoiceMu.Lock()
	routeActive := a.moduleVoiceRoute
	a.moduleVoiceMu.Unlock()
	if routeActive {
		if err := a.stopModuleVoiceRoute(a.runtimeDirectory()); err != nil {
			a.failCallMode("无法停止当前通话音频路由", err.Error())
			return
		}
	}
	safetyPath, err := a.saveCallModeUSBBackupWithIdentity(current, currentVoice, identity, callModeBackupReasonBeforeRestore)
	if err != nil {
		a.failCallMode("无法保存还原前保护备份", err.Error())
		return
	}
	if call := a.currentCallState(); call.State != "idle" {
		a.failCallModeWithBackup("还原已安全停止", "写入前检测到新的通话或呼叫状态，模块配置尚未修改", safetyPath)
		return
	}
	status := newCallModeStatus("restoring_usb", "正在还原模块 USB 配置")
	status.ADBEnabled = current.hasADB()
	status.UACEnabled = current.hasUAC()
	status.RuntimeDownloaded = verifyModuleVoiceRuntime(a.runtimeDirectory()) == nil
	status.BackupPath = backupPath
	status.InterfacesReady = current.hasADB() && current.hasUAC() && currentVoice.ready()
	status.VoiceConfigured = currentVoice.ready()
	status.Detail = "已自动保存当前 USB 与 IMS/VoLTE 配置，正在写入所选备份并进行完整回读。"
	a.setCallModeStatus(status)
	if usbChanged {
		writeResponse, writeErr := a.runATCommand(backup.USB.command(), 8*time.Second)
		if !a.callModeATCommandAccepted(writeResponse, writeErr) {
			a.failCallModeWithBackup("模块拒绝还原 USB 配置", firstCallModeDetail(writeErr, writeResponse), safetyPath)
			return
		}
		readBack, readErr := a.runATCommand(`AT+QCFG="USBCFG"`, 5*time.Second)
		actual, parseErr := parseUSBComposition(readBack)
		var validationErr error
		if readErr == nil && parseErr == nil {
			validationErr = validateUSBReadBackBeforeReboot(current, backup.USB, actual)
		}
		if readErr != nil || parseErr != nil || validationErr != nil {
			a.failCallModeWithBackup("还原 USB 配置回读出现意外变化，模块尚未重启", firstCallModeDetail(readErr, parseErr, validationErr, readBack), safetyPath)
			return
		}
	}
	if voiceChanged {
		if voiceErr := a.applyCallModeVoiceConfiguration(*backup.Voice); voiceErr != nil {
			a.failCallModeWithBackup("还原 IMS/VoLTE 配置回读失败，模块尚未重启", voiceErr.Error(), safetyPath)
			return
		}
	}
	status = newCallModeStatus("restarting_restore", "模块正在重启并应用还原配置")
	status.ADBEnabled = backup.USB.hasADB()
	status.UACEnabled = backup.USB.hasUAC()
	status.VoiceConfigured = backup.Voice == nil && currentVoice.ready() || backup.Voice != nil && backup.Voice.ready()
	status.RuntimeDownloaded = verifyModuleVoiceRuntime(a.runtimeDirectory()) == nil
	status.BackupPath = backupPath
	status.Detail = "网络、短信、ADB、IMS/VoLTE 和模块音频接口会按备份重新枚举，通常需要 20–60 秒。"
	a.setCallModeStatus(status)
	rebootResponse, rebootErr := a.runATCommand("AT+CFUN=1,1", 5*time.Second)
	if rebootErr == nil && callModeATResponseIsError(rebootResponse) {
		a.failCallModeWithBackup("模块拒绝重启命令", rebootResponse, safetyPath)
		return
	}
	a.markUSBATDetached("call mode USB composition restore")
	a.resetModuleVoiceState()

	var lastErr error
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
		if composition.command() != backup.USB.command() {
			lastErr = errors.New("模块已连接，但 USB 配置尚未恢复为备份值")
			continue
		}
		if backup.Voice != nil {
			voice, voiceErr := a.readCallModeVoiceConfiguration()
			if voiceErr != nil {
				lastErr = voiceErr
				continue
			}
			if !voice.sameWritableValues(*backup.Voice) {
				lastErr = fmt.Errorf("模块已连接，但 IMS/VoLTE 配置尚未恢复为备份值：IMS=%d、volte_disabled=%d", voice.IMS, voice.VoLTEDisabled)
				continue
			}
		}
		confirmedIdentity, err := a.currentCallModeModuleIdentity()
		if err != nil {
			lastErr = err
			continue
		}
		if confirmedIdentity.IMEI != backup.Module.IMEI {
			lastErr = errors.New("模块重连后的 IMEI 与备份不一致")
			continue
		}
		a.finishCallModeRestore(backupID, backupPath, backup, safetyPath, true)
		return
	}
	a.failCallModeWithBackup("等待还原后的模块重新连接超时", firstCallModeDetail(lastErr, rebootErr), safetyPath)
}

func (a *app) finishCallModeRestore(backupID, backupPath string, backup callModeUSBBackup, safetyPath string, changed bool) {
	status := a.inspectCallMode()
	status.BackupPath = backupPath
	status.LastRestore = &callModeRestoreNotice{
		BackupID:      backupID,
		BackupSavedAt: backup.SavedAt,
		RestoredAt:    time.Now(),
		SafetyBackup:  filepath.Base(safetyPath),
		Changed:       changed,
	}
	if changed {
		if backup.Voice != nil {
			status.Detail = "原始 USB 与 IMS/VoLTE 配置已还原并回读确认；还原前的配置也已自动保存为保护备份。"
		} else {
			status.Detail = "旧版备份中的 USB 配置已还原并回读确认；该备份不包含 IMS/VoLTE，相关配置未改变。"
		}
	} else {
		status.Detail = "当前模块配置已经与所选备份一致，没有写入或重启模块。"
	}
	a.setCallModeStatus(status)
}
