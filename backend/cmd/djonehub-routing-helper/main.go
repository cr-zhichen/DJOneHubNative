package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const serviceProtocolVersion = "5-sing-box-1.13.16-djonehub.1"

var expectedRouteExclusions = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "198.18.0.0/15", "224.0.0.0/4",
	"::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
}

type managedCoreConfig struct {
	Log struct {
		Level     string `json:"level"`
		Timestamp bool   `json:"timestamp"`
	} `json:"log"`
	DNS struct {
		Servers []struct {
			Type       string `json:"type"`
			Tag        string `json:"tag"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port,omitempty"`
			Detour     string `json:"detour"`
		} `json:"servers"`
		Final string `json:"final"`
	} `json:"dns,omitempty"`
	Inbounds []struct {
		Type                string   `json:"type"`
		Tag                 string   `json:"tag"`
		Address             []string `json:"address"`
		MTU                 int      `json:"mtu"`
		AutoRoute           bool     `json:"auto_route"`
		StrictRoute         bool     `json:"strict_route"`
		Stack               string   `json:"stack"`
		RouteExcludeAddress []string `json:"route_exclude_address"`
	} `json:"inbounds"`
	Outbounds []struct {
		Type             string `json:"type"`
		Tag              string `json:"tag"`
		BindInterface    string `json:"bind_interface,omitempty"`
		Inet4BindAddress string `json:"inet4_bind_address,omitempty"`
		Inet6BindAddress string `json:"inet6_bind_address,omitempty"`
		Server           string `json:"server,omitempty"`
		ServerPort       int    `json:"server_port,omitempty"`
		Version          string `json:"version,omitempty"`
		Username         string `json:"username,omitempty"`
		Password         string `json:"password,omitempty"`
	} `json:"outbounds"`
	Route struct {
		AutoDetectInterface bool `json:"auto_detect_interface"`
		FindProcess         bool `json:"find_process"`
		Rules               []struct {
			Inbound          []string `json:"inbound"`
			Port             int      `json:"port,omitempty"`
			ProcessPathRegex []string `json:"process_path_regex"`
			Action           string   `json:"action"`
			Outbound         string   `json:"outbound,omitempty"`
		} `json:"rules"`
		Final string `json:"final"`
	} `json:"route"`
}

type serviceOptions struct {
	SocketPath string
	CorePath   string
	ConfigPath string
	LogPath    string
	UserUID    int
	UserGID    int
}

type routingService struct {
	mu sync.Mutex

	options     serviceOptions
	listener    net.Listener
	command     *exec.Cmd
	commandDone chan struct{}
	generation  uint64
}

func main() {
	options := serviceOptions{UserUID: -1, UserGID: -1}
	var checkConfigPath string
	flag.StringVar(&checkConfigPath, "check-config", "", "validate a DJOneHub managed TUN configuration")
	flag.StringVar(&options.SocketPath, "socket", "", "control socket path")
	flag.StringVar(&options.CorePath, "core", "", "managed sing-box path")
	flag.StringVar(&options.ConfigPath, "config", "", "managed sing-box config path")
	flag.StringVar(&options.LogPath, "log", "", "managed sing-box log path")
	flag.IntVar(&options.UserUID, "user-uid", -1, "owning user uid")
	flag.IntVar(&options.UserGID, "user-gid", -1, "owning user gid")
	flag.Parse()
	if checkConfigPath != "" {
		if _, err := loadManagedCoreConfig(checkConfigPath, os.Getuid()); err != nil {
			fatal(err)
		}
		return
	}

	service, err := newRoutingService(options)
	if err != nil {
		fatal(err)
	}
	if err := service.run(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "DJOneHub routing service:", err)
	os.Exit(1)
}

func newRoutingService(options serviceOptions) (*routingService, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("administrator privileges are required")
	}
	if options.UserUID < 0 || options.UserGID < 0 {
		return nil, errors.New("invalid owning user metadata")
	}
	for label, path := range map[string]string{
		"socket": options.SocketPath,
		"core":   options.CorePath,
		"config": options.ConfigPath,
		"log":    options.LogPath,
	} {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("%s path must be absolute", label)
		}
	}
	protectedDirectory := filepath.Dir(options.CorePath)
	for label, path := range map[string]string{
		"socket": options.SocketPath,
		"log":    options.LogPath,
	} {
		if filepath.Dir(path) != protectedDirectory {
			return nil, fmt.Errorf("%s path must share the protected core directory", label)
		}
	}
	info, err := os.Stat(protectedDirectory)
	if err != nil || !info.IsDir() {
		return nil, errors.New("protected core directory is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("protected core directory ownership or permissions are unsafe")
	}
	return &routingService{options: options}, nil
}

func (s *routingService) run() error {
	_ = os.Remove(s.options.SocketPath)
	listener, err := net.Listen("unix", s.options.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on control socket: %w", err)
	}
	s.listener = listener
	defer func() {
		_ = listener.Close()
		_ = os.Remove(s.options.SocketPath)
	}()
	if err := os.Chmod(s.options.SocketPath, 0o600); err != nil {
		return fmt.Errorf("secure control socket: %w", err)
	}
	if err := os.Chown(s.options.SocketPath, s.options.UserUID, s.options.UserGID); err != nil {
		return fmt.Errorf("assign control socket: %w", err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
		s.stop()
		_ = listener.Close()
	}()

	for {
		connection, acceptError := listener.Accept()
		if acceptError != nil {
			if errors.Is(acceptError, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept control connection: %w", acceptError)
		}
		go s.serveConnection(connection)
	}
}

func (s *routingService) serveConnection(connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(12 * time.Second))
	line, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		_, _ = fmt.Fprintln(connection, "ERROR invalid request")
		return
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		_, _ = fmt.Fprintln(connection, "ERROR invalid request")
		return
	}

	switch strings.ToUpper(fields[0]) {
	case "STATUS":
		state, pid := s.status()
		_, _ = fmt.Fprintf(connection, "OK %s %s %d\n", serviceProtocolVersion, state, pid)
	case "START":
		if len(fields) != 2 {
			_, _ = fmt.Fprintln(connection, "ERROR START requires the client pid")
			return
		}
		parentPID, parseError := strconv.Atoi(fields[1])
		if parseError != nil || parentPID <= 1 {
			_, _ = fmt.Fprintln(connection, "ERROR invalid client pid")
			return
		}
		pid, startError := s.start(parentPID)
		if startError != nil {
			_, _ = fmt.Fprintln(connection, "ERROR "+startError.Error())
			return
		}
		_, _ = fmt.Fprintf(connection, "OK %d\n", pid)
	case "STOP":
		s.stop()
		_, _ = fmt.Fprintln(connection, "OK")
	default:
		_, _ = fmt.Fprintln(connection, "ERROR unsupported command")
	}
}

func (s *routingService) status() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.command == nil || s.command.Process == nil {
		return "stopped", 0
	}
	return "running", s.command.Process.Pid
}

func (s *routingService) start(parentPID int) (int, error) {
	s.mu.Lock()
	previousGeneration := s.generation
	hasPreviousCore := s.command != nil && s.command.Process != nil
	s.mu.Unlock()
	if hasPreviousCore {
		s.stopGeneration(previousGeneration)
	}

	s.mu.Lock()
	if s.command != nil && s.command.Process != nil {
		s.mu.Unlock()
		return 0, errors.New("previous network core did not stop")
	}
	if err := syscall.Kill(parentPID, 0); err != nil {
		s.mu.Unlock()
		return 0, errors.New("DJOneHub is no longer running")
	}
	if info, err := os.Stat(s.options.CorePath); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		s.mu.Unlock()
		return 0, errors.New("managed network core is unavailable")
	}
	configData, err := loadManagedCoreConfig(s.options.ConfigPath, s.options.UserUID)
	if err != nil {
		s.mu.Unlock()
		return 0, fmt.Errorf("managed network configuration is invalid: %w", err)
	}
	runtimeConfigPath := filepath.Join(filepath.Dir(s.options.CorePath), "runtime.json")
	temporaryConfigPath := runtimeConfigPath + ".tmp"
	_ = os.Remove(temporaryConfigPath)
	if err := os.WriteFile(temporaryConfigPath, configData, 0o600); err != nil {
		s.mu.Unlock()
		return 0, fmt.Errorf("write protected network configuration: %w", err)
	}
	if err := os.Rename(temporaryConfigPath, runtimeConfigPath); err != nil {
		_ = os.Remove(temporaryConfigPath)
		s.mu.Unlock()
		return 0, fmt.Errorf("activate protected network configuration: %w", err)
	}

	logFile, err := os.OpenFile(s.options.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = os.Remove(runtimeConfigPath)
		s.mu.Unlock()
		return 0, fmt.Errorf("open network core log: %w", err)
	}
	if os.Geteuid() == 0 {
		if err := logFile.Chown(0, s.options.UserGID); err != nil {
			_ = logFile.Close()
			_ = os.Remove(runtimeConfigPath)
			s.mu.Unlock()
			return 0, fmt.Errorf("assign network core log: %w", err)
		}
	}
	if err := logFile.Chmod(0o640); err != nil {
		_ = logFile.Close()
		_ = os.Remove(runtimeConfigPath)
		s.mu.Unlock()
		return 0, fmt.Errorf("secure network core log: %w", err)
	}

	command := exec.Command(
		s.options.CorePath,
		"run", "--disable-color",
		"-D", filepath.Dir(runtimeConfigPath),
		"-c", runtimeConfigPath,
	)
	protectedDirectory := filepath.Dir(s.options.CorePath)
	command.Env = []string{
		"HOME=" + protectedDirectory,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"TMPDIR=" + protectedDirectory,
	}
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		_ = os.Remove(runtimeConfigPath)
		s.mu.Unlock()
		return 0, fmt.Errorf("start network core: %w", err)
	}

	done := make(chan struct{})
	s.generation++
	generation := s.generation
	s.command = command
	s.commandDone = done
	s.mu.Unlock()

	go s.waitForCore(generation, command, logFile, runtimeConfigPath, done)
	go s.monitorClient(generation, parentPID)
	return command.Process.Pid, nil
}

func (s *routingService) waitForCore(
	generation uint64,
	command *exec.Cmd,
	logFile *os.File,
	runtimeConfigPath string,
	done chan struct{},
) {
	waitError := command.Wait()
	if waitError != nil {
		_, _ = fmt.Fprintf(logFile, "\nDJOneHub routing service: network core exited: %v\n", waitError)
	}
	_ = logFile.Close()
	_ = os.Remove(runtimeConfigPath)

	s.mu.Lock()
	if s.generation == generation && s.command == command {
		s.command = nil
		s.commandDone = nil
	}
	close(done)
	s.mu.Unlock()
}

func loadManagedCoreConfig(path string, userUID int) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("configuration file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("configuration file is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != userUID || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("configuration file ownership or permissions are unsafe")
	}
	if info.Size() <= 0 || info.Size() > 1024*1024 {
		return nil, errors.New("configuration file size is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > 1024*1024 {
		return nil, errors.New("configuration file size is invalid")
	}

	var config managedCoreConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("configuration contains trailing data")
	}
	if err := validateManagedCoreConfig(config); err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func validateManagedCoreConfig(config managedCoreConfig) error {
	if config.Log.Level != "info" || !config.Log.Timestamp {
		return errors.New("unexpected log configuration")
	}
	if len(config.Inbounds) != 1 {
		return errors.New("exactly one TUN inbound is required")
	}
	inbound := config.Inbounds[0]
	if inbound.Type != "tun" || inbound.Tag != "tun-in" || inbound.MTU != 1500 ||
		!inbound.AutoRoute || !inbound.StrictRoute || inbound.Stack != "gvisor" ||
		!slices.Equal(inbound.Address, []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}) ||
		!slices.Equal(inbound.RouteExcludeAddress, expectedRouteExclusions) {
		return errors.New("unexpected TUN inbound configuration")
	}

	if len(config.Outbounds) < 2 || len(config.Outbounds) > 3 {
		return errors.New("unexpected outbound count")
	}
	outboundTags := make(map[string]struct{}, len(config.Outbounds))
	var systemInterface string
	var moduleInterface string
	var socksInterface string
	for _, outbound := range config.Outbounds {
		if _, exists := outboundTags[outbound.Tag]; exists {
			return fmt.Errorf("duplicate outbound %q", outbound.Tag)
		}
		outboundTags[outbound.Tag] = struct{}{}
		switch outbound.Tag {
		case "system-direct":
			if outbound.Type != "direct" || strings.TrimSpace(outbound.BindInterface) == "" ||
				outbound.Inet4BindAddress != "" || outbound.Inet6BindAddress != "" {
				return errors.New("unexpected system direct outbound")
			}
			systemInterface = outbound.BindInterface
		case "module-direct":
			if outbound.Type != "direct" || strings.TrimSpace(outbound.BindInterface) == "" ||
				net.ParseIP(outbound.Inet4BindAddress).To4() == nil {
				return errors.New("unexpected module direct outbound")
			}
			if outbound.Inet6BindAddress != "" {
				ipv6 := net.ParseIP(outbound.Inet6BindAddress)
				if ipv6 == nil || ipv6.To4() != nil || !ipv6.IsGlobalUnicast() || ipv6.IsPrivate() || ipv6.IsLinkLocalUnicast() {
					return errors.New("unexpected module IPv6 bind address")
				}
			}
			moduleInterface = outbound.BindInterface
		case "system-socks":
			if outbound.Type != "socks" || strings.TrimSpace(outbound.Server) == "" ||
				outbound.ServerPort < 1 || outbound.ServerPort > 65535 || outbound.Version != "5" ||
				strings.TrimSpace(outbound.BindInterface) == "" {
				return errors.New("unexpected SOCKS outbound")
			}
			socksInterface = outbound.BindInterface
		default:
			return fmt.Errorf("unsupported outbound %q", outbound.Tag)
		}
	}
	if _, exists := outboundTags["system-direct"]; !exists {
		return errors.New("system direct outbound is missing")
	}
	if _, exists := outboundTags["module-direct"]; !exists {
		return errors.New("module direct outbound is missing")
	}
	if systemInterface == moduleInterface {
		return errors.New("system and module direct outbounds must use different interfaces")
	}
	if socksInterface != "" && socksInterface != "lo0" && socksInterface != systemInterface {
		return errors.New("SOCKS outbound must use loopback or the system interface")
	}

	if !config.Route.AutoDetectInterface || len(config.Route.Rules) > 4 ||
		config.Route.FindProcess != (len(config.Route.Rules) > 0) {
		return errors.New("unexpected route configuration")
	}
	if _, exists := outboundTags[config.Route.Final]; !exists {
		return errors.New("route final references an unknown outbound")
	}
	patternCount := 0
	hijackDNSRules := 0
	for _, rule := range config.Route.Rules {
		if !slices.Equal(rule.Inbound, []string{"tun-in"}) {
			return errors.New("unexpected route rule")
		}
		switch rule.Action {
		case "route":
			if rule.Port != 0 || len(rule.ProcessPathRegex) == 0 {
				return errors.New("unexpected route rule")
			}
			if _, exists := outboundTags[rule.Outbound]; !exists {
				return fmt.Errorf("route references unknown outbound %q", rule.Outbound)
			}
		case "hijack-dns":
			if rule.Port != 53 || rule.Outbound != "" {
				return errors.New("unexpected DNS hijack rule")
			}
			hijackDNSRules++
		default:
			return errors.New("unexpected route rule action")
		}
		for _, pattern := range rule.ProcessPathRegex {
			if pattern == "" || len(pattern) > 4096 || strings.ContainsAny(pattern, "\r\n\x00") {
				return errors.New("invalid process path rule")
			}
			patternCount++
		}
	}
	if patternCount > 256 {
		return errors.New("too many process path rules")
	}
	if hijackDNSRules == 0 {
		if len(config.DNS.Servers) != 0 || config.DNS.Final != "" {
			return errors.New("DNS configuration exists without a hijack rule")
		}
		return nil
	}
	if hijackDNSRules != 1 || len(config.DNS.Servers) != 1 || config.DNS.Final != "module-dns" {
		return errors.New("unexpected managed DNS configuration")
	}
	dnsServer := config.DNS.Servers[0]
	serverIP := net.ParseIP(dnsServer.Server)
	if dnsServer.Type != "udp" || dnsServer.Tag != "module-dns" ||
		serverIP == nil || serverIP.To4() == nil || serverIP.IsUnspecified() || serverIP.IsLoopback() || serverIP.IsMulticast() ||
		(dnsServer.ServerPort != 0 && dnsServer.ServerPort != 53) || dnsServer.Detour != "module-direct" {
		return errors.New("unexpected managed DNS server")
	}
	return nil
}

func (s *routingService) monitorClient(generation uint64, parentPID int) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		active := s.generation == generation && s.command != nil
		s.mu.Unlock()
		if !active {
			return
		}
		if err := syscall.Kill(parentPID, 0); err == nil || errors.Is(err, syscall.EPERM) {
			continue
		}
		s.stopGeneration(generation)
		return
	}
}

func (s *routingService) stop() {
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	s.stopGeneration(generation)
}

func (s *routingService) stopGeneration(generation uint64) {
	s.mu.Lock()
	if s.generation != generation || s.command == nil || s.command.Process == nil {
		s.mu.Unlock()
		return
	}
	command := s.command
	done := s.commandDone
	s.mu.Unlock()

	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
}
