package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	routingServiceLabel           = "com.djonehub.routing"
	routingServiceProtocolVersion = "5-sing-box-1.13.16-djonehub.1"
	routingServiceDirectory       = "/Library/PrivilegedHelperTools/com.djonehub.routing"
	routingServiceHelperPath      = routingServiceDirectory + "/djonehub-routing-helper"
	routingServiceCorePath        = routingServiceDirectory + "/sing-box"
	routingServiceSocketPath      = routingServiceDirectory + "/control.sock"
	routingServiceLogPath         = routingServiceDirectory + "/network-core.log"
	routingServicePlistPath       = "/Library/LaunchDaemons/com.djonehub.routing.plist"
)

type routingServiceStatus struct {
	Version string
	State   string
	CorePID int
}

func (m *routingManager) routingServiceInstalled() bool {
	if m.routingServiceFilesInstalled() {
		return true
	}
	status, err := m.queryRoutingService()
	return err == nil && status.Version == routingServiceProtocolVersion
}

func (m *routingManager) routingServiceCurrent() bool {
	status, err := m.queryRoutingService()
	return err == nil && status.Version == routingServiceProtocolVersion
}

func (m *routingManager) routingServiceFilesInstalled() bool {
	for _, path := range []string{routingServiceHelperPath, routingServiceCorePath, routingServicePlistPath} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
			return false
		}
	}
	return true
}

func routingServiceArtifactsPresent() bool {
	for _, path := range []string{routingServiceDirectory, routingServicePlistPath} {
		if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
			return true
		}
	}
	return false
}

func (m *routingManager) queryRoutingService() (routingServiceStatus, error) {
	response, err := sendRoutingServiceCommand(routingServiceSocketPath, "STATUS")
	if err != nil {
		return routingServiceStatus{}, err
	}
	return parseRoutingServiceStatus(response)
}

func parseRoutingServiceStatus(response string) (routingServiceStatus, error) {
	fields := strings.Fields(response)
	if len(fields) != 3 {
		return routingServiceStatus{}, fmt.Errorf("invalid routing service status %q", response)
	}
	pid, err := strconv.Atoi(fields[2])
	if err != nil || pid < 0 {
		return routingServiceStatus{}, fmt.Errorf("invalid routing service pid %q", fields[2])
	}
	if fields[1] != "running" && fields[1] != "stopped" {
		return routingServiceStatus{}, fmt.Errorf("invalid routing service state %q", fields[1])
	}
	if (fields[1] == "running" && pid <= 1) || (fields[1] == "stopped" && pid != 0) {
		return routingServiceStatus{}, fmt.Errorf("invalid routing service state/pid pair %q", response)
	}
	return routingServiceStatus{Version: fields[0], State: fields[1], CorePID: pid}, nil
}

func (m *routingManager) ensureRoutingService(ctx context.Context) error {
	if status, err := m.queryRoutingService(); err == nil && status.Version == routingServiceProtocolVersion {
		return nil
	}
	return m.installRoutingService(ctx)
}

func (m *routingManager) installRoutingService(ctx context.Context) error {
	for label, path := range map[string]string{
		"helper": m.helperPath,
		"core":   m.corePath,
	} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("应用包中的 TUN %s 不可用", label)
		}
	}

	plist, err := m.routingServicePlist()
	if err != nil {
		return fmt.Errorf("生成 TUN 服务配置失败：%w", err)
	}
	plistBase64 := base64.StdEncoding.EncodeToString(plist)
	script := strings.Join([]string{
		"set -eu",
		"/bin/launchctl bootout system/" + routingServiceLabel + " >/dev/null 2>&1 || true",
		"/bin/rm -f " + shellQuote(routingServiceSocketPath),
		"/bin/mkdir -p " + shellQuote(routingServiceDirectory),
		"/usr/bin/install -o root -g wheel -m 0555 " + shellQuote(m.helperPath) + " " + shellQuote(routingServiceHelperPath),
		"/usr/bin/install -o root -g wheel -m 0555 " + shellQuote(m.corePath) + " " + shellQuote(routingServiceCorePath),
		"/bin/chmod 0755 " + shellQuote(routingServiceDirectory),
		"/usr/sbin/chown -R root:wheel " + shellQuote(routingServiceDirectory),
		"/bin/echo " + shellQuote(plistBase64) + " | /usr/bin/base64 -D > " + shellQuote(routingServicePlistPath),
		"/bin/chmod 0644 " + shellQuote(routingServicePlistPath),
		"/usr/sbin/chown root:wheel " + shellQuote(routingServicePlistPath),
		"/bin/launchctl bootstrap system " + shellQuote(routingServicePlistPath),
	}, "\n")
	if err := runRoutingAdministratorScript(ctx, script); err != nil {
		return fmt.Errorf("安装 TUN 服务失败：%w", err)
	}

	deadline := time.Now().Add(12 * time.Second)
	var lastError error
	for time.Now().Before(deadline) {
		status, statusError := m.queryRoutingService()
		if statusError == nil && status.Version == routingServiceProtocolVersion {
			return nil
		}
		if statusError != nil {
			lastError = statusError
		} else {
			lastError = fmt.Errorf("服务协议版本不匹配：%s", status.Version)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("TUN 服务安装后未能启动：%w", lastError)
}

func (m *routingManager) uninstallRoutingService(ctx context.Context) error {
	if !m.routingServiceInstalled() && !routingServiceArtifactsPresent() {
		removeLegacyRoutingServiceSocket(m.dataDir)
		return nil
	}

	script := strings.Join([]string{
		"set -eu",
		"/bin/launchctl bootout system/" + routingServiceLabel + " >/dev/null 2>&1 || true",
		"/bin/rm -f " + shellQuote(routingServicePlistPath),
		"/bin/rm -rf " + shellQuote(routingServiceDirectory),
	}, "\n")
	if err := runRoutingAdministratorScript(ctx, script); err != nil {
		return fmt.Errorf("卸载 TUN 服务失败：%w", err)
	}
	removeLegacyRoutingServiceSocket(m.dataDir)
	if m.routingServiceInstalled() {
		return errors.New("TUN 服务仍存在，请重新尝试卸载")
	}
	return nil
}

func (m *routingManager) routingServicePlist() ([]byte, error) {
	values := map[string]string{
		"label":       routingServiceLabel,
		"helper":      routingServiceHelperPath,
		"core":        routingServiceCorePath,
		"config":      m.coreConfigPath,
		"log":         routingServiceLogPath,
		"socket":      routingServiceSocketPath,
		"user_uid":    strconv.Itoa(os.Getuid()),
		"user_gid":    strconv.Itoa(os.Getgid()),
		"working_dir": routingServiceDirectory,
	}
	for key, value := range values {
		var escaped bytes.Buffer
		if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
			return nil, fmt.Errorf("escape %s: %w", key, err)
		}
		values[key] = escaped.String()
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>` + values["label"] + `</string>
    <key>ProgramArguments</key>
    <array>
        <string>` + values["helper"] + `</string>
        <string>-socket</string>
        <string>` + values["socket"] + `</string>
        <string>-core</string>
        <string>` + values["core"] + `</string>
        <string>-config</string>
        <string>` + values["config"] + `</string>
        <string>-log</string>
        <string>` + values["log"] + `</string>
        <string>-user-uid</string>
        <string>` + values["user_uid"] + `</string>
        <string>-user-gid</string>
        <string>` + values["user_gid"] + `</string>
    </array>
    <key>WorkingDirectory</key>
    <string>` + values["working_dir"] + `</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>ThrottleInterval</key>
    <integer>2</integer>
    <key>Umask</key>
    <integer>63</integer>
</dict>
</plist>
`
	return []byte(plist), nil
}

func runRoutingAdministratorScript(ctx context.Context, commandText string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(commandText))
	script := fmt.Sprintf("do shell script \"echo %s | base64 -d | sh\" with administrator privileges", encoded)
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	if strings.Contains(strings.ToLower(detail), "user canceled") || strings.Contains(detail, "-128") {
		return errors.New("已取消管理员授权")
	}
	return errors.New(detail)
}

func removeLegacyRoutingServiceSocket(dataDir string) {
	_ = os.Remove(filepath.Join(dataDir, "network-routing-control.sock"))
}
