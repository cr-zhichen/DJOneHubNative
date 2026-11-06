package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusProtocolReportsStoppedService(t *testing.T) {
	service := &routingService{}
	server, client := net.Pipe()
	defer client.Close()
	go service.serveConnection(server)

	if _, err := client.Write([]byte("STATUS\n")); err != nil {
		t.Fatalf("write status: %v", err)
	}
	response, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	want := "OK " + serviceProtocolVersion + " stopped 0"
	if strings.TrimSpace(response) != want {
		t.Fatalf("response=%q, want %q", strings.TrimSpace(response), want)
	}
}

func TestProtocolRejectsUnsupportedCommand(t *testing.T) {
	service := &routingService{}
	server, client := net.Pipe()
	defer client.Close()
	go service.serveConnection(server)

	if _, err := client.Write([]byte("RELOAD\n")); err != nil {
		t.Fatalf("write command: %v", err)
	}
	response, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.HasPrefix(response, "ERROR ") {
		t.Fatalf("response=%q, want ERROR", response)
	}
}

func TestServiceStartsAndStopsManagedCore(t *testing.T) {
	directory := t.TempDir()
	corePath := filepath.Join(directory, "fake-core")
	configPath := filepath.Join(directory, "config.json")
	logPath := filepath.Join(directory, "core.log")
	if err := os.WriteFile(corePath, []byte("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatalf("write fake core: %v", err)
	}
	if err := os.WriteFile(configPath, validManagedConfig(), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	service := &routingService{options: serviceOptions{
		CorePath:   corePath,
		ConfigPath: configPath,
		LogPath:    logPath,
		UserUID:    os.Getuid(),
		UserGID:    os.Getgid(),
	}}

	pid, err := service.start(os.Getpid())
	if err != nil {
		t.Fatalf("start core: %v", err)
	}
	if state, statusPID := service.status(); state != "running" || statusPID != pid {
		t.Fatalf("status=%s pid=%d, want running %d", state, statusPID, pid)
	}
	service.stop()
	if state, statusPID := service.status(); state != "stopped" || statusPID != 0 {
		t.Fatalf("status=%s pid=%d, want stopped", state, statusPID)
	}
	if _, err := os.Stat(filepath.Join(directory, "runtime.json")); !os.IsNotExist(err) {
		t.Fatalf("protected runtime config was not removed: %v", err)
	}
}

func TestManagedConfigRejectsUnexpectedFields(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	invalid := strings.Replace(string(validManagedConfig()), `"timestamp": true`, `"timestamp": true, "output": "/etc/hosts"`, 1)
	if err := os.WriteFile(configPath, []byte(invalid), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadManagedCoreConfig(configPath, os.Getuid()); err == nil {
		t.Fatal("unexpected root file output must be rejected")
	}
}

func TestManagedConfigRejectsNonGlobalIPv6BindAddress(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	invalid := strings.Replace(string(validManagedConfig()), "240e:471:c30:6ad4::20", "fd00::20", 1)
	if err := os.WriteFile(configPath, []byte(invalid), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadManagedCoreConfig(configPath, os.Getuid()); err == nil {
		t.Fatal("private IPv6 bind address must be rejected")
	}
}

func TestManagedConfigRejectsSymbolicLink(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "target.json")
	linkPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(targetPath, validManagedConfig(), 0o600); err != nil {
		t.Fatalf("write config target: %v", err)
	}
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("create config symlink: %v", err)
	}
	if _, err := loadManagedCoreConfig(linkPath, os.Getuid()); err == nil {
		t.Fatal("configuration symlink must be rejected")
	}
}

func validManagedConfig() []byte {
	return []byte(`{
  "log": {"level": "info", "timestamp": true},
  "inbounds": [{
    "type": "tun",
    "tag": "tun-in",
    "address": ["172.19.0.1/30", "fdfe:dcba:9876::1/126"],
    "mtu": 1500,
    "auto_route": true,
    "strict_route": true,
    "stack": "gvisor",
    "route_exclude_address": [
      "127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
      "169.254.0.0/16", "198.18.0.0/15", "224.0.0.0/4",
      "::1/128", "fc00::/7", "fe80::/10", "ff00::/8"
    ]
  }],
  "outbounds": [
    {"type": "direct", "tag": "system-direct", "bind_interface": "en7"},
    {"type": "direct", "tag": "module-direct", "bind_interface": "en8", "inet4_bind_address": "192.168.225.20", "inet6_bind_address": "240e:471:c30:6ad4::20"}
  ],
  "route": {"auto_detect_interface": true, "rules": [], "final": "system-direct"}
}`)
}

// 无模块场景：规则不使用「模块直连」时，仅 system-direct 也必须通过校验。
func TestManagedConfigAcceptsMissingModuleDirect(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	withoutModule := strings.Replace(string(validManagedConfig()),
		`    {"type": "direct", "tag": "module-direct", "bind_interface": "en8", "inet4_bind_address": "192.168.225.20", "inet6_bind_address": "240e:471:c30:6ad4::20"}`+"\n", "", 1)
	withoutModule = strings.Replace(withoutModule,
		`{"type": "direct", "tag": "system-direct", "bind_interface": "en7"},`,
		`{"type": "direct", "tag": "system-direct", "bind_interface": "en7"}`, 1)
	if strings.Contains(withoutModule, "module-direct") {
		t.Fatal("fixture still contains module-direct")
	}
	if err := os.WriteFile(configPath, []byte(withoutModule), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadManagedCoreConfig(configPath, os.Getuid()); err != nil {
		t.Fatalf("config without module-direct must be accepted: %v", err)
	}
}

// DNS 出站经模块网关，出现 DNS 配置但没有 module-direct 必须拒绝。
func TestManagedConfigRejectsDNSWithoutModuleDirect(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	withoutModule := strings.Replace(string(validManagedConfig()),
		`    {"type": "direct", "tag": "module-direct", "bind_interface": "en8", "inet4_bind_address": "192.168.225.20", "inet6_bind_address": "240e:471:c30:6ad4::20"}`+"\n", "", 1)
	withoutModule = strings.Replace(withoutModule,
		`{"type": "direct", "tag": "system-direct", "bind_interface": "en7"},`,
		`{"type": "direct", "tag": "system-direct", "bind_interface": "en7"}`, 1)
	withDNS := strings.Replace(withoutModule,
		`"route": {"auto_detect_interface": true, "rules": [], "final": "system-direct"}`,
		`"dns": {"servers": [{"type": "udp", "tag": "module-dns", "server": "192.168.225.1", "detour": "module-direct"}], "final": "module-dns"}, "route": {"auto_detect_interface": true, "rules": [{"inbound": ["tun-in"], "port": 53, "action": "hijack-dns"}], "final": "system-direct"}`,
		1)
	if strings.Contains(withDNS, `"tag": "module-direct"`) {
		t.Fatal("fixture still contains module-direct outbound")
	}
	if err := os.WriteFile(configPath, []byte(withDNS), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadManagedCoreConfig(configPath, os.Getuid()); err == nil {
		t.Fatal("DNS configuration without module-direct outbound must be rejected")
	}
}
