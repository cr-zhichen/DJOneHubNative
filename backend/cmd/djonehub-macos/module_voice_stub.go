//go:build !darwin || !cgo

package main

import "errors"

func (a *app) probeModuleADB() error {
	return errors.New("模块 ADB 仅在 macOS cgo 版本可用")
}

func runInternalADBProbe() error {
	return errors.New("模块 ADB 仅在 macOS cgo 版本可用")
}

func (a *app) resetModuleVoiceState() {
	a.moduleVoiceMu.Lock()
	a.moduleVoicePrepared = false
	a.moduleVoiceRoute = false
	a.moduleVoiceMu.Unlock()
}

func (a *app) prepareModuleVoiceRuntime(_ string) error {
	return errors.New("模块侧语音运行时仅在 macOS cgo 版本可用")
}

func (a *app) startModuleVoiceRoute(_ string) error {
	return errors.New("模块侧语音路由仅在 macOS cgo 版本可用")
}

func (a *app) selfTestModuleVoiceRoute(_ string) error {
	return errors.New("模块侧语音路由仅在 macOS cgo 版本可用")
}

func (a *app) stopModuleVoiceRoute(_ string) error {
	return nil
}
