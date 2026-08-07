package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type routingRuntime struct {
	Enabled         bool                  `json:"enabled"`
	State           string                `json:"state"`
	Mode            string                `json:"mode,omitempty"`
	Message         string                `json:"message,omitempty"`
	ModuleInterface *routingInterfaceInfo `json:"module_interface,omitempty"`
	SystemInterface string                `json:"system_interface,omitempty"`
	SOCKSAddress    string                `json:"socks_address,omitempty"`
	StartedAt       *time.Time            `json:"started_at,omitempty"`
}

type routingCapabilities struct {
	CoreAvailable    bool   `json:"core_available"`
	CoreVersion      string `json:"core_version,omitempty"`
	CorePath         string `json:"core_path,omitempty"`
	ServiceInstalled bool   `json:"service_installed"`
	ServiceCurrent   bool   `json:"service_current"`
}

type routingSnapshot struct {
	Config       routingConfig       `json:"config"`
	Runtime      routingRuntime      `json:"runtime"`
	Capabilities routingCapabilities `json:"capabilities"`
}

type routingManager struct {
	mu sync.Mutex

	dataDir         string
	configPath      string
	coreConfigPath  string
	coreLogPath     string
	corePath        string
	helperPath      string
	userHome        string
	config          routingConfig
	runtime         routingRuntime
	coreVersion     string
	generation      uint64
	userCommand     *exec.Cmd
	userCommandDone chan struct{}
}

type routingOperationError struct {
	Status  int
	Message string
}

func (e *routingOperationError) Error() string { return e.Message }

func newRoutingManager(dataDir string) *routingManager {
	backendPath, _ := os.Executable()
	corePath := filepath.Join(filepath.Dir(backendPath), "sing-box")
	helperPath := filepath.Join(filepath.Dir(backendPath), "djonehub-routing-helper")
	manager := &routingManager{
		dataDir:        dataDir,
		configPath:     filepath.Join(dataDir, "network-routing.json"),
		coreConfigPath: filepath.Join(dataDir, "network-core.json"),
		coreLogPath:    filepath.Join(dataDir, "network-core.log"),
		corePath:       corePath,
		helperPath:     helperPath,
		userHome:       os.Getenv("HOME"),
		config:         loadRoutingConfig(filepath.Join(dataDir, "network-routing.json")),
		runtime: routingRuntime{
			State: "stopped",
		},
	}
	manager.coreVersion = manager.readCoreVersion()
	return manager
}

func (m *routingManager) coreIsAvailable() bool {
	info, err := os.Stat(m.corePath)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

func (m *routingManager) readCoreVersion() string {
	if !m.coreIsAvailable() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.corePath, "version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(strings.TrimPrefix(line, "sing-box version"))
}

func (m *routingManager) Snapshot() routingSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return routingSnapshot{
		Config:       cloneRoutingConfig(m.config),
		Runtime:      m.runtime,
		Capabilities: m.capabilitiesSnapshot(),
	}
}

func (m *routingManager) capabilitiesSnapshot() routingCapabilities {
	return routingCapabilities{
		CoreAvailable:    m.coreIsAvailable(),
		CoreVersion:      m.coreVersion,
		CorePath:         m.corePath,
		ServiceInstalled: m.routingServiceInstalled() || routingServiceArtifactsPresent(),
		ServiceCurrent:   m.routingServiceCurrent(),
	}
}

func (m *routingManager) UpdateConfig(config routingConfig) (routingSnapshot, error) {
	normalized, err := normalizeRoutingConfig(config)
	if err != nil {
		return routingSnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime.State == "starting" || m.runtime.State == "running" || m.runtime.State == "stopping" {
		return routingSnapshot{}, errors.New("请先停用应用分流，再修改配置")
	}
	if err := saveRoutingConfig(m.configPath, normalized); err != nil {
		return routingSnapshot{}, fmt.Errorf("保存分流配置失败：%w", err)
	}
	m.config = normalized
	return routingSnapshot{
		Config:       cloneRoutingConfig(m.config),
		Runtime:      m.runtime,
		Capabilities: m.capabilitiesSnapshot(),
	}, nil
}

func (m *routingManager) Preflight(moduleProduct string) routingPreflight {
	m.mu.Lock()
	config := cloneRoutingConfig(m.config)
	running := m.runtime.State == "running"
	m.mu.Unlock()
	return m.preflightConfig(config, moduleProduct, running)
}

func (m *routingManager) PreflightConfig(config routingConfig, moduleProduct string) routingPreflight {
	m.mu.Lock()
	running := m.runtime.State == "running"
	m.mu.Unlock()
	return m.preflightConfig(config, moduleProduct, running)
}

func (m *routingManager) preflightConfig(config routingConfig, moduleProduct string, running bool) routingPreflight {
	result := routingPreflight{
		CoreAvailable: m.coreIsAvailable(),
		CoreVersion:   m.coreVersion,
		Conflicts:     []routingConflict{},
		Issues:        []string{},
		Warnings:      []string{},
	}
	if !result.CoreAvailable {
		result.Issues = append(result.Issues, "应用包中缺少网络核心 sing-box")
	}
	normalized, normalizeError := normalizeRoutingConfig(config)
	if normalizeError != nil {
		result.Issues = append(result.Issues, normalizeError.Error())
	} else {
		config = normalized
	}

	module, err := resolveRoutingModuleInterface(moduleProduct)
	if err != nil {
		result.Issues = append(result.Issues, err.Error())
	} else {
		result.ModuleInterface = &module
		if module.IPv6 == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("模块网卡 %s 未获得全局 IPv6 地址；IPv4 可用，但 4G IPv6 暂不可用", module.Name))
		} else if !routingInterfaceHasScopedIPv6DefaultRoute(module.Name) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("模块网卡 %s 已获得 IPv6 地址，但没有可用的 IPv6 默认路由", module.Name))
		}
	}

	if config.Mode == routingModeIndependent {
		route := discoverMacDefaultRoute()
		result.SystemInterface = route.Interface
		if route.Interface == "" {
			result.Issues = append(result.Issues, "没有找到系统默认网络出口")
		} else if result.ModuleInterface != nil && route.Interface == result.ModuleInterface.Name {
			result.Issues = append(result.Issues, "系统默认出口当前也是 4G 模块，请先调整网络服务优先级")
		}
		if !running {
			result.Conflicts = detectRoutingConflicts()
		}
		if normalizeError == nil && routingConfigNeedsLoopbackSOCKSBypass(config) {
			patterns, bypassError := resolveLoopbackSOCKSBypassPatterns(config.SystemSOCKS.Port)
			if bypassError != nil {
				result.Issues = append(result.Issues, bypassError.Error())
			} else {
				result.SystemSOCKSBypassPatterns = patterns
			}
		}
	} else if !running {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.ClashListenPort))
		listener, listenErr := net.Listen("tcp", address)
		if listenErr != nil {
			result.Issues = append(result.Issues, fmt.Sprintf("本地端口 %d 已被占用", config.ClashListenPort))
		} else {
			_ = listener.Close()
		}
	}
	result.Ready = len(result.Issues) == 0 && len(result.Conflicts) == 0
	return result
}

func (m *routingManager) Start(ctx context.Context, moduleProduct string) (routingSnapshot, error) {
	m.mu.Lock()
	if m.runtime.State == "starting" || m.runtime.State == "running" || m.runtime.State == "stopping" {
		m.mu.Unlock()
		return routingSnapshot{}, &routingOperationError{Status: http.StatusConflict, Message: "应用分流已经启动或正在切换"}
	}
	config := cloneRoutingConfig(m.config)
	m.generation++
	generation := m.generation
	m.runtime = routingRuntime{Enabled: true, State: "starting", Mode: config.Mode, Message: "正在检查网络环境…"}
	m.mu.Unlock()

	fail := func(status int, err error) (routingSnapshot, error) {
		m.failStart(generation, err)
		return m.Snapshot(), &routingOperationError{Status: status, Message: err.Error()}
	}

	preflight := m.Preflight(moduleProduct)
	if len(preflight.Conflicts) > 0 {
		return fail(http.StatusConflict, errors.New("检测到其他 TUN 正在接管系统流量，请先关闭其 TUN 模式"))
	}
	if len(preflight.Issues) > 0 {
		return fail(http.StatusServiceUnavailable, errors.New(strings.Join(preflight.Issues, "；")))
	}
	if preflight.ModuleInterface == nil {
		return fail(http.StatusServiceUnavailable, errors.New("没有找到可用的 4G 模块网卡"))
	}

	var coreConfig []byte
	var err error
	if config.Mode == routingModeIndependent {
		coreConfig, err = buildIndependentCoreConfig(
			config,
			*preflight.ModuleInterface,
			preflight.SystemInterface,
			preflight.SystemSOCKSBypassPatterns,
		)
	} else {
		coreConfig, err = buildClashManagedCoreConfig(config, *preflight.ModuleInterface)
	}
	if err != nil {
		return fail(http.StatusBadRequest, err)
	}
	if err := writeRoutingCoreConfig(m.coreConfigPath, coreConfig); err != nil {
		return fail(http.StatusInternalServerError, fmt.Errorf("写入网络核心配置失败：%w", err))
	}
	if err := m.checkCoreConfig(ctx); err != nil {
		return fail(http.StatusBadRequest, err)
	}
	if config.Mode == routingModeIndependent {
		if err := m.checkRoutingServiceConfig(ctx); err != nil {
			return fail(http.StatusBadRequest, err)
		}
	}

	m.mu.Lock()
	if m.generation == generation {
		m.runtime.Message = "正在启动网络核心…"
		m.runtime.ModuleInterface = preflight.ModuleInterface
		m.runtime.SystemInterface = preflight.SystemInterface
	}
	m.mu.Unlock()

	if config.Mode == routingModeIndependent {
		m.mu.Lock()
		if m.generation == generation && !m.routingServiceInstalled() {
			m.runtime.Message = "正在安装 TUN 服务…"
		}
		m.mu.Unlock()
		err = m.startPrivilegedCore(ctx, generation)
	} else {
		err = m.startUserCore(generation, config.ClashListenPort)
	}
	if err != nil {
		return fail(http.StatusBadGateway, err)
	}

	now := time.Now()
	m.mu.Lock()
	if m.generation == generation {
		m.runtime.Enabled = true
		m.runtime.State = "running"
		m.runtime.Message = ""
		m.runtime.StartedAt = &now
		if config.Mode == routingModeClash {
			m.runtime.SOCKSAddress = net.JoinHostPort("127.0.0.1", strconv.Itoa(config.ClashListenPort))
		}
	}
	m.mu.Unlock()
	if config.Mode == routingModeIndependent {
		go m.monitorPrivilegedCore(generation)
	}
	return m.Snapshot(), nil
}

func (m *routingManager) failStart(generation uint64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation {
		return
	}
	m.runtime.Enabled = false
	m.runtime.State = "failed"
	m.runtime.Message = err.Error()
	m.runtime.StartedAt = nil
	m.runtime.SOCKSAddress = ""
}

func (m *routingManager) checkCoreConfig(ctx context.Context) error {
	checkContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(checkContext, m.corePath, "check", "--disable-color", "-D", m.dataDir, "-c", m.coreConfigPath)
	command.Env = append(os.Environ(), "HOME="+m.userHome)
	out, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return errors.New("网络核心配置无效：" + detail)
	}
	return nil
}

func (m *routingManager) checkRoutingServiceConfig(ctx context.Context) error {
	checkContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(checkContext, m.helperPath, "-check-config", m.coreConfigPath)
	out, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	return errors.New("TUN 权限服务不接受当前配置：" + detail)
}

func (m *routingManager) startUserCore(generation uint64, port int) error {
	logFile, err := os.OpenFile(m.coreLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("创建网络核心日志失败：%w", err)
	}
	command := exec.Command(m.corePath, "run", "--disable-color", "-D", m.dataDir, "-c", m.coreConfigPath)
	command.Env = append(os.Environ(), "HOME="+m.userHome)
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("启动网络核心失败：%w", err)
	}

	done := make(chan struct{})
	m.mu.Lock()
	if m.generation != generation {
		m.mu.Unlock()
		_ = command.Process.Kill()
		_ = logFile.Close()
		return errors.New("分流启动操作已经失效")
	}
	m.userCommand = command
	m.userCommandDone = done
	m.mu.Unlock()

	go func() {
		waitError := command.Wait()
		_ = logFile.Close()
		m.userCoreExited(generation, waitError)
		close(done)
	}()

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := waitForTCP(address, 5*time.Second); err != nil {
		m.stopUserCore(command, done)
		detail := m.readCoreLogTail()
		if detail != "" {
			return errors.New("SOCKS 服务启动失败：" + detail)
		}
		return fmt.Errorf("SOCKS 服务启动失败：%w", err)
	}
	return nil
}

func (m *routingManager) userCoreExited(generation uint64, waitError error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation {
		return
	}
	m.userCommand = nil
	if m.runtime.State == "stopping" {
		return
	}
	m.runtime.Enabled = false
	m.runtime.State = "failed"
	detail := m.readCoreLogTail()
	if detail == "" && waitError != nil {
		detail = waitError.Error()
	}
	if detail == "" {
		detail = "网络核心意外停止"
	}
	m.runtime.Message = detail
	m.runtime.StartedAt = nil
	m.runtime.SOCKSAddress = ""
}

func (m *routingManager) stopUserCore(command *exec.Cmd, done <-chan struct{}) {
	if command == nil || command.Process == nil {
		return
	}
	_ = command.Process.Signal(syscall.SIGTERM)
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

func (m *routingManager) startPrivilegedCore(ctx context.Context, generation uint64) error {
	if err := m.ensureRoutingService(ctx); err != nil {
		return err
	}
	if _, err := sendRoutingServiceCommand(routingServiceSocketPath, "START "+strconv.Itoa(os.Getpid())); err != nil {
		return fmt.Errorf("启动 TUN 服务失败：%w", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var lastError error
	for time.Now().Before(deadline) {
		m.mu.Lock()
		valid := m.generation == generation
		m.mu.Unlock()
		if !valid {
			return errors.New("分流启动操作已经失效")
		}
		if status, err := m.queryRoutingService(); err == nil && status.State == "running" {
			time.Sleep(500 * time.Millisecond)
			if secondStatus, secondError := m.queryRoutingService(); secondError == nil && secondStatus.State == "running" {
				return nil
			} else {
				if secondError != nil {
					lastError = secondError
				} else {
					lastError = fmt.Errorf("TUN 服务状态为 %s", secondStatus.State)
				}
			}
		} else {
			if err != nil {
				lastError = err
			} else {
				lastError = fmt.Errorf("TUN 服务状态为 %s", status.State)
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	detail := readCoreLogTail(routingServiceLogPath)
	if detail != "" {
		return errors.New("独立分流核心启动失败：" + detail)
	}
	if lastError == nil {
		lastError = errors.New("TUN 服务未报告运行状态")
	}
	return fmt.Errorf("独立分流核心启动超时：%w", lastError)
}

func (m *routingManager) monitorPrivilegedCore(generation uint64) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	failures := 0
	for range ticker.C {
		m.mu.Lock()
		active := m.generation == generation && m.runtime.State == "running" && m.runtime.Mode == routingModeIndependent
		m.mu.Unlock()
		if !active {
			return
		}
		if status, err := m.queryRoutingService(); err == nil && status.State == "running" {
			failures = 0
			continue
		}
		failures++
		if failures < 3 {
			continue
		}
		m.mu.Lock()
		if m.generation == generation && m.runtime.State == "running" {
			m.runtime.Enabled = false
			m.runtime.State = "failed"
			m.runtime.Message = "独立分流核心意外停止"
			if detail := readCoreLogTail(routingServiceLogPath); detail != "" {
				m.runtime.Message = detail
			}
			m.runtime.StartedAt = nil
		}
		m.mu.Unlock()
		return
	}
}

func (m *routingManager) Stop() (routingSnapshot, error) {
	m.mu.Lock()
	if m.runtime.State == "stopped" {
		m.mu.Unlock()
		return m.Snapshot(), nil
	}
	mode := m.runtime.Mode
	command := m.userCommand
	done := m.userCommandDone
	m.runtime.State = "stopping"
	m.runtime.Message = "正在停止网络核心…"
	m.mu.Unlock()

	var stopError error
	if mode == routingModeIndependent {
		stopped := false
		_, stopError = sendRoutingServiceCommand(routingServiceSocketPath, "STOP")
		if stopError != nil {
			if _, statusError := os.Stat(routingServiceSocketPath); os.IsNotExist(statusError) {
				stopError = nil
				stopped = true
			}
		}
		if stopError == nil && !stopped {
			deadline := time.Now().Add(6 * time.Second)
			for time.Now().Before(deadline) {
				status, err := m.queryRoutingService()
				if err == nil && status.State == "stopped" {
					stopped = true
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !stopped {
				stopError = errors.New("TUN 网络核心未能在限定时间内停止")
			}
		}
	} else if command != nil && done != nil {
		m.stopUserCore(command, done)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if stopError != nil {
		m.runtime.State = "failed"
		m.runtime.Enabled = true
		m.runtime.Message = "停止独立分流失败：" + stopError.Error()
		return routingSnapshot{
			Config:       cloneRoutingConfig(m.config),
			Runtime:      m.runtime,
			Capabilities: m.capabilitiesSnapshot(),
		}, stopError
	}
	m.runtime = routingRuntime{State: "stopped"}
	m.userCommand = nil
	m.userCommandDone = nil
	return routingSnapshot{
		Config:       cloneRoutingConfig(m.config),
		Runtime:      m.runtime,
		Capabilities: m.capabilitiesSnapshot(),
	}, nil
}

func (m *routingManager) Uninstall(ctx context.Context) (routingSnapshot, error) {
	m.mu.Lock()
	runningIndependent := m.runtime.Enabled && m.runtime.Mode == routingModeIndependent
	runningUserCore := m.userCommand != nil
	m.mu.Unlock()
	if runningIndependent {
		_, _ = m.Stop()
	}
	if err := m.uninstallRoutingService(ctx); err != nil {
		return m.Snapshot(), err
	}
	if !runningUserCore {
		_ = os.Remove(m.coreConfigPath)
		_ = os.Remove(m.coreLogPath)
	}
	m.mu.Lock()
	if m.runtime.Mode == routingModeIndependent {
		m.runtime = routingRuntime{State: "stopped"}
	}
	m.mu.Unlock()
	return m.Snapshot(), nil
}

func (m *routingManager) Close() {
	_, _ = m.Stop()
}

func (m *routingManager) readCoreLogTail() string {
	return readCoreLogTail(m.coreLogPath)
}

func readCoreLogTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > 32*1024 {
		data = data[len(data)-32*1024:]
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "\n")
}

func sendRoutingServiceCommand(socketPath, command string) (string, error) {
	connection, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintln(connection, command); err != nil {
		return "", err
	}
	response, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		return "", err
	}
	response = strings.TrimSpace(response)
	if !strings.HasPrefix(response, "OK") {
		return response, errors.New(strings.TrimSpace(strings.TrimPrefix(response, "ERROR")))
	}
	return strings.TrimSpace(strings.TrimPrefix(response, "OK")), nil
}

func (a *app) getRouting(w http.ResponseWriter, _ *http.Request) {
	if a.routing == nil {
		writeError(w, http.StatusServiceUnavailable, "应用分流服务尚未初始化")
		return
	}
	writeJSON(w, http.StatusOK, a.routing.Snapshot())
}

func (a *app) updateRoutingConfig(w http.ResponseWriter, r *http.Request) {
	if a.routing == nil {
		writeError(w, http.StatusServiceUnavailable, "应用分流服务尚未初始化")
		return
	}
	var config routingConfig
	if !decodeJSON(w, r, &config) {
		return
	}
	snapshot, err := a.routing.UpdateConfig(config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *app) routingPreflight(w http.ResponseWriter, r *http.Request) {
	if a.routing == nil {
		writeError(w, http.StatusServiceUnavailable, "应用分流服务尚未初始化")
		return
	}
	if r.ContentLength > 0 {
		var config routingConfig
		if !decodeJSON(w, r, &config) {
			return
		}
		writeJSON(w, http.StatusOK, a.routing.PreflightConfig(config, a.routingModuleProduct()))
		return
	}
	writeJSON(w, http.StatusOK, a.routing.Preflight(a.routingModuleProduct()))
}

func (a *app) startRouting(w http.ResponseWriter, r *http.Request) {
	if a.routing == nil {
		writeError(w, http.StatusServiceUnavailable, "应用分流服务尚未初始化")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute+15*time.Second)
	defer cancel()
	snapshot, err := a.routing.Start(ctx, a.routingModuleProduct())
	if err != nil {
		status := http.StatusBadGateway
		var operationError *routingOperationError
		if errors.As(err, &operationError) {
			status = operationError.Status
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *app) stopRouting(w http.ResponseWriter, _ *http.Request) {
	if a.routing == nil {
		writeError(w, http.StatusServiceUnavailable, "应用分流服务尚未初始化")
		return
	}
	snapshot, err := a.routing.Stop()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *app) uninstallRoutingService(w http.ResponseWriter, r *http.Request) {
	if a.routing == nil {
		writeError(w, http.StatusServiceUnavailable, "应用分流服务尚未初始化")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute+15*time.Second)
	defer cancel()
	snapshot, err := a.routing.Uninstall(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *app) routingModuleProduct() string {
	if usbDevice := a.currentUSBDevice(); usbDevice != nil {
		return usbDevice.Product
	}
	return ""
}
