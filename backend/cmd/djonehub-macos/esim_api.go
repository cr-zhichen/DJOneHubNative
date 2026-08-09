package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/iniwex5/vohive/internal/esim"
)

const (
	esimCardTypeEUICC        = "euicc"
	esimCardTypePhysicalSIM  = "physical_sim"
	esimOperationStateQueued = "queued"
	esimOperationStateRun    = "running"
	esimOperationStateOK     = "succeeded"
	esimOperationStateWarn   = "warning"
	esimOperationStateFailed = "failed"
)

type esimCapabilities struct {
	CanRefresh            bool `json:"can_refresh"`
	CanDownload           bool `json:"can_download"`
	CanSwitch             bool `json:"can_switch"`
	CanRename             bool `json:"can_rename"`
	CanDelete             bool `json:"can_delete"`
	CanProbePhonebook     bool `json:"can_probe_phonebook"`
	VendorSerialAvailable bool `json:"vendor_serial_available"`
}

type esimOverviewResponse struct {
	CardType     string                 `json:"card_type"`
	Message      string                 `json:"message,omitempty"`
	ChipInfo     *esim.EUICCChipInfo    `json:"chip_info,omitempty"`
	Profiles     []esim.EUICCProfiles   `json:"profiles"`
	Capabilities esimCapabilities       `json:"capabilities"`
	UpdatedAt    *time.Time             `json:"updated_at,omitempty"`
	Operation    *esimOperationSnapshot `json:"operation,omitempty"`
}

type esimOperationResult struct {
	Warning     string           `json:"warning,omitempty"`
	WarningCode string           `json:"warning_code,omitempty"`
	SpaceDelta  *esim.SpaceDelta `json:"space_delta,omitempty"`
}

type esimOperationSnapshot struct {
	ID                  string               `json:"id"`
	Kind                string               `json:"kind"`
	State               string               `json:"state"`
	Step                string               `json:"step,omitempty"`
	Message             string               `json:"message,omitempty"`
	Progress            int                  `json:"progress"`
	TargetICCID         string               `json:"target_iccid,omitempty"`
	ErrorCode           string               `json:"error_code,omitempty"`
	Error               string               `json:"error,omitempty"`
	Recoverable         bool                 `json:"recoverable,omitempty"`
	RefreshAfterSeconds int                  `json:"refresh_after_seconds,omitempty"`
	Result              *esimOperationResult `json:"result,omitempty"`
	StartedAt           time.Time            `json:"started_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
	FinishedAt          *time.Time           `json:"finished_at,omitempty"`
}

func cloneESIMOperation(in *esimOperationSnapshot) *esimOperationSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	if in.Result != nil {
		result := *in.Result
		if in.Result.SpaceDelta != nil {
			delta := *in.Result.SpaceDelta
			result.SpaceDelta = &delta
		}
		out.Result = &result
	}
	if in.FinishedAt != nil {
		finished := *in.FinishedAt
		out.FinishedAt = &finished
	}
	return &out
}

func (a *app) currentESIMOperation() *esimOperationSnapshot {
	if a == nil {
		return nil
	}
	a.esimOperationMu.RLock()
	defer a.esimOperationMu.RUnlock()
	return cloneESIMOperation(a.esimOperation)
}

func isActiveESIMOperation(operation *esimOperationSnapshot) bool {
	return operation != nil && (operation.State == esimOperationStateQueued || operation.State == esimOperationStateRun)
}

func (a *app) beginESIMOperation(kind, message, targetICCID string) (*esimOperationSnapshot, bool) {
	if a == nil {
		return nil, false
	}
	now := time.Now()
	a.esimOperationMu.Lock()
	defer a.esimOperationMu.Unlock()
	if isActiveESIMOperation(a.esimOperation) {
		return cloneESIMOperation(a.esimOperation), false
	}
	a.esimOperation = &esimOperationSnapshot{
		ID:          fmt.Sprintf("%d", now.UnixNano()),
		Kind:        strings.TrimSpace(kind),
		State:       esimOperationStateQueued,
		Step:        "queued",
		Message:     strings.TrimSpace(message),
		Progress:    0,
		TargetICCID: strings.TrimSpace(targetICCID),
		StartedAt:   now,
		UpdatedAt:   now,
	}
	return cloneESIMOperation(a.esimOperation), true
}

func (a *app) updateESIMOperation(id, state, step, message string, progress int) {
	if a == nil {
		return
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	a.esimOperationMu.Lock()
	defer a.esimOperationMu.Unlock()
	if a.esimOperation == nil || a.esimOperation.ID != id {
		return
	}
	if strings.TrimSpace(state) != "" {
		a.esimOperation.State = state
	}
	if strings.TrimSpace(step) != "" {
		a.esimOperation.Step = step
	}
	if strings.TrimSpace(message) != "" {
		a.esimOperation.Message = message
	}
	a.esimOperation.Progress = progress
	a.esimOperation.UpdatedAt = time.Now()
}

func (a *app) finishESIMOperation(id, message string, result *esimOperationResult, refreshAfterSeconds int) {
	if a == nil {
		return
	}
	now := time.Now()
	a.esimOperationMu.Lock()
	defer a.esimOperationMu.Unlock()
	if a.esimOperation == nil || a.esimOperation.ID != id {
		return
	}
	state := esimOperationStateOK
	if result != nil && strings.TrimSpace(result.WarningCode) != "" {
		state = esimOperationStateWarn
	}
	a.esimOperation.State = state
	a.esimOperation.Step = "done"
	a.esimOperation.Message = strings.TrimSpace(message)
	a.esimOperation.Progress = 100
	a.esimOperation.Result = result
	a.esimOperation.RefreshAfterSeconds = refreshAfterSeconds
	a.esimOperation.Error = ""
	a.esimOperation.ErrorCode = ""
	a.esimOperation.Recoverable = false
	a.esimOperation.UpdatedAt = now
	a.esimOperation.FinishedAt = &now
}

func (a *app) failESIMOperation(id, code, message string, recoverable bool) {
	if a == nil {
		return
	}
	now := time.Now()
	a.esimOperationMu.Lock()
	defer a.esimOperationMu.Unlock()
	if a.esimOperation == nil || a.esimOperation.ID != id {
		return
	}
	a.esimOperation.State = esimOperationStateFailed
	a.esimOperation.Step = "failed"
	a.esimOperation.Message = ""
	a.esimOperation.ErrorCode = strings.TrimSpace(code)
	a.esimOperation.Error = strings.TrimSpace(message)
	a.esimOperation.Recoverable = recoverable
	a.esimOperation.UpdatedAt = now
	a.esimOperation.FinishedAt = &now
}

func (a *app) rejectIfESIMOperationActive(w http.ResponseWriter) bool {
	operation := a.currentESIMOperation()
	if !isActiveESIMOperation(operation) {
		return false
	}
	writeCodedError(w, http.StatusConflict, "esim_operation_busy", "已有 eSIM 操作正在进行，请等待完成后重试", true)
	return true
}

func (a *app) esimCapabilities(overview *esim.EsimOverview, switchAllowed bool) esimCapabilities {
	available := overview != nil && overview.ChipInfo != nil && len(overview.ChipInfo.EIDs) > 0
	serialAvailable := available && strings.TrimSpace(overview.ChipInfo.SerialNumber) != ""
	return esimCapabilities{
		CanRefresh:            available,
		CanDownload:           available,
		CanSwitch:             available && (switchAllowed || a.modem != nil),
		CanRename:             available,
		CanDelete:             available,
		CanProbePhonebook:     available,
		VendorSerialAvailable: serialAvailable,
	}
}

func physicalSIMOverviewResponse(message string, operation *esimOperationSnapshot) esimOverviewResponse {
	return esimOverviewResponse{
		CardType:     esimCardTypePhysicalSIM,
		Message:      strings.TrimSpace(message),
		Profiles:     []esim.EUICCProfiles{},
		Capabilities: esimCapabilities{},
		Operation:    operation,
	}
}

type codedErrorPayload struct {
	Error       string `json:"error"`
	Code        string `json:"code,omitempty"`
	Recoverable bool   `json:"recoverable,omitempty"`
}

func writeCodedError(w http.ResponseWriter, status int, code, message string, recoverable bool) {
	writeJSON(w, status, codedErrorPayload{
		Error:       strings.TrimSpace(message),
		Code:        strings.TrimSpace(code),
		Recoverable: recoverable,
	})
}

func writeDeleteProfileError(w http.ResponseWriter, err error) {
	code := esim.ClassifyDeleteProfileError(err)
	switch code {
	case esim.DeleteProfileErrorInvalidICCID, esim.DeleteProfileErrorInvalidAIDHex:
		writeCodedError(w, http.StatusBadRequest, strings.ToLower(string(code)), err.Error(), false)
	case esim.DeleteProfileErrorProfileNotFound, esim.DeleteProfileErrorEUICCNotFound:
		writeCodedError(w, http.StatusNotFound, strings.ToLower(string(code)), err.Error(), false)
	case esim.DeleteProfileErrorBusy:
		writeCodedError(w, http.StatusConflict, "esim_operation_busy", "已有 eSIM 操作正在进行，请稍后重试", true)
	default:
		writeCodedError(w, http.StatusBadGateway, "delete_profile_failed", "删除 Profile 失败，请刷新卡片状态后重试", true)
	}
}

func classifyDownloadOperationError(err error) (code, message string, recoverable bool) {
	if err == nil {
		return "", "", false
	}
	if errors.Is(err, esim.ErrOperationInProgress) {
		return "esim_operation_busy", "已有 eSIM 操作正在进行，请稍后重试", true
	}
	var downloadErr *esim.DownloadProfileError
	if errors.As(err, &downloadErr) && downloadErr != nil {
		code = downloadErr.Code
		message = downloadErr.Error()
		switch code {
		case esim.DownloadErrorEUICCIccidAlreadyExists, esim.DownloadErrorEUICCPPRNotAllowed:
			return code, message, false
		case esim.DownloadErrorGeneric:
			return code, "下载 Profile 失败，请检查网络和激活码后重试", true
		default:
			return code, message, true
		}
	}
	return "download_failed", "下载 Profile 失败，请检查网络和激活码后重试", true
}

func normalizeSIMIdentifier(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "\""))
	return strings.TrimRight(value, "Ff")
}

func (a *app) monitorESIMSwitch(operationID, targetICCID, initialWarning string, initialDelay time.Duration) {
	if a == nil {
		return
	}
	if initialDelay < 0 {
		initialDelay = 0
	}
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	<-timer.C
	a.updateESIMOperation(operationID, esimOperationStateRun, "reconnect", "正在等待模块重新识别 Profile…", 85)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	target := normalizeSIMIdentifier(targetICCID)
	for {
		if current := a.currentESIMOperation(); current == nil || current.ID != operationID {
			return
		}
		if err := a.ensureUSBAT(); err == nil {
			if status, statusErr := a.usbATStatus(); statusErr == nil {
				current := normalizeSIMIdentifier(status.ICCID)
				if status.SimInserted && current != "" && current == target {
					var result *esimOperationResult
					if strings.TrimSpace(initialWarning) != "" {
						result = &esimOperationResult{
							Warning:     "Profile 已切换，但模块重启指令未能完全确认",
							WarningCode: "switch_reboot_unconfirmed",
						}
					}
					a.finishESIMOperation(operationID, "Profile 已切换，模块已重新识别", result, 0)
					return
				}
			}
		}

		select {
		case <-ctx.Done():
			a.finishESIMOperation(operationID, "Profile 切换指令已提交，但模块重连尚未确认", &esimOperationResult{
				Warning:     "请稍后刷新卡片状态；如仍未恢复，请重新连接模块",
				WarningCode: "switch_reconnect_unconfirmed",
			}, 0)
			return
		case <-ticker.C:
		}
	}
}
