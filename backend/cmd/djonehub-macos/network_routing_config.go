package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	routingConfigVersion = 1

	routingModeIndependent = "independent"
	routingModeClash       = "clash"

	routingActionModuleDirect = "module_direct"
	routingActionSystemDirect = "system_direct"
	routingActionSystemSOCKS  = "system_socks"
)

type routingApplication struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	BundleID       string `json:"bundle_id,omitempty"`
	BundlePath     string `json:"bundle_path"`
	ExecutablePath string `json:"executable_path,omitempty"`
	Action         string `json:"action"`
}

type routingSOCKSConfig struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type routingConfig struct {
	Version         int                  `json:"version"`
	Mode            string               `json:"mode"`
	Applications    []routingApplication `json:"applications"`
	SystemSOCKS     routingSOCKSConfig   `json:"system_socks"`
	ClashListenPort int                  `json:"clash_listen_port"`
}

type routingConflict struct {
	Interface    string   `json:"interface"`
	Destinations []string `json:"destinations"`
	Detail       string   `json:"detail"`
}

type routingInterfaceInfo struct {
	Name string `json:"name"`
	IPv4 string `json:"ipv4"`
}

type routingPreflight struct {
	Ready           bool                  `json:"ready"`
	CoreAvailable   bool                  `json:"core_available"`
	CoreVersion     string                `json:"core_version,omitempty"`
	ModuleInterface *routingInterfaceInfo `json:"module_interface,omitempty"`
	SystemInterface string                `json:"system_interface,omitempty"`
	Conflicts       []routingConflict     `json:"conflicts"`
	Issues          []string              `json:"issues"`
	Warnings        []string              `json:"warnings"`
}

func defaultRoutingConfig() routingConfig {
	return routingConfig{
		Version:         routingConfigVersion,
		Mode:            routingModeIndependent,
		Applications:    []routingApplication{},
		SystemSOCKS:     routingSOCKSConfig{Server: "127.0.0.1", Port: 7891},
		ClashListenPort: 17890,
	}
}

func cloneRoutingConfig(config routingConfig) routingConfig {
	applications := make([]routingApplication, len(config.Applications))
	copy(applications, config.Applications)
	config.Applications = applications
	return config
}

func normalizeRoutingConfig(config routingConfig) (routingConfig, error) {
	if config.Version == 0 {
		config.Version = routingConfigVersion
	}
	if config.Version != routingConfigVersion {
		return routingConfig{}, fmt.Errorf("不支持的分流配置版本：%d", config.Version)
	}
	config.Mode = strings.TrimSpace(config.Mode)
	if config.Mode == "" {
		config.Mode = routingModeIndependent
	}
	if config.Mode != routingModeIndependent && config.Mode != routingModeClash {
		return routingConfig{}, errors.New("分流模式必须是 independent 或 clash")
	}
	if config.ClashListenPort == 0 {
		config.ClashListenPort = 17890
	}
	if config.ClashListenPort < 1 || config.ClashListenPort > 65535 {
		return routingConfig{}, errors.New("Clash 代管 SOCKS 端口必须在 1–65535 之间")
	}

	config.SystemSOCKS.Server = strings.TrimSpace(config.SystemSOCKS.Server)
	config.SystemSOCKS.Username = strings.TrimSpace(config.SystemSOCKS.Username)
	if strings.ContainsAny(config.SystemSOCKS.Server, "\r\n\t ") || strings.Contains(config.SystemSOCKS.Server, "://") {
		return routingConfig{}, errors.New("SOCKS 服务器只填写主机名或 IP，不要包含协议和路径")
	}
	if config.SystemSOCKS.Port < 0 || config.SystemSOCKS.Port > 65535 {
		return routingConfig{}, errors.New("系统侧 SOCKS 端口必须在 1–65535 之间")
	}

	if len(config.Applications) > 256 {
		return routingConfig{}, errors.New("应用规则不能超过 256 条")
	}
	seen := make(map[string]struct{}, len(config.Applications))
	cleaned := make([]routingApplication, 0, len(config.Applications))
	usesSOCKS := false
	for index, application := range config.Applications {
		application.ID = strings.TrimSpace(application.ID)
		application.Name = strings.TrimSpace(application.Name)
		application.BundleID = strings.TrimSpace(application.BundleID)
		application.BundlePath = filepath.Clean(strings.TrimSpace(application.BundlePath))
		application.ExecutablePath = filepath.Clean(strings.TrimSpace(application.ExecutablePath))
		application.Action = strings.TrimSpace(application.Action)

		if !filepath.IsAbs(application.BundlePath) || !strings.HasSuffix(strings.ToLower(application.BundlePath), ".app") {
			return routingConfig{}, fmt.Errorf("第 %d 条应用规则缺少有效的 .app 路径", index+1)
		}
		if application.Name == "" {
			application.Name = strings.TrimSuffix(filepath.Base(application.BundlePath), filepath.Ext(application.BundlePath))
		}
		if application.ID == "" {
			application.ID = application.BundleID
			if application.ID == "" {
				application.ID = application.BundlePath
			}
		}
		if application.ExecutablePath == "." {
			application.ExecutablePath = ""
		}
		if application.ExecutablePath != "" && !pathIsInside(application.ExecutablePath, application.BundlePath) {
			return routingConfig{}, fmt.Errorf("%s 的可执行文件不在应用包内", application.Name)
		}
		switch application.Action {
		case routingActionModuleDirect, routingActionSystemDirect:
		case routingActionSystemSOCKS:
			usesSOCKS = true
		default:
			return routingConfig{}, fmt.Errorf("%s 使用了未知的出口规则", application.Name)
		}
		key := strings.ToLower(application.BundlePath)
		if _, exists := seen[key]; exists {
			return routingConfig{}, fmt.Errorf("应用规则重复：%s", application.Name)
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, application)
	}
	config.Applications = cleaned

	if usesSOCKS {
		if config.SystemSOCKS.Server == "" || config.SystemSOCKS.Port == 0 {
			return routingConfig{}, errors.New("存在“系统侧 SOCKS”应用规则，请先填写 SOCKS 服务器和端口")
		}
	}
	return config, nil
}

func pathIsInside(path, directory string) bool {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return false
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func loadRoutingConfig(path string) routingConfig {
	config := defaultRoutingConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return config
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return defaultRoutingConfig()
	}
	normalized, err := normalizeRoutingConfig(config)
	if err != nil {
		return defaultRoutingConfig()
	}
	return normalized
}

func saveRoutingConfig(path string, config routingConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func resolveRoutingModuleInterface(moduleProduct string) (routingInterfaceInfo, error) {
	services, err := macNetworkServiceOrder(moduleProduct)
	if err != nil {
		return routingInterfaceInfo{}, err
	}
	moduleDevice := ""
	for _, service := range services {
		knownBaiwang := strings.EqualFold(strings.TrimSpace(service.Name), "Baiwang") ||
			strings.EqualFold(strings.TrimSpace(service.Port), "Baiwang")
		if service.Module || knownBaiwang {
			moduleDevice = strings.TrimSpace(service.Device)
			if moduleDevice != "" {
				break
			}
		}
	}
	if moduleDevice == "" {
		return routingInterfaceInfo{}, errors.New("没有找到 Baiwang 模块网络服务")
	}
	for _, networkInterface := range discoverMacNetworkInterfaces() {
		if networkInterface.Name != moduleDevice {
			continue
		}
		if networkInterface.Status != "active" || strings.TrimSpace(networkInterface.IPv4) == "" {
			return routingInterfaceInfo{}, fmt.Errorf("模块网卡 %s 尚未连接或没有 IPv4 地址", moduleDevice)
		}
		return routingInterfaceInfo{Name: moduleDevice, IPv4: networkInterface.IPv4}, nil
	}
	return routingInterfaceInfo{}, fmt.Errorf("没有找到模块网卡 %s", moduleDevice)
}

func detectRoutingConflicts() []routingConflict {
	out, err := exec.Command("netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		return []routingConflict{{Detail: "无法读取 IPv4 路由表：" + err.Error()}}
	}
	return parseRoutingConflicts(string(out))
}

func parseRoutingConflicts(routeTable string) []routingConflict {
	destinationsByInterface := make(map[string][]string)
	for _, line := range strings.Split(routeTable, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		destination := fields[0]
		networkInterface := ""
		for _, field := range fields[2:] {
			if strings.HasPrefix(field, "utun") {
				networkInterface = field
				break
			}
		}
		if networkInterface == "" || !isBroadTUNCaptureDestination(destination) {
			continue
		}
		destinationsByInterface[networkInterface] = append(destinationsByInterface[networkInterface], destination)
	}

	interfaces := make([]string, 0, len(destinationsByInterface))
	for networkInterface := range destinationsByInterface {
		interfaces = append(interfaces, networkInterface)
	}
	sort.Strings(interfaces)
	conflicts := make([]routingConflict, 0, len(interfaces))
	for _, networkInterface := range interfaces {
		destinations := destinationsByInterface[networkInterface]
		sort.Strings(destinations)
		if len(destinations) > 8 {
			destinations = destinations[:8]
		}
		conflicts = append(conflicts, routingConflict{
			Interface:    networkInterface,
			Destinations: destinations,
			Detail:       fmt.Sprintf("%s 正在接管大范围 IPv4 路由（%s）", networkInterface, strings.Join(destinations, "、")),
		})
	}
	return conflicts
}

func isBroadTUNCaptureDestination(destination string) bool {
	normalized := strings.ToLower(strings.TrimSpace(destination))
	if normalized == "default" || strings.HasPrefix(normalized, "198.18.") || strings.HasPrefix(normalized, "198.18/") {
		return true
	}
	if normalized == "1" {
		return true
	}
	if normalized == "10" || strings.HasPrefix(normalized, "10/") ||
		normalized == "172.16" || strings.HasPrefix(normalized, "172.16/") ||
		normalized == "192.168" || strings.HasPrefix(normalized, "192.168/") {
		return false
	}
	slash := strings.LastIndex(normalized, "/")
	if slash < 0 {
		return false
	}
	prefix, err := strconv.Atoi(normalized[slash+1:])
	return err == nil && prefix <= 8
}

func buildIndependentCoreConfig(config routingConfig, module routingInterfaceInfo) ([]byte, error) {
	config, err := normalizeRoutingConfig(config)
	if err != nil {
		return nil, err
	}

	patternsByAction := map[string][]string{
		routingActionModuleDirect: {},
		routingActionSystemDirect: {},
		routingActionSystemSOCKS:  {},
	}
	for _, application := range config.Applications {
		bundlePrefix := strings.TrimSuffix(application.BundlePath, string(filepath.Separator)) + string(filepath.Separator)
		patternsByAction[application.Action] = append(patternsByAction[application.Action], "^"+regexp.QuoteMeta(bundlePrefix))
	}

	outbounds := []map[string]any{
		{"type": "direct", "tag": "system-direct"},
		{"type": "direct", "tag": "module-direct", "bind_interface": module.Name},
	}
	if len(patternsByAction[routingActionSystemSOCKS]) > 0 {
		socksOutbound := map[string]any{
			"type":        "socks",
			"tag":         "system-socks",
			"server":      config.SystemSOCKS.Server,
			"server_port": config.SystemSOCKS.Port,
			"version":     "5",
		}
		if isLoopbackSOCKSServer(config.SystemSOCKS.Server) {
			socksOutbound["bind_interface"] = "lo0"
		}
		if config.SystemSOCKS.Username != "" {
			socksOutbound["username"] = config.SystemSOCKS.Username
			socksOutbound["password"] = config.SystemSOCKS.Password
		}
		outbounds = append(outbounds, socksOutbound)
	}

	rules := make([]map[string]any, 0, 3)
	for _, route := range []struct {
		action   string
		outbound string
	}{
		{routingActionModuleDirect, "module-direct"},
		{routingActionSystemSOCKS, "system-socks"},
		{routingActionSystemDirect, "system-direct"},
	} {
		patterns := patternsByAction[route.action]
		if len(patterns) == 0 {
			continue
		}
		rules = append(rules, map[string]any{
			"inbound":            []string{"tun-in"},
			"process_path_regex": patterns,
			"action":             "route",
			"outbound":           route.outbound,
		})
	}

	coreConfig := map[string]any{
		"log": map[string]any{"level": "info", "timestamp": true},
		"inbounds": []map[string]any{{
			"type":         "tun",
			"tag":          "tun-in",
			"address":      []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
			"mtu":          1500,
			"auto_route":   true,
			"strict_route": true,
			"stack":        "mixed",
			"route_exclude_address": []string{
				"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
				"169.254.0.0/16", "224.0.0.0/4", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
			},
		}},
		"outbounds": outbounds,
		"route": map[string]any{
			"auto_detect_interface": true,
			"rules":                 rules,
			"final":                 "system-direct",
		},
	}
	return json.MarshalIndent(coreConfig, "", "  ")
}

func buildClashManagedCoreConfig(config routingConfig, module routingInterfaceInfo) ([]byte, error) {
	config, err := normalizeRoutingConfig(config)
	if err != nil {
		return nil, err
	}
	coreConfig := map[string]any{
		"log": map[string]any{"level": "info", "timestamp": true},
		"inbounds": []map[string]any{{
			"type":        "socks",
			"tag":         "module-socks",
			"listen":      "127.0.0.1",
			"listen_port": config.ClashListenPort,
		}},
		"outbounds": []map[string]any{{
			"type":           "direct",
			"tag":            "module-direct",
			"bind_interface": module.Name,
		}},
		"route": map[string]any{"final": "module-direct"},
	}
	return json.MarshalIndent(coreConfig, "", "  ")
}

func isLoopbackSOCKSServer(server string) bool {
	host := strings.Trim(strings.TrimSpace(server), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeRoutingCoreConfig(path string, data []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func waitForTCP(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError error
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		lastError = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastError == nil {
		lastError = errors.New("等待监听端口超时")
	}
	return lastError
}
