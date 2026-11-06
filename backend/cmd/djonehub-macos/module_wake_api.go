//go:build darwin && cgo

package main

import (
	"net/http"
	"strings"
	"time"
)

// 模块网络保活（原 DJOneHub-mac-enhanced 的 network wake 能力）。
//
// 背景：QDC507 在「移动设备模式」下没有 iOS 配套 App 时，数据通路会因为
// 长时间空转而进入休眠，表现为 iPhone 侧还在连着但实际断网。这里的做法
// 是在模块上装一个 30 行不到的 shell 守护，靠 wake_lock + 定时 ping 维持
// 通路，不需要 Mac 一直插着。
//
// 与通话模式的关系：两者都改 USB 组合，但保活只在没有 USB Audio 时生效
// （脚本里判断 functions 含 audio 就直接退出），所以不会和通话抢音频。

func (a *app) enableNetworkWakeAPI(w http.ResponseWriter, _ *http.Request) {
	if err := enableModuleNetworkWake(); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true})
}

func (a *app) disableNetworkWakeAPI(w http.ResponseWriter, _ *http.Request) {
	if err := disableModuleNetworkWake(); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// networkWakeStatusAPI 只回读模块上的开关文件，不去 ping 任何外部地址：
// 状态查询应当是只读且廉价的，真正的探活由模块侧的守护自己完成。
func (a *app) networkWakeStatusAPI(w http.ResponseWriter, _ *http.Request) {
	adb, err := openDJIUSBADB()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":     false,
			"reachable":   false,
			"detail":      err.Error(),
			"checked_now": time.Now(),
		})
		return
	}
	defer adb.Close()
	out, status, err := adb.shellChecked("test -f "+moduleNetworkWakeFlag+" && echo on || echo off", 8*time.Second)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":     false,
			"reachable":   true,
			"detail":      err.Error(),
			"checked_now": time.Now(),
		})
		return
	}
	enabled := status == 0 && strings.Contains(out, "on")
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     enabled,
		"reachable":   true,
		"checked_now": time.Now(),
	})
}
