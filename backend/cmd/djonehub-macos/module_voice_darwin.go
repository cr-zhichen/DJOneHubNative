//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	moduleVoiceRemoteDirectory = "/tmp/djonehub-call"
	moduleVoiceHelper          = "/tmp/djonehub-call/mavo-pcm-bridge.armv7"
	moduleVoiceRoutePID        = "/run/djonehub-voice-route.pid"
	moduleVoiceRouteLog        = "/run/djonehub-voice-route.log"
	moduleVoiceCalibrationPID  = "/run/djonehub-alsaucm.pid"
	moduleVoiceCalibrationLog  = "/run/djonehub-alsaucm.log"
)

func (a *app) probeModuleADB() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 ADB 探测程序：%w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	output, commandErr := exec.CommandContext(ctx, executable, "-internal-adb-probe").CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// IOUSBHost may keep the killed helper's user client briefly. Waiting here
		// prevents the next recovery attempt from being reported as false ACCESS.
		time.Sleep(2 * time.Second)
		return errors.New("模块 ADB 探测超过 12 秒，已终止隔离进程")
	}
	if commandErr != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = commandErr.Error()
		}
		return errors.New(detail)
	}
	return nil
}

func runInternalADBProbe() error {
	return probeModuleADBDirect()
}

func probeModuleADBDirect() error {
	adb, err := openDJIUSBADB()
	if err != nil {
		return err
	}
	defer adb.Close()
	output, status, err := adb.shellChecked("id -u", 8*time.Second)
	if err != nil {
		return err
	}
	if status != 0 || !containsShellField(output, "0") {
		return errors.New("模块 ADB 未返回 root shell")
	}
	return nil
}

func (a *app) resetModuleVoiceState() {
	a.moduleVoiceMu.Lock()
	a.moduleVoicePrepared = false
	a.moduleVoiceRoute = false
	a.moduleVoiceMu.Unlock()
}

func (a *app) prepareModuleVoiceRuntime(directory string) error {
	a.moduleVoiceMu.Lock()
	defer a.moduleVoiceMu.Unlock()
	if err := verifyModuleVoiceRuntime(directory); err != nil {
		return fmt.Errorf("本地语音运行时无效：%w", err)
	}
	adb, err := openDJIUSBADB()
	if err != nil {
		return fmt.Errorf("打开模块 ADB：%w", err)
	}
	defer adb.Close()

	identity, status, err := adb.shellChecked("id -u", 8*time.Second)
	if err != nil {
		return err
	}
	if status != 0 || !containsShellField(identity, "0") {
		return errors.New("模块 ADB 没有 root 控制权限")
	}
	release, status, err := adb.shellChecked("uname -r", 8*time.Second)
	if err != nil {
		return err
	}
	if status != 0 || !containsShellField(release, moduleVoiceKernel) {
		actual := strings.TrimSpace(release)
		if actual == "" {
			actual = "未知"
		}
		return fmt.Errorf("模块内核版本不匹配；需要 %s，实际为 %s", moduleVoiceKernel, actual)
	}
	if _, status, err := adb.shellChecked("mkdir -p '"+moduleVoiceRemoteDirectory+"' && chmod 700 '"+moduleVoiceRemoteDirectory+"'", 8*time.Second); err != nil || status != 0 {
		return firstModuleVoiceError("无法创建模块运行时目录", err)
	}

	for _, expected := range moduleVoiceFiles[:3] {
		data, err := os.ReadFile(filepath.Join(directory, expected.Name))
		if err != nil {
			return fmt.Errorf("读取 %s：%w", expected.Name, err)
		}
		mode := uint32(0o100000 | expected.Mode.Perm())
		if err := adb.push(data, moduleVoiceRemoteDirectory+"/"+expected.Name, mode, 35*time.Second); err != nil {
			return fmt.Errorf("部署 %s：%w", expected.Name, err)
		}
	}

	ready, err := moduleVoiceSoundDevicesReady(adb)
	if err != nil {
		return err
	}
	if !ready {
		legacyOutput, legacyStatus, legacyErr := adb.shellChecked("grep -q '^qdc507_afe ' /proc/modules", 8*time.Second)
		if legacyErr != nil {
			return legacyErr
		}
		if legacyStatus == 0 {
			return errors.New("检测到旧版 qdc507_afe 驱动仍在内核中；请重启模块后再试，应用不会热切换该驱动")
		}
		if legacyStatus != 1 {
			return fmt.Errorf("无法核对旧版音频驱动状态：%s", legacyOutput)
		}
		modules := []struct {
			File string
			Name string
		}{
			{File: "qdc507_aprv3.ko", Name: "qdc507_aprv3"},
			{File: "qdc507_voice.ko", Name: "qdc507_voice"},
		}
		for _, module := range modules {
			_, present, err := adb.shellChecked("grep -q '^"+module.Name+" ' /proc/modules", 8*time.Second)
			if err != nil {
				return err
			}
			if present == 0 {
				continue
			}
			if present != 1 {
				return errors.New("无法读取模块驱动列表")
			}
			output, loadStatus, loadErr := adb.shellChecked("insmod '"+moduleVoiceRemoteDirectory+"/"+module.File+"'", 20*time.Second)
			if loadErr != nil || loadStatus != 0 {
				diagnostics, _, _ := adb.shellChecked("dmesg | tail -n 100", 8*time.Second)
				detail := strings.TrimSpace(strings.Join(nonEmptyStrings(output, diagnostics), "\n"))
				if detail == "" && loadErr != nil {
					detail = loadErr.Error()
				}
				if detail == "" {
					detail = "模块音频驱动加载失败"
				}
				// APR/voice may receive late DSP callbacks. Never hot-unload these
				// modules after they have been loaded; a module reboot is the safe
				// rollback boundary.
				return errors.New(detail)
			}
		}
	}

	waitCommand := "ready=0; n=0; while test \"$n\" -lt 100; do if " + moduleVoiceSoundDeviceChecks() + "; then ready=1; break; fi; sleep 0.2; n=$((n+1)); done; test \"$ready\" -eq 1"
	output, status, err := adb.shellChecked(waitCommand, 25*time.Second)
	if err != nil || status != 0 {
		diagnostics, _, _ := adb.shellChecked("dmesg | tail -n 100", 8*time.Second)
		return errors.New(firstNonEmptyModuleVoice(strings.TrimSpace(diagnostics), strings.TrimSpace(output), errorText(err), "音频驱动已加载，但 ALSA 设备没有出现"))
	}
	if err := ensureModuleVoiceCalibration(adb); err != nil {
		return err
	}
	output, status, err = adb.shellChecked("test -c /dev/ttyGS0 && test -p /run/voc_svr", 8*time.Second)
	if err != nil || status != 0 {
		return errors.New(firstNonEmptyModuleVoice(strings.TrimSpace(output), errorText(err), "模块缺少 ttyGS0 或 voc_svr，无法建立 USB 通话路由"))
	}
	output, status, err = adb.shellChecked("'"+moduleVoiceHelper+"' --check", 15*time.Second)
	if err != nil || status != 0 {
		return errors.New(firstNonEmptyModuleVoice(strings.TrimSpace(output), errorText(err), "模块 PCM 桥自检失败"))
	}
	a.moduleVoicePrepared = true
	return nil
}

func (a *app) startModuleVoiceRoute(directory string) error {
	if err := a.prepareModuleVoiceRuntime(directory); err != nil {
		return err
	}
	a.voiceUSBTransition.Lock()
	defer a.voiceUSBTransition.Unlock()
	a.moduleVoiceMu.Lock()
	defer a.moduleVoiceMu.Unlock()
	if ready, _ := a.moduleVoiceRouteReady(); ready {
		a.moduleVoiceRoute = true
		return nil
	}
	command := "rm -f '" + moduleVoiceRoutePID + "' '" + moduleVoiceRouteLog + "'; " +
		"nohup '" + moduleVoiceHelper + "' --voice-route-session --verbose </dev/null >> '" + moduleVoiceRouteLog + "' 2>&1 & pid=$!; " +
		"starttime=$(cut -d ' ' -f 22 \"/proc/$pid/stat\" 2>/dev/null); " +
		"case \"$pid:$starttime\" in :*|*:|*[!0-9:]*) false;; *) printf '%s %s\\n' \"$pid\" \"$starttime\" > '" + moduleVoiceRoutePID + "';; esac"
	launchOutput, launchStatus, launchErr := a.moduleVoiceShell(command, 8*time.Second)
	launchDetail := ""
	if launchErr != nil || launchStatus != 0 {
		launchDetail = firstNonEmptyModuleVoice(strings.TrimSpace(launchOutput), errorText(launchErr), fmt.Sprintf("启动命令返回状态 %d", launchStatus))
	}
	// audio_enable=1 can briefly re-enumerate the USB gadget. The helper may
	// already be healthy even when the launch shell reply was lost.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ready, _ := a.moduleVoiceRouteReady(); ready {
			a.moduleVoiceRoute = true
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	logOutput, _, _ := a.moduleVoiceShell("test ! -f '"+moduleVoiceRouteLog+"' || tail -n 160 '"+moduleVoiceRouteLog+"'", 8*time.Second)
	detail := strings.Join(nonEmptyStrings(launchDetail, strings.TrimSpace(logOutput)), "\n")
	if detail == "" {
		detail = "模块 D4/UAC 语音路由没有进入 RUNNING"
	}
	if cleanupErr := a.stopModuleVoiceRouteLocked(); cleanupErr != nil {
		detail += "\n自动回滚未完全确认：" + cleanupErr.Error()
	}
	return errors.New(detail)
}

func (a *app) selfTestModuleVoiceRoute(directory string) error {
	if err := a.startModuleVoiceRoute(directory); err != nil {
		return err
	}
	if err := a.stopModuleVoiceRoute(directory); err != nil {
		return fmt.Errorf("语音路由已启动，但安全回滚未通过：%w", err)
	}
	return nil
}

func (a *app) stopModuleVoiceRoute(_ string) error {
	a.voiceUSBTransition.Lock()
	defer a.voiceUSBTransition.Unlock()
	a.moduleVoiceMu.Lock()
	defer a.moduleVoiceMu.Unlock()
	return a.stopModuleVoiceRouteLocked()
}

// stopModuleVoiceRouteLocked is also used by the failed-start rollback path;
// callers must hold voiceUSBTransition and moduleVoiceMu.
func (a *app) stopModuleVoiceRouteLocked() error {
	stopCommand := "helper_stopped=1; " +
		"is_owned() { current_start=$(cut -d ' ' -f 22 \"/proc/$pid/stat\" 2>/dev/null); " +
		"argv0=$(tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null | sed -n '1p'); " +
		"args=$(tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null); " +
		"test \"$current_start\" = \"$expected_start\" && test \"$argv0\" = '" + moduleVoiceHelper + "' && printf '%s\\n' \"$args\" | grep -q '^--voice-route-session$'; }; " +
		"if test -s '" + moduleVoiceRoutePID + "'; then read pid expected_start < '" + moduleVoiceRoutePID + "' || true; " +
		"case \"$pid:$expected_start\" in :*|*:|*[!0-9:]*) true;; *) if is_owned; then kill -TERM \"$pid\" 2>/dev/null || true; " +
		"n=0; while is_owned && test \"$n\" -lt 50; do sleep 0.1; n=$((n+1)); done; is_owned && helper_stopped=0 || true; fi;; esac; fi; " +
		"test \"$helper_stopped\" -eq 1 && rm -f '" + moduleVoiceRoutePID + "'"
	_, _, stopErr := a.moduleVoiceShell(stopCommand, 10*time.Second)
	if stopErr != nil {
		return stopErr
	}
	stopped := false
	for index := 0; index < 20; index++ {
		if ready, err := a.moduleVoiceRouteStopped(); err == nil && ready {
			stopped = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !stopped {
		return errors.New("D4 语音 helper 未确认正常退出；为保留 mixer 回滚，没有发送 SIGKILL")
	}
	cleanup := "echo 0 > /sys/class/android_usb/f_audio/audio_enable; " +
		"if test -p /run/voc_svr; then printf 'T\\n' > /run/voc_svr; printf 'T\\n' > /run/voc_svr; printf 'B\\n' > /run/voc_svr; fi; " +
		"test \"$(cat /sys/class/android_usb/f_audio/audio_enable)\" = 0"
	var cleanupDetail string
	for index := 0; index < 5; index++ {
		output, status, err := a.moduleVoiceShell(cleanup, 8*time.Second)
		if err == nil && status == 0 {
			a.moduleVoiceRoute = false
			return nil
		}
		cleanupDetail = firstNonEmptyModuleVoice(strings.TrimSpace(output), errorText(err), fmt.Sprintf("清理命令返回状态 %d", status))
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New(firstNonEmptyModuleVoice(cleanupDetail, "D4 helper 已退出，但 T/T/B 路由回滚未确认"))
}

func (a *app) moduleVoiceShell(command string, timeout time.Duration) (string, int, error) {
	adb, err := openDJIUSBADB()
	if err != nil {
		return "", 0, err
	}
	defer adb.Close()
	return adb.shellChecked(command, timeout)
}

func (a *app) moduleVoiceRouteReady() (bool, error) {
	command := "test -s '" + moduleVoiceRoutePID + "' && read pid expected_start < '" + moduleVoiceRoutePID + "' && " +
		"test \"$(cut -d ' ' -f 22 \"/proc/$pid/stat\" 2>/dev/null)\" = \"$expected_start\" && " +
		"test \"$(tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null | sed -n '1p')\" = '" + moduleVoiceHelper + "' && " +
		"tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null | grep -q '^--voice-route-session$' && " +
		"grep -q 'VoLTE route session active on hw:0,4' '" + moduleVoiceRouteLog + "' && " +
		"test \"$(cat /sys/class/android_usb/f_audio/audio_enable)\" = 1 && " +
		"grep -q '^state: RUNNING' /proc/asound/card0/pcm4p/sub0/status && " +
		"grep -q '^state: RUNNING' /proc/asound/card0/pcm4c/sub0/status"
	_, status, err := a.moduleVoiceShell(command, 8*time.Second)
	return err == nil && status == 0, err
}

func (a *app) moduleVoiceRouteStopped() (bool, error) {
	command := "owned=0; if test -s '" + moduleVoiceRoutePID + "'; then read pid expected_start < '" + moduleVoiceRoutePID + "' || true; " +
		"current_start=$(cut -d ' ' -f 22 \"/proc/$pid/stat\" 2>/dev/null); argv0=$(tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null | sed -n '1p'); " +
		"args=$(tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null); if test \"$current_start\" = \"$expected_start\" && " +
		"test \"$argv0\" = '" + moduleVoiceHelper + "' && printf '%s\\n' \"$args\" | grep -q '^--voice-route-session$'; then owned=1; else rm -f '" + moduleVoiceRoutePID + "'; fi; fi; test \"$owned\" -eq 0"
	_, status, err := a.moduleVoiceShell(command, 8*time.Second)
	return err == nil && status == 0, err
}

func moduleVoiceSoundDevicesReady(adb *adbClient) (bool, error) {
	_, status, err := adb.shellChecked(moduleVoiceSoundDeviceChecks(), 8*time.Second)
	return err == nil && status == 0, err
}

func moduleVoiceSoundDeviceChecks() string {
	return "test -c '/dev/snd/controlC0' && test -c '/dev/snd/pcmC0D4p' && test -c '/dev/snd/pcmC0D4c' && " +
		"test -c '/dev/snd/pcmC0D5p' && test -c '/dev/snd/pcmC0D6c' && grep -Fq 'mdm9607-tomtom-i2s-snd-card' /proc/asound/cards"
}

func ensureModuleVoiceCalibration(adb *adbClient) error {
	command := "owned=0; " +
		"if test -s '" + moduleVoiceCalibrationPID + "'; then read pid expected_start < '" + moduleVoiceCalibrationPID + "' || true; " +
		"current_start=$(cut -d ' ' -f 22 \"/proc/$pid/stat\" 2>/dev/null); argv0=$(tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null | sed -n '1p'); " +
		"test \"$current_start\" = \"$expected_start\" && test \"$argv0\" = /usr/bin/alsaucm_test && owned=1 || true; fi; " +
		"if test \"$owned\" -eq 0; then for proc in /proc/[0-9]*; do test -r \"$proc/cmdline\" || continue; " +
		"argv0=$(tr '\\000' '\\n' < \"$proc/cmdline\" 2>/dev/null | sed -n '1p'); test \"$argv0\" = /usr/bin/alsaucm_test || continue; " +
		"oldpid=${proc##*/}; kill -TERM \"$oldpid\" 2>/dev/null || true; n=0; while kill -0 \"$oldpid\" 2>/dev/null && test \"$n\" -lt 30; do sleep 0.1; n=$((n+1)); done; " +
		"kill -0 \"$oldpid\" 2>/dev/null && exit 71 || true; done; rm -f /run/alsaucm_test '" + moduleVoiceCalibrationPID + "' '" + moduleVoiceCalibrationLog + "'; " +
		"nohup /usr/bin/alsaucm_test </dev/null >> '" + moduleVoiceCalibrationLog + "' 2>&1 & pid=$!; starttime=$(cut -d ' ' -f 22 \"/proc/$pid/stat\" 2>/dev/null); " +
		"printf '%s %s\\n' \"$pid\" \"$starttime\" > '" + moduleVoiceCalibrationPID + "'; n=0; while test \"$n\" -lt 50 && test ! -p /run/alsaucm_test; do " +
		"kill -0 \"$pid\" 2>/dev/null || exit 72; sleep 0.1; n=$((n+1)); done; test -p /run/alsaucm_test || exit 73; fi; " +
		"if ! grep -q 'ACDB -> Sent VocProc Cal!' '" + moduleVoiceCalibrationLog + "' 2>/dev/null; then " +
		"printf 'open snd_soc_msm_9x07_Tomtom_I2S\\n' > /run/alsaucm_test; printf 'set _verb VoLTE\\n' > /run/alsaucm_test; " +
		"printf 'set _enadev Auxpcm Rx\\n' > /run/alsaucm_test; printf 'set _enadev Auxpcm Tx\\n' > /run/alsaucm_test; " +
		"n=0; while test \"$n\" -lt 100; do grep -q 'ACDB -> Sent VocProc Cal!' '" + moduleVoiceCalibrationLog + "' 2>/dev/null && break; sleep 0.1; n=$((n+1)); done; fi; " +
		"grep -q 'ACDB -> Sent VocProc Cal!' '" + moduleVoiceCalibrationLog + "'"
	output, status, err := adb.shellChecked(command, 25*time.Second)
	if err == nil && status == 0 {
		return nil
	}
	logOutput, _, _ := adb.shellChecked("test ! -f '"+moduleVoiceCalibrationLog+"' || tail -n 100 '"+moduleVoiceCalibrationLog+"'", 8*time.Second)
	return errors.New(firstNonEmptyModuleVoice(strings.TrimSpace(logOutput), strings.TrimSpace(output), errorText(err), "模块 VoLTE ACDB 校准服务没有就绪"))
}

func containsShellField(output, expected string) bool {
	for _, field := range strings.Fields(output) {
		if field == expected {
			return true
		}
	}
	return false
}

func nonEmptyStrings(values ...string) []string {
	var result []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func firstNonEmptyModuleVoice(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "未知错误"
}

func firstModuleVoiceError(fallback string, err error) error {
	if err != nil {
		return err
	}
	return errors.New(fallback)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
