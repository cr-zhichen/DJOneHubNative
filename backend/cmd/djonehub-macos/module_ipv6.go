//go:build darwin && cgo

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// 模块网卡的 IPv6 开关。
//
// 背景：模块通过 USB ECM 下发 RA，Mac 侧 en10 会拿到公网 IPv6 地址并装上
// 一条全局 IPv6 默认路由。而家用 Wi-Fi 往往没有 IPv6 前缀（en0 只有
// fe80:: 链路本地），于是整个系统唯一的 IPv6 出口变成了 4G 模块——
// 任何走 IPv6 优先的应用（微信、部分 Electron 客户端）都会悄悄把长连接
// 建到模块上，持续消耗流量。IPv4 那边调服务顺序完全管不到它。
//
// 关掉模块服务的 IPv6 后 en10 不再有全局地址，这些连接会自然断开并回落到
// IPv4，按 IPv4 默认路由走 Wi-Fi。由于 Wi-Fi 本来就没有 IPv6 能力，关闭
// 不损失任何可用性。

var serviceOrderLineRe = regexp.MustCompile(`^\((\d+|\*)\)\s*(.+)$`)

// moduleIPv6Status 是 GET /api/module/ipv6 的返回体。
type moduleIPv6Status struct {
	Available bool     `json:"available"`
	Service   string   `json:"service"`
	Device    string   `json:"device"`
	Enabled   bool     `json:"enabled"`
	State     string   `json:"state,omitempty"`
	Addresses []string `json:"addresses"`
	Error     string   `json:"error,omitempty"`
}

// moduleProductName 返回当前模块的 USB 产品名，没有模块时返回空串。
func (a *app) moduleProductName() string {
	if device := a.currentUSBDevice(); device != nil {
		return strings.TrimSpace(device.Product)
	}
	return ""
}

// findModuleService 找出当前模块对应的网络服务（Hardware Port 与 USB 产品名一致）。
func (a *app) findModuleService() (networkService, error) {
	services, err := macNetworkServiceOrder(a.moduleProductName())
	if err != nil {
		return networkService{}, err
	}
	for _, s := range services {
		if s.Module {
			return s, nil
		}
	}
	return networkService{}, fmt.Errorf("未找到 4G 模块对应的网络服务")
}

// parseIPv6Setting 从 networksetup -getinfo 的输出里读出 IPv6 配置状态。
// 输出跟随系统语言，因此同时兼容中英文。
func parseIPv6Setting(output string) (enabled bool, state string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "ipv6:") {
			continue
		}
		state = strings.TrimSpace(line[len("ipv6:"):])
		lower := strings.ToLower(state)
		switch {
		case lower == "off", state == "关闭":
			return false, state
		case strings.HasPrefix(lower, "automatic"),
			strings.HasPrefix(lower, "manual"),
			strings.HasPrefix(lower, "link-local"),
			strings.HasPrefix(state, "自动"),
			strings.HasPrefix(state, "手动"):
			return true, state
		}
		// 未知取值时保守认定「开」，避免误报为已关闭。
		return true, state
	}
	return false, ""
}

// globalIPv6Addresses 列出指定网卡上的全局 IPv6 地址（排除 fe80 链路本地）。
func globalIPv6Addresses(device string) []string {
	addrs := []string{}
	if device == "" {
		return addrs
	}
	out, err := exec.Command("ifconfig", device).Output()
	if err != nil {
		return addrs
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "inet6" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(fields[1]), "fe80") {
			continue
		}
		addrs = append(addrs, fields[1])
	}
	return addrs
}

// readModuleIPv6Status 组装当前状态。只读，不产生副作用。
func (a *app) readModuleIPv6Status() moduleIPv6Status {
	svc, err := a.findModuleService()
	if err != nil {
		return moduleIPv6Status{Available: false, Error: err.Error(), Addresses: []string{}}
	}
	status := moduleIPv6Status{Available: true, Service: svc.Name, Device: svc.Device}
	out, err := exec.Command("networksetup", "-getinfo", svc.Name).Output()
	if err != nil {
		status.Error = fmt.Sprintf("读取 IPv6 配置失败: %v", err)
		status.Addresses = globalIPv6Addresses(svc.Device)
		return status
	}
	status.Enabled, status.State = parseIPv6Setting(string(out))
	status.Addresses = globalIPv6Addresses(svc.Device)
	return status
}

// runPrivilegedShell 以管理员权限执行一段 shell 命令，返回其标准输出。
func runPrivilegedShell(ctx context.Context, command string) (string, error) {
	b64 := base64.StdEncoding.EncodeToString([]byte(command))
	script := fmt.Sprintf("do shell script \"echo %s | base64 -d | sh\" with administrator privileges", b64)
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("执行失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// GET /api/module/ipv6：查询模块网卡的 IPv6 开关状态
func (a *app) moduleIPv6StatusAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.readModuleIPv6Status())
}

// POST /api/module/ipv6：开启/关闭模块网卡的 IPv6（需要管理员授权）
func (a *app) setModuleIPv6API(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "缺少 enabled 字段")
		return
	}
	svc, err := a.findModuleService()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// 记录操作前的服务顺序，用于事后校验。
	before := []string{}
	if services, err := macNetworkServiceOrder(a.moduleProductName()); err == nil {
		for _, s := range services {
			before = append(before, s.Name)
		}
	}

	flag := "-setv6off"
	if *body.Enabled {
		flag = "-setv6automatic"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	command := "networksetup " + shellQuote(flag) + " " + shellQuote(svc.Name) + "\n" +
		"networksetup -listnetworkserviceorder | grep -E '^\\([0-9*]\\)' || true"
	out, err := runPrivilegedShell(ctx, command)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	order := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if m := serviceOrderLineRe.FindStringSubmatch(line); m != nil {
			order = append(order, strings.TrimSpace(m[2]))
		}
	}

	// 兜底：万一 setv6off 重建服务导致顺序被打乱，立刻按原顺序恢复。
	orderRestored := false
	if len(before) > 0 && len(order) > 0 && !equalStringSlices(before, order) {
		quoted := make([]string, 0, len(before))
		for _, name := range before {
			quoted = append(quoted, shellQuote(name))
		}
		if _, err := runPrivilegedShell(ctx, "networksetup -ordernetworkservices "+strings.Join(quoted, " ")); err != nil {
			writeError(w, http.StatusBadGateway, "恢复网络服务顺序失败："+err.Error())
			return
		}
		orderRestored = true
	}

	status := a.readModuleIPv6Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"enabled":        status.Enabled,
		"service":        status.Service,
		"addresses":      status.Addresses,
		"order_restored": orderRestored,
	})
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
