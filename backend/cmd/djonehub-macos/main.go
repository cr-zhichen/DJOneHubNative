package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/damonto/euicc-go/driver"
	"github.com/iniwex5/vohive/internal/backend"
	"github.com/iniwex5/vohive/internal/config"
	"github.com/iniwex5/vohive/internal/esim"
	"github.com/iniwex5/vohive/internal/modem"
	"github.com/iniwex5/vohive/pkg/smscodec"
)

// Web 前端已移除：原生 macOS 应用不再需要内嵌网页资源

type receivedSMS struct {
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	Code      string    `json:"code,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	// 模块存储位置（"SM"/"ME"）与索引，用于单条删除；非模块来源（如发送记录）为 0/空
	ModuleStorage string `json:"module_storage,omitempty"`
	ModuleIndex   int    `json:"module_index,omitempty"`
	// Archived 表示短信已持久化到本机（接管模式），ModuleStorage 保留原始来源
	Archived bool `json:"archived,omitempty"`
	// Direction 短信方向："in" 接收 / "out" 发送
	Direction string `json:"direction,omitempty"`
}

type profileNote struct {
	Label string `json:"label"`
	Phone string `json:"phone"`
	Tags  string `json:"tags"`
}

type phonebookProbeResult struct {
	StorageSupported bool              `json:"storage_supported"`
	StorageSelected  bool              `json:"storage_selected"`
	ReadSupported    bool              `json:"read_supported"`
	WriteSupported   bool              `json:"write_supported"`
	StorageStatus    string            `json:"storage_status"`
	Responses        map[string]string `json:"responses"`
}

type moduleProfileNote struct {
	Index int    `json:"index"`
	ICCID string `json:"iccid"`
	Label string `json:"label"`
	Phone string `json:"phone"`
	Tags  string `json:"tags"`
}

type modulePhonebookEntry struct {
	Index  int
	Number string
	Text   string
}

type app struct {
	modem             *modem.Manager
	esimMu            sync.RWMutex
	esim              *esim.Manager
	esimSwitchAllowed bool
	usbAT             *usbAT
	port              string
	discoveryError    string
	usbDevice         *usbDeviceStatus
	usbATBackoffUntil time.Time
	usbATBackoffErr   string

	smsMu          sync.RWMutex
	sms            []receivedSMS
	smsSendMu      sync.Mutex
	smsReassembler *smscodec.Reassembler

	smsPollInterval  time.Duration
	smsLastPoll      time.Time
	smsLastPollError string

	// 接管短信保存模式：开启后短信持久化到本机并删除 SIM/模块原始短信
	smsAdopt   bool
	smsArchive *smsArchive
	dataDir    string

	// 语音功能（USB 音频）开关
	voiceEnabled bool

	profileNotesMu     sync.Mutex
	profileNotes       map[string]profileNote
	profileNotesLoaded bool
	profileNotesPath   string

	moduleNotesMu sync.Mutex

	trafficMu        sync.Mutex
	trafficBaselines map[string]networkByteCounters
}

type usbInterfaceStatus struct {
	Number    int `json:"number"`
	Class     int `json:"class"`
	Subclass  int `json:"subclass"`
	Protocol  int `json:"protocol"`
	Endpoints int `json:"endpoints"`
}

type usbDeviceStatus struct {
	Product    string               `json:"product"`
	Vendor     string               `json:"vendor"`
	VendorID   string               `json:"vendor_id"`
	ProductID  string               `json:"product_id"`
	LocationID string               `json:"location_id"`
	Speed      string               `json:"speed"`
	Mode       string               `json:"mode"`
	Interfaces []usbInterfaceStatus `json:"interfaces"`
}

type networkDiagnostic struct {
	USBNetMode        string            `json:"usbnet_mode"`
	USBCfg            string            `json:"usbcfg"`
	PDPContexts       []pdpContext      `json:"pdp_contexts"`
	ActiveContexts    []int             `json:"active_contexts"`
	PDPAddresses      []string          `json:"pdp_addresses"`
	MacInterfaces     []macNetInterface `json:"mac_interfaces"`
	DefaultRoute      macDefaultRoute   `json:"default_route"`
	USBNetworkPresent bool              `json:"usb_network_present"`
	USBDevice         *usbDeviceStatus  `json:"usb_device,omitempty"`
	Raw               map[string]string `json:"raw,omitempty"`
	Errors            map[string]string `json:"errors,omitempty"`
}

type pdpContext struct {
	ID  int    `json:"id"`
	PDN string `json:"pdn"`
	APN string `json:"apn"`
}

type macNetInterface struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	IPv4   string `json:"ipv4"`
	Kind   string `json:"kind"`
}

type macDefaultRoute struct {
	Interface string `json:"interface"`
	Gateway   string `json:"gateway"`
}

type networkByteCounters struct {
	RX uint64
	TX uint64
}

type networkTrafficSnapshot struct {
	Available    bool   `json:"available"`
	Interface    string `json:"interface,omitempty"`
	RXBytes      uint64 `json:"rx_bytes"`
	TXBytes      uint64 `json:"tx_bytes"`
	SessionRX    uint64 `json:"session_rx_bytes"`
	SessionTX    uint64 `json:"session_tx_bytes"`
	SessionTotal uint64 `json:"session_total_bytes"`
	SampledAtMS  int64  `json:"sampled_at_ms"`
	Error        string `json:"error,omitempty"`
}

type networkCheckResult struct {
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

func main() {
	var port string
	var listen string
	flag.StringVar(&port, "port", "", "AT serial port; auto-detected when omitted")
	flag.StringVar(&listen, "listen", "unix:/tmp/djonehub.sock", "HTTP listen address (unix:/path or host:port)")
	flag.Parse()

	if strings.TrimSpace(port) == "" {
		var err error
		port, err = discoverATPort()
		if err != nil {
			usbDevice := discoverDJIUSBDevice()
			usbATDevice, usbATErr := openDJIUSBAT()
			instance := &app{
				port:             "未发现 AT 串口",
				discoveryError:   err.Error(),
				usbDevice:        usbDevice,
				usbAT:            usbATDevice,
				smsPollInterval:  8 * time.Second,
				smsReassembler:   smscodec.NewReassembler(),
			}
			if usbDevice != nil {
				log.Printf("DJI USB device detected without AT serial port: %s %s (%s:%s)",
					usbDevice.Vendor, usbDevice.Product, usbDevice.VendorID, usbDevice.ProductID)
			}
			if usbATErr != nil {
				log.Printf("USB AT unavailable: %v", usbATErr)
			} else {
				instance.port = usbATDevice.Description()
				instance.discoveryError = ""
				defer usbATDevice.Close()
				log.Printf("USB AT bridge opened on DJI %s", usbATDevice.Description())
				instance.initUSBATESIMManager()
			}
			log.Printf("modem discovery skipped: %v", err)
			go instance.startSMSPoller(context.Background())
			instance.initSMSArchive()
			serve(instance, listen)
			return
		}
	}

	cfg := config.DeviceConfig{
		ID:            "mac-modem",
		Name:          "DJI 4G Module",
		ATPort:        port,
		ManagePort:    port,
		DeviceBackend: backend.BackendAT,
		ESIMTransport: "at",
		BaudRate:      115200,
		DataBits:      8,
		StopBits:      1,
		Parity:        "none",
		SMSEnabled:    true,
	}
	manager, err := modem.New(cfg)
	if err != nil {
		log.Fatalf("create modem manager: %v", err)
	}

	instance := &app{modem: manager, port: port, smsPollInterval: 8 * time.Second}
	manager.SetSMSCallback(instance.recordSMS)
	if err := manager.Start(); err != nil {
		log.Fatalf("open modem on %s: %v", port, err)
	}
	defer manager.Stop()

	if !manager.WaitReady(15 * time.Second) {
		log.Printf("modem initialization is still running; the web UI will remain available")
	}

	atBackend := backend.NewATBackend(manager)
	esimManager, err := esim.NewManager(esim.ManagerOptions{
		DeviceID:  "mac-modem",
		Transport: "at",
		Modem:     manager,
		Backend:   atBackend,
	})
	if err != nil {
		log.Printf("eSIM manager unavailable: %v", err)
	} else {
		instance.installESIMManager(esimManager, false)
	}

	go manager.CheckAllSMS()

	instance.initSMSArchive()
	serve(instance, listen)
}

func (a *app) initUSBATESIMManager() {
	if manager, _ := a.currentESIMManager(); manager != nil {
		return
	}
	esimManager, err := esim.NewManager(esim.ManagerOptions{
		DeviceID:  "mac-usbat",
		Transport: "custom",
		SmartCardChannelFactory: func() (driver.SmartCardChannel, error) {
			return newUSBATESIMChannel(a.runATCommand), nil
		},
	})
	if err != nil {
		log.Printf("eSIM manager unavailable over USB AT: %v", err)
		return
	}
	if a.installESIMManager(esimManager, true) {
		log.Printf("eSIM manager is available over USB AT with profile switching enabled")
	}
}

func (a *app) currentESIMManager() (*esim.Manager, bool) {
	a.esimMu.RLock()
	defer a.esimMu.RUnlock()
	return a.esim, a.esimSwitchAllowed
}

func (a *app) installESIMManager(manager *esim.Manager, switchAllowed bool) bool {
	if manager == nil {
		return false
	}
	a.esimMu.Lock()
	defer a.esimMu.Unlock()
	if a.esim != nil {
		return false
	}
	a.esim = manager
	a.esimSwitchAllowed = switchAllowed
	return true
}

func serve(instance *app, listen string) {
	server := &http.Server{
		Handler:           instance.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := listenWith(listen, server)
	if err != nil {
		log.Fatalf("HTTP server listen: %v", err)
	}

	log.Printf("DJOneHub is using %s", instance.port)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(ln)
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped unexpectedly: %v", err)
		}
	case <-ctx.Done():
		log.Printf("DJOneHub is stopping")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown: %v", err)
		}
	}
}

// listenWith 返回 HTTP 服务监听器。listen 支持两种形式：
//   - "unix:/absolute/path.sock"：Unix domain socket（原生 macOS 应用默认方式）
//   - "host:port"：TCP（兼容旧的命令行用法）
func listenWith(listen string, server *http.Server) (net.Listener, error) {
	if strings.HasPrefix(listen, "unix:") {
		sockPath := strings.TrimPrefix(listen, "unix:")
		if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			return nil, err
		}
		log.Printf("Listening on unix socket %s", sockPath)
		return ln, nil
	}

	server.Addr = listen
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	log.Printf("Listening on http://%s", listen)
	return ln, nil
}

func discoverATPort() (string, error) {
	var ports []string
	for _, pattern := range []string{
		"/dev/cu.usbmodem*",
		"/dev/cu.usbserial*",
		"/dev/cu.wchusbserial*",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		ports = append(ports, matches...)
	}

	sort.SliceStable(ports, func(i, j int) bool {
		return portScore(ports[i]) > portScore(ports[j])
	})
	var attempted []string
	for _, port := range ports {
		attempted = append(attempted, port)
		if _, err := modem.ProbeIMEICached(port, 2*time.Second); err == nil {
			return port, nil
		}
	}
	if len(attempted) == 0 {
		return "", errors.New("no Quectel/DJI USB serial ports found; pass -port /dev/cu.* explicitly")
	}
	return "", fmt.Errorf("no AT-capable port found among %s", strings.Join(attempted, ", "))
}

func portScore(port string) int {
	name := strings.ToLower(port)
	if strings.Contains(name, "quectel") || strings.Contains(name, "dji") {
		return 100
	}
	if strings.Contains(name, "usbmodem") {
		return 80
	}
	if strings.Contains(name, "usbserial") {
		return 60
	}
	return 0
}

func discoverDJIUSBDevice() *usbDeviceStatus {
	out, err := exec.Command("ioreg", "-r", "-c", "IOUSBHostInterface", "-l", "-w", "0").Output()
	if err != nil {
		return nil
	}

	var device *usbDeviceStatus
	for _, block := range strings.Split(string(out), "\n\n") {
		vendorID, okVendor := intProperty(block, "idVendor")
		productID, okProduct := intProperty(block, "idProduct")
		if !okVendor || !okProduct || vendorID != 0x2ca3 {
			continue
		}
		if device == nil {
			device = &usbDeviceStatus{
				Product:    stringProperty(block, "USB Product Name"),
				Vendor:     stringProperty(block, "USB Vendor Name"),
				VendorID:   fmt.Sprintf("%04x", vendorID),
				ProductID:  fmt.Sprintf("%04x", productID),
				LocationID: formatHexProperty(block, "locationID"),
				Speed:      usbSpeedName(intPropertyOrZero(block, "USBSpeed")),
				Mode:       "vendor-specific USB mode",
			}
			if strings.TrimSpace(device.Product) == "" {
				device.Product = "DJI 4G Module"
			}
			if strings.TrimSpace(device.Vendor) == "" {
				device.Vendor = "DJI"
			}
		}
		ifaceNumber, okIface := intProperty(block, "bInterfaceNumber")
		if !okIface {
			continue
		}
		iface := usbInterfaceStatus{
			Number:    ifaceNumber,
			Class:     intPropertyOrZero(block, "bInterfaceClass"),
			Subclass:  intPropertyOrZero(block, "bInterfaceSubClass"),
			Protocol:  intPropertyOrZero(block, "bInterfaceProtocol"),
			Endpoints: intPropertyOrZero(block, "bNumEndpoints"),
		}
		device.Interfaces = append(device.Interfaces, iface)
	}
	if device == nil {
		return nil
	}
	sort.SliceStable(device.Interfaces, func(i, j int) bool {
		return device.Interfaces[i].Number < device.Interfaces[j].Number
	})
	if allVendorSpecific(device.Interfaces) {
		device.Mode = "vendor-specific QMI/diagnostic mode"
	}
	return device
}

func allVendorSpecific(interfaces []usbInterfaceStatus) bool {
	if len(interfaces) == 0 {
		return false
	}
	for _, iface := range interfaces {
		if iface.Class != 255 {
			return false
		}
	}
	return true
}

func intPropertyOrZero(block, name string) int {
	value, _ := intProperty(block, name)
	return value
}

func intProperty(block, name string) (int, bool) {
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(name) + `"\s*=\s*(\d+)`)
	match := pattern.FindStringSubmatch(block)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.Atoi(match[1])
	return value, err == nil
}

func stringProperty(block, name string) string {
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(name) + `"\s*=\s*"([^"]*)"`)
	match := pattern.FindStringSubmatch(block)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func formatHexProperty(block, name string) string {
	value, ok := intProperty(block, name)
	if !ok {
		return ""
	}
	return fmt.Sprintf("0x%x", value)
}

func usbSpeedName(speed int) string {
	switch speed {
	case 1:
		return "low-speed"
	case 2:
		return "full-speed"
	case 3:
		return "high-speed"
	case 4:
		return "super-speed"
	default:
		if speed == 0 {
			return ""
		}
		return fmt.Sprintf("speed-%d", speed)
	}
}

func (a *app) recordSMS(sender, content string, timestamp time.Time) {
	a.mergeSMS([]receivedSMS{{
		Sender: sender, Content: content, Timestamp: timestamp, Direction: "in",
	}})
}

func (a *app) mergeSMS(messages []receivedSMS) (newCount int, total int) {
	a.smsMu.Lock()
	defer a.smsMu.Unlock()
	seen := make(map[string]bool, len(a.sms)+len(messages))
	for _, item := range a.sms {
		seen[smsCacheKey(item)] = true
	}
	for _, item := range messages {
		if item.Code == "" {
			item.Code = extractSMSCode(item.Content)
		}
		key := smsCacheKey(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		a.sms = append(a.sms, item)
		newCount++
	}
	sort.SliceStable(a.sms, func(i, j int) bool {
		return a.sms[i].Timestamp.After(a.sms[j].Timestamp)
	})
	if len(a.sms) > 500 {
		a.sms = a.sms[:500]
	}
	return newCount, len(a.sms)
}

func smsCacheKey(item receivedSMS) string {
	return item.Sender + "\x00" + item.Content + "\x00" + item.Timestamp.Format(time.RFC3339Nano)
}

func (a *app) setSMSPollStatus(err error) {
	a.smsMu.Lock()
	defer a.smsMu.Unlock()
	a.smsLastPoll = time.Now()
	if err != nil {
		a.smsLastPollError = err.Error()
		return
	}
	a.smsLastPollError = ""
}

func (a *app) startSMSPoller(ctx context.Context) {
	interval := a.smsPollInterval
	if interval <= 0 {
		interval = 8 * time.Second
	}
	timer := time.NewTimer(1200 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := a.pollSMSOnce(); err != nil {
				log.Printf("SMS poll failed: %v", err)
			}
			timer.Reset(interval)
		}
	}
}

func (a *app) pollSMSOnce() error {
	if a.modem != nil {
		return nil
	}
	if err := a.ensureUSBAT(); err != nil {
		a.setSMSPollStatus(err)
		return err
	}
	messages, err := a.readUSBATSMS()
	if err != nil {
		a.resetUSBATIfGone(err)
		a.setSMSPollStatus(err)
		return err
	}
	newCount, total := a.mergeSMS(messages)
	if a.smsAdopt && len(messages) > 0 {
		// 接管模式：短信持久化到本机后清理 SIM 与模块原始存储
		archived := a.smsArchive.add(messages)
		if archived > 0 {
			log.Printf("SMS archived %d message(s) locally", archived)
		}
		if _, _, err := a.clearUSBATSMSMemory("SM"); err != nil {
			log.Printf("clear SM SMS after adopt failed: %v", err)
		}
		if _, _, err := a.clearUSBATSMSMemory("ME"); err != nil {
			log.Printf("clear ME SMS after adopt failed: %v", err)
		}
		a.smsMu.Lock()
		a.sms = nil
		a.smsMu.Unlock()
	}
	a.setSMSPollStatus(nil)
	if newCount > 0 {
		log.Printf("SMS poll cached %d new message(s), total %d", newCount, total)
	}
	return nil
}

func (a *app) ensureUSBAT() error {
	if a.modem != nil || a.usbAT != nil {
		return nil
	}
	if a.currentUSBDevice() == nil {
		a.port = "未检测到 DJI USB 设备"
		a.discoveryError = "DJI USB device is not connected"
		return errors.New("DJI USB device is not connected")
	}
	if !a.usbATBackoffUntil.IsZero() && time.Now().Before(a.usbATBackoffUntil) {
		if a.usbATBackoffErr != "" {
			return fmt.Errorf("USB AT is cooling down after disconnect: %s", a.usbATBackoffErr)
		}
		return errors.New("USB AT is cooling down after disconnect")
	}
	dev, err := openDJIUSBAT()
	if err != nil {
		return err
	}
	a.usbAT = dev
	a.usbATBackoffUntil = time.Time{}
	a.usbATBackoffErr = ""
	a.port = dev.Description()
	a.discoveryError = ""
	log.Printf("USB AT bridge opened on DJI %s", dev.Description())
	// The first open may fail while USB is re-enumerating. When a later poll
	// succeeds, rebuild the eSIM service that startup could not create.
	a.initUSBATESIMManager()
	return nil
}

func (a *app) resetUSBATIfGone(err error) {
	if err == nil || a.usbAT == nil {
		return
	}
	text := strings.ToUpper(err.Error())
	if !strings.Contains(text, "NO_DEVICE") &&
		!strings.Contains(text, "NOT_FOUND") &&
		!strings.Contains(text, "USB AT COMMAND TIMED OUT") {
		return
	}
	a.markUSBATDetached(err.Error())
}

// markUSBATDetached clears state belonging to a physically removed module.
// A later status/SMS poll will discover and open a newly connected module.
func (a *app) markUSBATDetached(reason string) {
	if a.usbAT != nil {
		log.Printf("USB AT bridge detached; waiting for a new enumeration: %s", reason)
		a.usbAT.Close()
		a.usbAT = nil
	}
	a.usbDevice = nil
	a.port = "未检测到 DJI USB 设备"
	a.discoveryError = "DJI USB device is not connected"
	a.usbATBackoffUntil = time.Now().Add(2 * time.Second)
	a.usbATBackoffErr = reason
	if manager, _ := a.currentESIMManager(); manager != nil {
		manager.NotifyModemReset()
	}
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/sms", a.listSMS)
	mux.HandleFunc("GET /api/sms/status", a.smsStatus)
	mux.HandleFunc("GET /api/sms/storage", a.smsStorageUsage)
	mux.HandleFunc("POST /api/sms/send", a.sendSMS)
	mux.HandleFunc("POST /api/sms/refresh", a.refreshSMS)
	mux.HandleFunc("POST /api/sms/delete", a.deleteSMS)
	mux.HandleFunc("POST /api/sms/adopt", a.setSMSAdopt)
	mux.HandleFunc("GET /api/sms/adopt", a.getSMSAdopt)
	mux.HandleFunc("POST /api/sms/clear", a.clearSMS)
	mux.HandleFunc("POST /api/at", a.executeAT)
	mux.HandleFunc("GET /api/network", a.networkDiagnostic)
	mux.HandleFunc("GET /api/network/traffic", a.networkTraffic)
	mux.HandleFunc("POST /api/network/check-4g", a.check4GRoute)
	mux.HandleFunc("GET /api/network/services", a.listNetworkServices)
	mux.HandleFunc("PUT /api/network/services-order", a.setNetworkServicesOrder)
	mux.HandleFunc("GET /api/call/status", a.callStatus)
	mux.HandleFunc("POST /api/call/dial", a.callDial)
	mux.HandleFunc("POST /api/call/answer", a.callAnswer)
	mux.HandleFunc("POST /api/call/hangup", a.callHangup)
	mux.HandleFunc("GET /api/voice/enabled", a.getVoiceEnabled)
	mux.HandleFunc("POST /api/voice/enable", a.setVoiceEnabled)
	mux.HandleFunc("POST /api/network/usbnet", a.setUSBNetMode)
	mux.HandleFunc("POST /api/network/reboot-module", a.rebootModule)
	mux.HandleFunc("GET /api/esim", a.esimOverview)
	mux.HandleFunc("GET /api/esim/notes", a.listESIMNotes)
	mux.HandleFunc("PUT /api/esim/notes", a.saveESIMNote)
	mux.HandleFunc("GET /api/esim/module-notes", a.listModuleESIMNotes)
	mux.HandleFunc("PUT /api/esim/module-notes", a.saveModuleESIMNote)
	mux.HandleFunc("GET /api/esim/health", a.esimHealth)
	mux.HandleFunc("POST /api/esim/phonebook/probe", a.probeESIMPhonebook)
	mux.HandleFunc("POST /api/esim/switch", a.switchESIM)
	mux.HandleFunc("PATCH /api/esim/profile", a.renameESIMProfile)
	mux.HandleFunc("DELETE /api/esim/profile", a.deleteESIMProfile)
	mux.HandleFunc("POST /api/esim/download", a.downloadESIMProfile)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (a *app) health(w http.ResponseWriter, _ *http.Request) {
	usbDevice := a.currentUSBDevice()
	esimManager, _ := a.currentESIMManager()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "port": a.port, "esim_available": esimManager != nil,
		"usb_device": usbDevice, "discovery_error": a.discoveryError,
	})
}

func (a *app) status(w http.ResponseWriter, _ *http.Request) {
	if a.modem == nil {
		// A libusb handle may survive a physical unplug. Refresh the macOS USB
		// inventory before using it so the UI never reports a stale connection.
		if a.usbAT != nil && a.currentUSBDevice() == nil {
			a.markUSBATDetached("DJI USB device disconnected")
		}
		if err := a.ensureUSBAT(); err != nil {
			log.Printf("USB AT retry failed: %v", err)
		}
		if a.usbAT != nil {
			status, err := a.usbATStatus()
			if err == nil {
				writeJSON(w, http.StatusOK, status)
				return
			}
			a.resetUSBATIfGone(err)
			log.Printf("USB AT status failed: %v", err)
		}
		usbDevice := a.currentUSBDevice()
		summary := "未发现 AT 串口"
		operator := "未连接"
		network := "不可用"
		if usbDevice != nil {
			summary = fmt.Sprintf("%s %s (%s:%s)", usbDevice.Vendor, usbDevice.Product, usbDevice.VendorID, usbDevice.ProductID)
			operator = "已检测到 USB 设备"
			network = usbDevice.Mode
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"operator":        operator,
			"signal_dbm":      nil,
			"network_mode":    network,
			"sim_inserted":    false,
			"hardware_status": summary,
			"discovery_error": a.discoveryError,
			"usb_device":      usbDevice,
		})
		return
	}
	writeJSON(w, http.StatusOK, a.modem.GetFullStatus())
}

func (a *app) currentUSBDevice() *usbDeviceStatus {
	if a.modem != nil {
		return a.usbDevice
	}
	usbDevice := discoverDJIUSBDevice()
	// Never retain the last successful scan: that is stale after an unplug.
	a.usbDevice = usbDevice
	return usbDevice
}

func (a *app) usbATStatus() (modem.DeviceStatus, error) {
	firmwareResp, _ := a.usbAT.Command("ATI", 3*time.Second)
	cpinResp, cpinErr := a.usbAT.Command("AT+CPIN?", 3*time.Second)
	csqResp, _ := a.usbAT.Command("AT+CSQ", 3*time.Second)
	ceregResp, _ := a.usbAT.Command("AT+CEREG?", 3*time.Second)
	cregResp, _ := a.usbAT.Command("AT+CREG?", 3*time.Second)
	_, _ = a.usbAT.Command("AT+COPS=3,2", 3*time.Second)
	copsResp, _ := a.usbAT.Command("AT+COPS?", 3*time.Second)
	qccidResp, _ := a.usbAT.Command("AT+QCCID", 3*time.Second)
	cimiResp, _ := a.usbAT.Command("AT+CIMI", 3*time.Second)
	gsnResp, _ := a.usbAT.Command("AT+CGSN", 3*time.Second)
	qnwinfoResp, _ := a.usbAT.Command("AT+QNWINFO", 3*time.Second)
	usbnetResp, _ := a.usbAT.Command(`AT+QCFG="usbnet"`, 3*time.Second)

	if cpinErr != nil {
		return modem.DeviceStatus{}, cpinErr
	}

	regStatus := firstNonZeroRegistration(ceregResp, cregResp)
	mode, duplex, band, channel := parseUSBATQNWInfo(qnwinfoResp)
	usbnetMode := -1
	if parsedMode, err := strconv.Atoi(parseUSBNetMode(usbnetResp)); err == nil {
		usbnetMode = parsedMode
	}
	status := modem.DeviceStatus{
		IMEI:          parseUSBATIMEI(gsnResp),
		Firmware:      parseUSBATFirmware(firmwareResp),
		ICCID:         parseUSBATPrefixed(qccidResp, "+QCCID:"),
		IMSI:          parseUSBATIMSI(cimiResp),
		Operator:      parseUSBATOperator(copsResp),
		SimInserted:   strings.Contains(strings.ToUpper(cpinResp), "READY"),
		SignalDBM:     parseUSBATCSQDBM(csqResp),
		RegStatus:     regStatus,
		RegStatusText: registrationText(regStatus),
		NetworkMode:   mode,
		NetworkDuplex: duplex,
		RadioBand:     band,
		RadioChannel:  channel,
		USBNetMode:    usbnetMode,
	}
	if status.Operator == "" && strings.Contains(copsResp, "CHN-UNICOM") {
		status.Operator = "CHN-UNICOM"
	}
	return status, nil
}

func parseUSBATFirmware(resp string) string {
	lines := splitATLines(resp)
	var useful []string
	for _, line := range lines {
		up := strings.ToUpper(line)
		if strings.HasPrefix(up, "ATI") || up == "OK" {
			continue
		}
		useful = append(useful, line)
	}
	return strings.Join(useful, " · ")
}

func splitATLines(resp string) []string {
	resp = strings.ReplaceAll(resp, "\r", "\n")
	raw := strings.Split(resp, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseUSBATPrefixed(resp, prefix string) string {
	for _, line := range splitATLines(resp) {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func parseUSBATIMSI(resp string) string {
	for _, line := range splitATLines(resp) {
		up := strings.ToUpper(line)
		if up == "OK" || strings.HasPrefix(up, "AT") {
			continue
		}
		if _, err := strconv.ParseUint(line, 10, 64); err == nil && len(line) >= 5 {
			return line
		}
	}
	return ""
}

// parseUSBATIMEI 提取 AT+CGSN 响应中的 IMEI（15 位数字串，可能带 +CGSN: 前缀或引号）
func parseUSBATIMEI(resp string) string {
	re := regexp.MustCompile(`\d{15}`)
	return re.FindString(resp)
}

func parseUSBATCSQDBM(resp string) int {
	re := regexp.MustCompile(`\+CSQ:\s*(\d+),`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return 0
	}
	rssi, err := strconv.Atoi(match[1])
	if err != nil || rssi == 99 {
		return 0
	}
	return -113 + 2*rssi
}

func parseUSBATOperator(resp string) string {
	re := regexp.MustCompile(`\+COPS:\s*\d+,\d+,"([^"]*)"`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return ""
	}
	return modem.ResolveServingOperatorNameFromPLMN(match[1])
}

func firstNonZeroRegistration(responses ...string) int {
	for _, resp := range responses {
		re := regexp.MustCompile(`\+(?:CE)?REG:\s*\d+,(\d+)`)
		match := re.FindStringSubmatch(resp)
		if len(match) != 2 {
			continue
		}
		status, err := strconv.Atoi(match[1])
		if err == nil && status != 0 {
			return status
		}
	}
	return 0
}

func registrationText(status int) string {
	switch status {
	case 1:
		return "已注册"
	case 5:
		return "漫游注册"
	case 2:
		return "搜索中"
	case 3:
		return "注册被拒绝"
	default:
		return "未注册"
	}
}

func parseUSBATQNWInfo(resp string) (mode, duplex, band string, channel uint32) {
	re := regexp.MustCompile(`\+QNWINFO:\s*"([^"]*)","[^"]*","([^"]*)",(\d+)`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 4 {
		return "", "", "", 0
	}
	mode = match[1]
	radio := match[2]
	if strings.Contains(strings.ToUpper(mode), "FDD") {
		duplex = "FDD"
		mode = strings.TrimSpace(strings.TrimPrefix(mode, "FDD"))
	}
	if strings.Contains(strings.ToUpper(mode), "TDD") {
		duplex = "TDD"
		mode = strings.TrimSpace(strings.TrimPrefix(mode, "TDD"))
	}
	band = strings.TrimPrefix(radio, "LTE ")
	if value, err := strconv.ParseUint(match[3], 10, 32); err == nil {
		channel = uint32(value)
	}
	return mode, duplex, band, channel
}

func (a *app) readUSBATSMS() ([]receivedSMS, error) {
	if _, err := a.usbAT.Command("AT+CMGF=0", 3*time.Second); err != nil {
		return nil, fmt.Errorf("set SMS PDU mode: %w", err)
	}
	memories := []string{"SM", "ME"}
	seen := make(map[string]bool)
	messages := make([]receivedSMS, 0)
	var errs []string
	for _, memory := range memories {
		items, err := a.readUSBATSMSFromMemory(memory)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", memory, err))
			continue
		}
		for _, item := range items {
			key := item.Sender + "\x00" + item.Content + "\x00" + item.Timestamp.Format(time.RFC3339Nano)
			if seen[key] {
				continue
			}
			seen[key] = true
			messages = append(messages, item)
		}
	}
	if len(messages) == 0 && len(errs) == len(memories) {
		return nil, fmt.Errorf("list SMS failed: %s", strings.Join(errs, "; "))
	}
	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].Timestamp.After(messages[j].Timestamp)
	})
	return messages, nil
}

func (a *app) readUSBATSMSFromMemory(memory string) ([]receivedSMS, error) {
	if _, err := a.usbAT.Command(fmt.Sprintf(`AT+CPMS="%s","%s","%s"`, memory, memory, memory), 5*time.Second); err != nil {
		return nil, fmt.Errorf("select storage: %w", err)
	}
	resp, err := a.usbAT.Command("AT+CMGL=4", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("list SMS: %w", err)
	}
	pdus := parseUSBATCMGL(resp)
	messages := make([]receivedSMS, 0, len(pdus))
	for _, item := range pdus {
		msg, concat, err := decodeUSBATPDU(item.header, item.pdu)
		if err != nil {
			messages = append(messages, receivedSMS{
				Sender:    "PDU",
				Content:   fmt.Sprintf("[短信解析失败] %v\n%s", err, item.pdu),
				Timestamp: time.Now(),
			})
			continue
		}
		if concat.IsConcat {
			if a.smsReassembler == nil {
				a.smsReassembler = smscodec.NewReassembler()
			}
			complete, content := a.smsReassembler.Add(msg.Sender, concat, msg.Content)
			if !complete {
				continue
			}
			msg.Content = content
			log.Printf("USB AT long SMS reassembled: sender=%s segments=%d", msg.Sender, concat.Total)
		}
		msg.ModuleStorage = memory
		msg.ModuleIndex = item.index
		msg.Direction = "in"
		messages = append(messages, msg)
	}
	if a.smsReassembler != nil {
		a.smsReassembler.Cleanup(10 * time.Minute)
	}
	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].Timestamp.After(messages[j].Timestamp)
	})
	return messages, nil
}

func (a *app) clearUSBATSMSMemory(memory string) (before, after int, err error) {
	resp, err := a.usbAT.Command(fmt.Sprintf(`AT+CPMS="%s","%s","%s"`, memory, memory, memory), 5*time.Second)
	if err != nil {
		return 0, 0, fmt.Errorf("select storage: %w", err)
	}
	before = parseUSBATCPMSUsed(resp)
	if _, err := a.usbAT.Command("AT+CMGD=1,4", 20*time.Second); err != nil {
		return before, 0, fmt.Errorf("delete messages: %w", err)
	}
	resp, err = a.usbAT.Command(fmt.Sprintf(`AT+CPMS="%s","%s","%s"`, memory, memory, memory), 5*time.Second)
	if err != nil {
		return before, 0, fmt.Errorf("recheck storage: %w", err)
	}
	after = parseUSBATCPMSUsed(resp)
	return before, after, nil
}

func parseUSBATCPMSUsed(resp string) int {
	re := regexp.MustCompile(`\+CPMS:\s*(\d+),`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return 0
	}
	used, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return used
}

type usbATSMSPDU struct {
	index  int
	header string
	pdu    string
}

func parseUSBATCMGL(resp string) []usbATSMSPDU {
	lines := splitATLines(resp)
	var out []usbATSMSPDU
	indexRe := regexp.MustCompile(`\+CMGL:\s*(\d+)`)
	for i := 0; i < len(lines)-1; i++ {
		if !strings.HasPrefix(lines[i], "+CMGL:") {
			continue
		}
		next := strings.TrimSpace(lines[i+1])
		if !smscodec.IsHexString(next) {
			continue
		}
		index := 0
		if m := indexRe.FindStringSubmatch(lines[i]); m != nil {
			index, _ = strconv.Atoi(m[1])
		}
		pdu, _ := smscodec.TrimFullPDUHexByATHeader(next, lines[i])
		out = append(out, usbATSMSPDU{index: index, header: lines[i], pdu: pdu})
		i++
	}
	return out
}

func decodeUSBATPDU(header, pduHex string) (receivedSMS, smscodec.ConcatInfo, error) {
	raw := strings.TrimSpace(pduHex)
	if trimmed, ok := smscodec.TrimFullPDUHexByATHeader(raw, header); ok {
		raw = trimmed
	}
	full, err := hex.DecodeString(raw)
	if err != nil {
		return receivedSMS{}, smscodec.ConcatInfo{}, err
	}
	if len(full) < 2 {
		return receivedSMS{}, smscodec.ConcatInfo{}, errors.New("PDU too short")
	}
	smscLen := int(full[0])
	tpduOffset := 1 + smscLen
	if tpduOffset >= len(full) {
		return receivedSMS{}, smscodec.ConcatInfo{}, errors.New("PDU has invalid SMSC length")
	}
	sender, content, timestamp, concat, err := smscodec.DecodeDeliverTPDU(full[tpduOffset:])
	if err != nil {
		return receivedSMS{}, smscodec.ConcatInfo{}, err
	}
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return receivedSMS{Sender: sender, Content: content, Timestamp: timestamp}, concat, nil
}

func (a *app) listSMS(w http.ResponseWriter, _ *http.Request) {
	items := a.allSMS()
	if items == nil {
		items = []receivedSMS{}
	}
	writeJSON(w, http.StatusOK, items)
}

// GET /api/sms/storage：查询 SIM 卡（SM）与模块（ME）短信存储用量
func (a *app) smsStorageUsage(w http.ResponseWriter, _ *http.Request) {
	type storageUsage struct {
		Used  int `json:"used"`
		Total int `json:"total"`
	}
	usage := make(map[string]storageUsage)
	if a.modem != nil {
		writeJSON(w, http.StatusOK, map[string]any{"usage": usage})
		return
	}
	if err := a.ensureUSBAT(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "AT serial port is unavailable: "+err.Error())
		return
	}
	re := regexp.MustCompile(`\+CPMS:\s*(\d+),(\d+)`)
	for _, memory := range []string{"SM", "ME"} {
		resp, err := a.usbAT.Command(fmt.Sprintf(`AT+CPMS="%s","%s","%s"`, memory, memory, memory), 5*time.Second)
		if err != nil {
			continue
		}
		if m := re.FindStringSubmatch(resp); m != nil {
			used, _ := strconv.Atoi(m[1])
			total, _ := strconv.Atoi(m[2])
			usage[memory] = storageUsage{Used: used, Total: total}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage})
}

// smsArchive 本机短信归档：持久化到 Application Support 目录 JSON 文件
type smsArchive struct {
	mu    sync.Mutex
	path  string
	items []receivedSMS
}

func newSMSArchive(path string) *smsArchive {
	ar := &smsArchive{path: path}
	ar.load()
	return ar
}

func (ar *smsArchive) load() {
	data, err := os.ReadFile(ar.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &ar.items)
}

func (ar *smsArchive) save() {
	data, err := json.Marshal(ar.items)
	if err != nil {
		return
	}
	_ = os.WriteFile(ar.path, data, 0o600)
}

// snapshot 返回归档内容副本
func (ar *smsArchive) snapshot() []receivedSMS {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	return append([]receivedSMS(nil), ar.items...)
}

// add 合并新短信到归档（去重），返回新增数量；短信归本机所有，清除模块位置信息
func (ar *smsArchive) add(items []receivedSMS) int {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	seen := make(map[string]bool, len(ar.items)+len(items))
	for _, it := range ar.items {
		seen[smsCacheKey(it)] = true
	}
	added := 0
	for _, it := range items {
		key := smsCacheKey(it)
		if seen[key] {
			continue
		}
		seen[key] = true
		// 保留原始来源（SM/ME），标记已归档到本机
		it.Archived = true
		ar.items = append(ar.items, it)
		added++
	}
	if added > 0 {
		sort.SliceStable(ar.items, func(i, j int) bool {
			return ar.items[i].Timestamp.After(ar.items[j].Timestamp)
		})
		if len(ar.items) > 1000 {
			ar.items = ar.items[:1000]
		}
		ar.save()
	}
	return added
}

// remove 按发送者+内容+时间窗口匹配删除归档中的短信
func (ar *smsArchive) remove(sender, content string, ts time.Time) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	if ts.IsZero() {
		return
	}
	filtered := ar.items[:0]
	changed := false
	for _, it := range ar.items {
		if it.Sender == sender && it.Content == content && it.Timestamp.Sub(ts) < time.Minute {
			changed = true
			continue
		}
		filtered = append(filtered, it)
	}
	if changed {
		ar.items = filtered
		ar.save()
	}
}

func (ar *smsArchive) clear() {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	if len(ar.items) > 0 {
		ar.items = nil
		ar.save()
	}
}

func (a *app) initSMSArchive() {
	dir := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "DJOneHubNative")
	_ = os.MkdirAll(dir, 0o700)
	a.dataDir = dir
	a.smsArchive = newSMSArchive(filepath.Join(dir, "sms-archive.json"))
	// 恢复接管模式状态
	if data, err := os.ReadFile(filepath.Join(dir, "sms-adopt.json")); err == nil {
		var state struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(data, &state) == nil {
			a.smsAdopt = state.Enabled
		}
	}
	// 恢复语音功能状态
	if data, err := os.ReadFile(filepath.Join(dir, "voice-enabled.json")); err == nil {
		var state struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(data, &state) == nil {
			a.voiceEnabled = state.Enabled
		}
	}
}

func (a *app) persistSMSAdopt() {
	if a.dataDir == "" {
		return
	}
	data, _ := json.Marshal(map[string]bool{"enabled": a.smsAdopt})
	_ = os.WriteFile(filepath.Join(a.dataDir, "sms-adopt.json"), data, 0o600)
}

// allSMS 返回内存缓存与本机归档合并去重后的全部短信（按时间倒序）
func (a *app) allSMS() []receivedSMS {
	a.smsMu.RLock()
	mem := append([]receivedSMS(nil), a.sms...)
	a.smsMu.RUnlock()
	arch := []receivedSMS(nil)
	if a.smsArchive != nil {
		arch = a.smsArchive.snapshot()
	}
	seen := make(map[string]bool, len(mem)+len(arch))
	merged := make([]receivedSMS, 0, len(mem)+len(arch))
	for _, it := range arch {
		key := smsCacheKey(it)
		if seen[key] {
			continue
		}
		seen[key] = true
		it.Archived = true
		merged = append(merged, it)
	}
	for _, it := range mem {
		key := smsCacheKey(it)
		if seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, it)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Timestamp.After(merged[j].Timestamp)
	})
	return merged
}

func (a *app) smsStatus(w http.ResponseWriter, _ *http.Request) {
	count := len(a.allSMS())
	lastPoll := a.smsLastPoll
	lastPollError := a.smsLastPollError
	writeJSON(w, http.StatusOK, map[string]any{
		"count":           count,
		"polling":         a.modem == nil,
		"poll_interval_s": int(a.smsPollInterval.Seconds()),
		"last_poll":       lastPoll,
		"last_poll_error": lastPollError,
	})
}

func (a *app) refreshSMS(w http.ResponseWriter, _ *http.Request) {
	if a.modem == nil {
		if err := a.pollSMSOnce(); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		a.smsMu.RLock()
		count := len(a.sms)
		a.smsMu.RUnlock()
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "count": count})
		return
	}
	go a.modem.CheckAllSMS()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

// POST /api/sms/delete：删除单条短信并同步内存缓存。
// 模块短信按存储+索引删除；发送记录等无模块索引的短信（index<=0）仅从内存缓存删除。
func (a *app) deleteSMS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Storage   string    `json:"storage"`
		Index     int       `json:"index"`
		Sender    string    `json:"sender"`
		Content   string    `json:"content"`
		Timestamp time.Time `json:"timestamp"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Index <= 0 {
		if strings.TrimSpace(body.Sender) == "" && strings.TrimSpace(body.Content) == "" {
			writeError(w, http.StatusBadRequest, "index is required")
			return
		}
		a.smsMu.Lock()
		filtered := a.sms[:0]
		for _, item := range a.sms {
			if item.Sender == body.Sender && item.Content == body.Content &&
				!body.Timestamp.IsZero() && item.Timestamp.Sub(body.Timestamp) < time.Minute {
				continue
			}
			filtered = append(filtered, item)
		}
		a.sms = filtered
		a.smsMu.Unlock()
		if a.smsArchive != nil {
			a.smsArchive.remove(body.Sender, body.Content, body.Timestamp)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
		return
	}
	memory := strings.ToUpper(strings.TrimSpace(body.Storage))
	if memory == "LOCAL" {
		a.smsArchive.remove(body.Sender, body.Content, body.Timestamp)
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
		return
	}
	if a.modem != nil {
		writeError(w, http.StatusServiceUnavailable, "module SMS deletion is only available through USB AT")
		return
	}
	if err := a.ensureUSBAT(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "AT serial port is unavailable: "+err.Error())
		return
	}
	if _, err := a.usbAT.Command(fmt.Sprintf(`AT+CPMS="%s","%s","%s"`, memory, memory, memory), 5*time.Second); err != nil {
		writeError(w, http.StatusBadGateway, "select storage: "+err.Error())
		return
	}
	if _, err := a.usbAT.Command(fmt.Sprintf("AT+CMGD=%d", body.Index), 10*time.Second); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.smsMu.Lock()
	filtered := a.sms[:0]
	for _, item := range a.sms {
		if item.ModuleStorage == memory && item.ModuleIndex == body.Index {
			continue
		}
		filtered = append(filtered, item)
	}
	a.sms = filtered
	a.smsMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// GET /api/sms/adopt：查询接管模式状态
func (a *app) getSMSAdopt(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"enabled": a.smsAdopt})
}

// POST /api/sms/adopt：设置"接管短信保存"模式
func (a *app) setSMSAdopt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.smsAdopt = body.Enabled
	a.persistSMSAdopt()
	log.Printf("SMS adopt mode set to %v", body.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"adopt": a.smsAdopt})
}

// POST /api/sms/clear：按选择清空 SIM 卡（SM）、模块（ME）、本机存储，可多选
func (a *app) clearSMS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SM    bool `json:"sm"`
		ME    bool `json:"me"`
		Local bool `json:"local"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.SM && !body.ME && !body.Local {
		writeError(w, http.StatusBadRequest, "至少选择一项要清空的存储")
		return
	}
	if a.modem != nil {
		writeError(w, http.StatusServiceUnavailable, "module SMS cleanup is only available through USB AT")
		return
	}
	if err := a.ensureUSBAT(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "AT serial port is unavailable: "+err.Error())
		return
	}
	details := make(map[string]any)
	if body.SM {
		before, after, err := a.clearUSBATSMSMemory("SM")
		if err != nil {
			writeError(w, http.StatusBadGateway, "清空 SIM 卡失败："+err.Error())
			return
		}
		details["sm"] = map[string]any{"ok": true, "before": before, "after": after}
	}
	if body.ME {
		before, after, err := a.clearUSBATSMSMemory("ME")
		if err != nil {
			writeError(w, http.StatusBadGateway, "清空模块存储失败："+err.Error())
			return
		}
		details["me"] = map[string]any{"ok": true, "before": before, "after": after}
	}
	if body.Local {
		a.smsArchive.clear()
		a.smsMu.Lock()
		a.sms = nil
		a.smsMu.Unlock()
		details["local"] = map[string]any{"ok": true}
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true, "details": details})
}

func (a *app) runATCommand(command string, timeout time.Duration) (string, error) {
	if a.modem == nil {
		if err := a.ensureUSBAT(); err != nil {
			return "", err
		}
		if a.usbAT == nil {
			return "", errors.New("AT serial port is unavailable")
		}
		response, err := a.usbAT.Command(command, timeout)
		if err != nil {
			a.resetUSBATIfGone(err)
		}
		return response, err
	}
	return a.modem.ExecuteAT(command, timeout)
}

func (a *app) sendSMS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone   string `json:"phone"`
		Message string `json:"message"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Phone) == "" || strings.TrimSpace(body.Message) == "" {
		writeError(w, http.StatusBadRequest, "phone and message are required")
		return
	}
	segments, err := a.sendTextSMS(body.Phone, body.Message)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// 记录已发送短信（方向 out）；发送记录总是持久化到本机，保证聊天记录完整
	sent := receivedSMS{
		Sender: body.Phone, Content: body.Message, Timestamp: time.Now(), Direction: "out",
	}
	a.mergeSMS([]receivedSMS{sent})
	a.smsArchive.add([]receivedSMS{sent})
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "segments": segments})
}

func (a *app) sendTextSMS(phone, message string) (int, error) {
	if a.modem == nil {
		return a.sendUSBATSMS(phone, message)
	}
	if err := a.modem.SendSMSWithOptions(phone, message, smsSubmitOptions(message)); err != nil {
		return 0, err
	}
	return 1, nil
}

func (a *app) sendUSBATSMS(phone, message string) (int, error) {
	a.smsSendMu.Lock()
	defer a.smsSendMu.Unlock()

	if err := a.ensureUSBAT(); err != nil {
		return 0, err
	}
	if a.usbAT == nil {
		return 0, errors.New("AT serial port is unavailable")
	}

	modeResponse, err := a.usbAT.Command("AT+CMGF=0", 5*time.Second)
	if err != nil {
		a.resetUSBATIfGone(err)
		return 0, fmt.Errorf("set SMS PDU mode: %w", err)
	}
	if !atProbeSucceeded(modeResponse) {
		return 0, fmt.Errorf("set SMS PDU mode failed: %s", modeResponse)
	}

	tpdus, tpduLengths, err := smscodec.BuildSubmitTPDUsWithOptions(phone, message, smsSubmitOptions(message))
	if err != nil {
		return 0, fmt.Errorf("build SMS PDU: %w", err)
	}
	for i, tpdu := range tpdus {
		pdu := append([]byte{0x00}, tpdu...)
		payload := []byte(strings.ToUpper(hex.EncodeToString(pdu)) + "\x1a")
		response, sendErr := a.usbAT.CommandWithPrompt(
			fmt.Sprintf("AT+CMGS=%d", tpduLengths[i]),
			payload,
			45*time.Second,
		)
		if sendErr != nil {
			a.resetUSBATIfGone(sendErr)
			return i, fmt.Errorf("send SMS segment %d/%d: %w", i+1, len(tpdus), sendErr)
		}
		if atResponseIsError(response) || !strings.Contains(response, "+CMGS:") || !atProbeSucceeded(response) {
			return i, fmt.Errorf("send SMS segment %d/%d failed: %s", i+1, len(tpdus), response)
		}
		if i+1 < len(tpdus) {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return len(tpdus), nil
}

func smsSubmitOptions(message string) smscodec.SubmitOptions {
	for _, r := range message {
		if r > 127 {
			return smscodec.SubmitOptions{Encoding: smscodec.SMSEncodingUCS2}
		}
	}
	return smscodec.SubmitOptions{}
}

func (a *app) executeAT(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command string `json:"command"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(body.Command)), "AT") {
		writeError(w, http.StatusBadRequest, "command must start with AT")
		return
	}
	response, err := a.runATCommand(body.Command, 20*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"response": response})
}

func (a *app) networkDiagnostic(w http.ResponseWriter, _ *http.Request) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("network diagnostic panic: %v", recovered)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("network diagnostic failed: %v", recovered))
		}
	}()
	raw := make(map[string]string)
	errs := make(map[string]string)
	diag := networkDiagnostic{
		USBDevice:     a.currentUSBDevice(),
		MacInterfaces: discoverMacNetworkInterfaces(),
		DefaultRoute:  discoverMacDefaultRoute(),
		Raw:           raw,
		Errors:        errs,
	}
	diag.USBNetworkPresent = hasLikelyUSBNetworkInterface(diag.MacInterfaces)

	commands := map[string]string{
		"usbnet":  `AT+QCFG="usbnet"`,
		"usbcfg":  `AT+QCFG="usbcfg"`,
		"cgdcont": `AT+CGDCONT?`,
		"cgact":   `AT+CGACT?`,
		"cgpaddr": `AT+CGPADDR=1`,
	}
	for key, command := range commands {
		resp, err := a.runATCommand(command, 8*time.Second)
		if err != nil {
			errs[key] = err.Error()
			continue
		}
		raw[key] = resp
	}

	diag.USBNetMode = parseUSBNetMode(raw["usbnet"])
	diag.USBCfg = parseUSBATPrefixed(raw["usbcfg"], "+QCFG:")
	diag.PDPContexts = parsePDPContexts(raw["cgdcont"])
	diag.ActiveContexts = parseActivePDPContexts(raw["cgact"])
	diag.PDPAddresses = parsePDPAddresses(raw["cgpaddr"])
	if len(errs) == 0 {
		diag.Errors = nil
	}
	writeJSON(w, http.StatusOK, diag)
}

func (a *app) networkTraffic(w http.ResponseWriter, _ *http.Request) {
	snapshot := networkTrafficSnapshot{
		SampledAtMS: time.Now().UnixMilli(),
	}

	interfaces := discoverMacNetworkInterfaces()
	name := selectUSBTrafficInterface(interfaces, discoverMacDefaultRoute())
	if name == "" {
		writeJSON(w, http.StatusOK, snapshot)
		return
	}
	counters, err := discoverMacInterfaceCounters()
	if err != nil {
		snapshot.Interface = name
		snapshot.Error = err.Error()
		writeJSON(w, http.StatusOK, snapshot)
		return
	}
	current, ok := counters[name]
	if !ok {
		snapshot.Interface = name
		snapshot.Error = "未读取到网卡计数"
		writeJSON(w, http.StatusOK, snapshot)
		return
	}

	a.trafficMu.Lock()
	if a.trafficBaselines == nil {
		a.trafficBaselines = make(map[string]networkByteCounters)
	}
	baseline, exists := a.trafficBaselines[name]
	if !exists || current.RX < baseline.RX || current.TX < baseline.TX {
		baseline = current
		a.trafficBaselines[name] = baseline
	}
	a.trafficMu.Unlock()

	snapshot.Available = true
	snapshot.Interface = name
	snapshot.RXBytes = current.RX
	snapshot.TXBytes = current.TX
	snapshot.SessionRX, snapshot.SessionTX, snapshot.SessionTotal = sessionTrafficFromCounters(current, baseline)
	writeJSON(w, http.StatusOK, snapshot)
}

func sessionTrafficFromCounters(current, baseline networkByteCounters) (rx, tx, total uint64) {
	rx = current.RX - baseline.RX
	tx = current.TX - baseline.TX
	return rx, tx, rx + tx
}

func (a *app) check4GRoute(w http.ResponseWriter, _ *http.Request) {
	route := discoverMacDefaultRoute()
	interfaces := discoverMacNetworkInterfaces()
	var active *macNetInterface
	for i := range interfaces {
		if interfaces[i].Name == route.Interface {
			active = &interfaces[i]
			break
		}
	}
	if route.Interface == "" {
		writeJSON(w, http.StatusOK, networkCheckResult{
			OK:      false,
			Summary: "未读取到默认出口",
			Detail:  "macOS 没有返回 default route",
		})
		return
	}
	if active != nil && active.Name != "en0" && active.Kind == "ethernet" && active.Status == "active" {
		writeJSON(w, http.StatusOK, networkCheckResult{
			OK:      true,
			Summary: "当前正在走 4G 模块",
			Detail:  fmt.Sprintf("默认出口 %s -> %s，IP %s", route.Interface, route.Gateway, active.IPv4),
		})
		return
	}
	detail := fmt.Sprintf("默认出口 %s -> %s", route.Interface, route.Gateway)
	if active != nil && active.IPv4 != "" {
		detail += "，IP " + active.IPv4
	}
	writeJSON(w, http.StatusOK, networkCheckResult{
		OK:      false,
		Summary: "当前没有优先走 4G 模块",
		Detail:  detail,
	})
}

// MARK: - 语音与通话

type clccCall struct {
	ID     int
	Dir    int
	Stat   int
	Number string
}

// parseCLCC 解析 AT+CLCC 响应
func parseCLCC(resp string) []clccCall {
	var calls []clccCall
	re := regexp.MustCompile(`\+CLCC:\s*(\d+),(\d+),(\d+),(\d+),(\d+)(?:,("[^"]*"|[^,]*))?`)
	for _, line := range splitATLines(resp) {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		call := clccCall{}
		call.ID, _ = strconv.Atoi(m[1])
		call.Dir, _ = strconv.Atoi(m[2])
		call.Stat, _ = strconv.Atoi(m[3])
		if len(m) > 6 {
			call.Number = strings.Trim(m[6], `"`)
		}
		calls = append(calls, call)
	}
	return calls
}

// callStateInfo 通话状态聚合
type callStateInfo struct {
	State    string `json:"state"` // idle | incoming | dialing | alerting | active
	Number   string `json:"number,omitempty"`
	Incoming bool   `json:"incoming"`
	Active   bool   `json:"active"`
}

func (a *app) currentCallState() callStateInfo {
	resp, err := a.runATCommand("AT+CLCC", 3*time.Second)
	if err != nil {
		return callStateInfo{State: "unknown"}
	}
	calls := parseCLCC(resp)
	if len(calls) == 0 {
		return callStateInfo{State: "idle"}
	}
	info := callStateInfo{}
	anyLive := false
	for _, call := range calls {
		if call.Number != "" {
			info.Number = call.Number
		}
		switch call.Stat {
		case 4, 5: // incoming / waiting
			info.State = "incoming"
			info.Incoming = true
			anyLive = true
		case 2:
			info.State = "dialing"
			anyLive = true
		case 3:
			info.State = "alerting"
			anyLive = true
		case 0:
			// 无号码的 active 条目视为固件残留（ATH/CHUP 无法清除），不计入活跃通话
			if call.Number == "" {
				continue
			}
			info.State = "active"
			info.Active = true
			anyLive = true
		}
		// stat 6 = disconnected：不视为活跃通话
	}
	if !anyLive {
		return callStateInfo{State: "idle"}
	}
	return info
}

// GET /api/call/status
func (a *app) callStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.currentCallState())
}

// POST /api/call/dial {"number":"+86138..."}
func (a *app) callDial(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Number string `json:"number"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Number) == "" {
		writeError(w, http.StatusBadRequest, "number is required")
		return
	}
	// 分号结尾表示语音呼叫
	response, err := a.runATCommand("ATD"+body.Number+";", 10*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "response": response})
}

// POST /api/call/answer
func (a *app) callAnswer(w http.ResponseWriter, _ *http.Request) {
	response, err := a.runATCommand("ATA", 10*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "response": response})
}

// POST /api/call/hangup
func (a *app) callHangup(w http.ResponseWriter, _ *http.Request) {
	response, err := a.runATCommand("ATH", 10*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "response": response})
}

// parseUACFromUSBCFG 从 AT+QCFG="usbcfg"? 响应解析 UAC（USB 音频）参数，返回 (值, 是否解析成功)
func parseUACFromUSBCFG(resp string) (bool, bool) {
	lower := strings.ToLower(resp)
	re := regexp.MustCompile(`"usbcfg",(?:[^,]+,){8}([^,\r\n]+)`)
	m := re.FindStringSubmatch(lower)
	if m == nil {
		return false, false
	}
	return strings.TrimSpace(m[1]) == "1", true
}

// GET /api/voice/enabled：直接读取模块 usbcfg 的 UAC 参数，反映真实状态
func (a *app) getVoiceEnabled(w http.ResponseWriter, _ *http.Request) {
	enabled, ok := a.queryUACEnabled()
	if ok {
		// 与模块状态对齐并持久化
		if a.voiceEnabled != enabled {
			a.voiceEnabled = enabled
			a.persistVoiceEnabled()
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": a.voiceEnabled})
}

// queryUACEnabled 查询模块当前 UAC 状态；AT 不可用时回退本地记录
func (a *app) queryUACEnabled() (bool, bool) {
	resp, err := a.runATCommand(`AT+QCFG="usbcfg"?`, 3*time.Second)
	if err != nil {
		return a.voiceEnabled, false
	}
	return parseUACFromUSBCFG(resp)
}

// POST /api/voice/enable {"enabled":true|false}
// 通过 usbcfg 的 UAC 参数切换 USB 音频接口，并重启模块使配置生效
func (a *app) setVoiceEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	uac := 0
	if body.Enabled {
		uac = 1
	}
	cmd := fmt.Sprintf(`AT+QCFG="usbcfg",0x2CA3,0x4006,1,1,1,1,1,0,%d`, uac)
	if _, err := a.runATCommand(cmd, 8*time.Second); err != nil {
		writeError(w, http.StatusBadGateway, "切换 USB 音频失败："+err.Error())
		return
	}
	if _, err := a.runATCommand("AT+CFUN=1,1", 5*time.Second); err != nil {
		writeError(w, http.StatusBadGateway, "模块重启失败："+err.Error())
		return
	}
	a.voiceEnabled = body.Enabled
	a.persistVoiceEnabled()
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "enabled": body.Enabled, "needs_unplug": true})
}

func (a *app) persistVoiceEnabled() {
	if a.dataDir == "" {
		return
	}
	data, _ := json.Marshal(map[string]bool{"enabled": a.voiceEnabled})
	_ = os.WriteFile(filepath.Join(a.dataDir, "voice-enabled.json"), data, 0o600)
}

// MARK: - 网络服务优先级

type networkService struct {
	Name   string `json:"name"`
	Device string `json:"device,omitempty"`
	Port   string `json:"port,omitempty"`
	USB    bool   `json:"usb"`
	Module bool   `json:"module"`
}

// isLikelyUSBNetService 判定服务对应的网卡设备是否为 USB/附加网卡（en 开头且非 en0）
func isLikelyUSBNetService(device string) bool {
	if device == "" || device == "en0" {
		return false
	}
	return strings.HasPrefix(device, "en")
}

// macNetworkServiceOrder 读取 macOS 当前网络服务顺序（networksetup -listnetworkserviceorder）。
// moduleProduct 为当前模块的 USB 产品名（如 "Baiwang"），用于标记模块创建的网卡。
func macNetworkServiceOrder(moduleProduct string) ([]networkService, error) {
	out, err := exec.Command("networksetup", "-listnetworkserviceorder").Output()
	if err != nil {
		return nil, fmt.Errorf("networksetup: %v", err)
	}
	nameRe := regexp.MustCompile(`^\(\d+\)\s*(.+)$`)
	portRe := regexp.MustCompile(`Hardware Port:\s*([^,]+)`)
	deviceRe := regexp.MustCompile(`Device:\s*([^,\)]+)`)
	var services []networkService
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if m := nameRe.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(strings.TrimPrefix(m[1], "*"))
			services = append(services, networkService{Name: name})
			continue
		}
		if len(services) == 0 || !strings.Contains(line, "Hardware Port:") {
			continue
		}
		last := &services[len(services)-1]
		if m := portRe.FindStringSubmatch(line); m != nil {
			last.Port = strings.TrimSpace(m[1])
		}
		if m := deviceRe.FindStringSubmatch(line); m != nil {
			last.Device = strings.TrimSpace(m[1])
			last.USB = isLikelyUSBNetService(last.Device)
		}
		if moduleProduct != "" && last.Port != "" && strings.EqualFold(strings.TrimSpace(last.Port), strings.TrimSpace(moduleProduct)) {
			last.Module = true
		}
	}
	return services, nil
}

// GET /api/network/services：列出网络服务顺序
func (a *app) listNetworkServices(w http.ResponseWriter, _ *http.Request) {
	moduleProduct := ""
	if usbDevice := a.currentUSBDevice(); usbDevice != nil {
		moduleProduct = usbDevice.Product
	}
	services, err := macNetworkServiceOrder(moduleProduct)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": services})
}

// PUT /api/network/services-order：应用新的网络服务顺序（需要管理员授权）
func (a *app) setNetworkServicesOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Services []string `json:"services"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Services) < 2 {
		writeError(w, http.StatusBadRequest, "至少需要两个网络服务")
		return
	}
	moduleProduct := ""
	if usbDevice := a.currentUSBDevice(); usbDevice != nil {
		moduleProduct = usbDevice.Product
	}
	current, err := macNetworkServiceOrder(moduleProduct)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	valid := make(map[string]bool)
	for _, s := range current {
		valid[s.Name] = true
	}
	seen := make(map[string]bool)
	cleaned := make([]string, 0, len(body.Services))
	for _, name := range body.Services {
		name = strings.TrimSpace(name)
		if name == "" || !valid[name] {
			writeError(w, http.StatusBadRequest, "存在无效的服务名称："+name)
			return
		}
		if seen[name] {
			writeError(w, http.StatusBadRequest, "服务名称重复："+name)
			return
		}
		seen[name] = true
		cleaned = append(cleaned, name)
	}

	quoted := make([]string, 0, len(cleaned))
	for _, name := range cleaned {
		quoted = append(quoted, shellQuote(name))
	}
	command := "networksetup -ordernetworkservices " + strings.Join(quoted, " ")
	b64 := base64.StdEncoding.EncodeToString([]byte(command))
	script := fmt.Sprintf("do shell script \"echo %s | base64 -d | sh\" with administrator privileges", b64)

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err != nil {
		writeError(w, http.StatusBadGateway, "执行失败："+err.Error()+" "+strings.TrimSpace(string(out)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "网络服务顺序已更新"})
}

// shellQuote 对 POSIX shell 单引号转义
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (a *app) setUSBNetMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode int `json:"mode"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Mode < 0 || body.Mode > 1 {
		writeError(w, http.StatusBadRequest, "only usbnet mode 0 or 1 is allowed")
		return
	}
	command := fmt.Sprintf(`AT+QCFG="usbnet",%d`, body.Mode)
	response, err := a.runATCommand(command, 8*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":         body.Mode,
		"response":     response,
		"needs_reboot": true,
	})
}

func (a *app) rebootModule(w http.ResponseWriter, _ *http.Request) {
	response, err := a.runATCommand("AT+CFUN=1,1", 3*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"response": response,
	})
}

func parseUSBNetMode(resp string) string {
	re := regexp.MustCompile(`\+QCFG:\s*"usbnet",(\d+)`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func parsePDPContexts(resp string) []pdpContext {
	re := regexp.MustCompile(`\+CGDCONT:\s*(\d+),"([^"]*)","([^"]*)"`)
	var contexts []pdpContext
	for _, match := range re.FindAllStringSubmatch(resp, -1) {
		id, _ := strconv.Atoi(match[1])
		contexts = append(contexts, pdpContext{ID: id, PDN: match[2], APN: match[3]})
	}
	return contexts
}

func parseActivePDPContexts(resp string) []int {
	re := regexp.MustCompile(`\+CGACT:\s*(\d+),1`)
	var contexts []int
	for _, match := range re.FindAllStringSubmatch(resp, -1) {
		id, _ := strconv.Atoi(match[1])
		contexts = append(contexts, id)
	}
	return contexts
}

func parsePDPAddresses(resp string) []string {
	re := regexp.MustCompile(`\+CGPADDR:\s*\d+,"([^"]*)"`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return nil
	}
	var out []string
	for _, part := range strings.Split(match[1], ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func discoverMacNetworkInterfaces() []macNetInterface {
	out, err := exec.Command("ifconfig").Output()
	if err != nil {
		return nil
	}
	var interfaces []macNetInterface
	for _, block := range splitIfconfigBlocks(string(out)) {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		if len(lines) == 0 {
			continue
		}
		fields := strings.Fields(lines[0])
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ":")
		if name == "" || strings.HasPrefix(name, "lo") || strings.HasPrefix(name, "utun") {
			continue
		}
		item := macNetInterface{Name: name, Status: "unknown", Kind: classifyMacInterfaceName(name)}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "status:") {
				item.Status = strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			}
			if strings.HasPrefix(line, "inet ") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					item.IPv4 = fields[1]
				}
			}
		}
		interfaces = append(interfaces, item)
	}
	return interfaces
}

func discoverMacDefaultRoute() macDefaultRoute {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return macDefaultRoute{}
	}
	var route macDefaultRoute
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			route.Gateway = strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
		}
		if strings.HasPrefix(line, "interface:") {
			route.Interface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		}
	}
	return route
}

func splitIfconfigBlocks(out string) []string {
	var blocks []string
	var current []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if line[0] != '\t' && line[0] != ' ' && strings.Contains(line, ":") {
			if len(current) > 0 {
				blocks = append(blocks, strings.Join(current, "\n"))
			}
			current = []string{line}
			continue
		}
		if len(current) > 0 {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		blocks = append(blocks, strings.Join(current, "\n"))
	}
	return blocks
}

func classifyMacInterfaceName(name string) string {
	switch {
	case strings.HasPrefix(name, "en"):
		return "ethernet"
	case strings.HasPrefix(name, "bridge"):
		return "bridge"
	case strings.HasPrefix(name, "awdl") || strings.HasPrefix(name, "llw") || strings.HasPrefix(name, "ap"):
		return "apple-wireless"
	default:
		return "other"
	}
}

func hasLikelyUSBNetworkInterface(interfaces []macNetInterface) bool {
	for _, item := range interfaces {
		if item.Kind == "ethernet" && item.Name != "en0" && item.Status == "active" {
			return true
		}
	}
	return false
}

func selectUSBTrafficInterface(interfaces []macNetInterface, route macDefaultRoute) string {
	for _, item := range interfaces {
		if item.Name == route.Interface && item.Kind == "ethernet" && item.Name != "en0" && item.Status == "active" {
			return item.Name
		}
	}
	for _, item := range interfaces {
		if item.Kind == "ethernet" && item.Name != "en0" && item.Status == "active" {
			return item.Name
		}
	}
	return ""
}

func discoverMacInterfaceCounters() (map[string]networkByteCounters, error) {
	out, err := exec.Command("netstat", "-ibn").Output()
	if err != nil {
		return nil, err
	}
	return parseMacInterfaceCounters(string(out)), nil
}

func parseMacInterfaceCounters(out string) map[string]networkByteCounters {
	counters := make(map[string]networkByteCounters)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || !strings.HasPrefix(fields[2], "<Link#") {
			continue
		}
		name := strings.TrimSuffix(fields[0], "*")
		rx, rxErr := strconv.ParseUint(fields[6], 10, 64)
		tx, txErr := strconv.ParseUint(fields[9], 10, 64)
		if name == "" || rxErr != nil || txErr != nil {
			continue
		}
		counters[name] = networkByteCounters{RX: rx, TX: tx}
	}
	return counters
}

func (a *app) loadProfileNotesLocked() error {
	if a.profileNotesLoaded {
		return nil
	}
	path := a.profileNotesPath
	if path == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("locate profile notes directory: %w", err)
		}
		path = filepath.Join(configDir, "DJOneHub", "profile-notes.json")
		a.profileNotesPath = path
	}
	notes := make(map[string]profileNote)
	readPath := path
	if _, err := os.Stat(readPath); errors.Is(err, os.ErrNotExist) {
		legacyPath := filepath.Join(filepath.Dir(filepath.Dir(path)), "VoHive macOS", "profile-notes.json")
		if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
			readPath = legacyPath
		}
	}
	data, err := os.ReadFile(readPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read profile notes: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &notes); err != nil {
			return fmt.Errorf("parse profile notes: %w", err)
		}
	}
	a.profileNotes = notes
	a.profileNotesLoaded = true
	return nil
}

func (a *app) persistProfileNotesLocked() error {
	if err := os.MkdirAll(filepath.Dir(a.profileNotesPath), 0o700); err != nil {
		return fmt.Errorf("create profile notes directory: %w", err)
	}
	data, err := json.MarshalIndent(a.profileNotes, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profile notes: %w", err)
	}
	temporary := a.profileNotesPath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write profile notes: %w", err)
	}
	if err := os.Rename(temporary, a.profileNotesPath); err != nil {
		return fmt.Errorf("replace profile notes: %w", err)
	}
	return nil
}

func (a *app) listESIMNotes(w http.ResponseWriter, _ *http.Request) {
	a.profileNotesMu.Lock()
	defer a.profileNotesMu.Unlock()
	if err := a.loadProfileNotesLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": a.profileNotes})
}

func (a *app) saveESIMNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ICCID string `json:"iccid"`
		Label string `json:"label"`
		Phone string `json:"phone"`
		Tags  string `json:"tags"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.ICCID = strings.TrimSpace(body.ICCID)
	body.Label = strings.TrimSpace(body.Label)
	body.Phone = strings.TrimSpace(body.Phone)
	body.Tags = strings.TrimSpace(body.Tags)
	if body.ICCID == "" {
		writeError(w, http.StatusBadRequest, "iccid is required")
		return
	}
	if len(body.Label) > 80 || len(body.Phone) > 80 || len(body.Tags) > 200 {
		writeError(w, http.StatusBadRequest, "本地备注字段过长")
		return
	}
	a.profileNotesMu.Lock()
	defer a.profileNotesMu.Unlock()
	if err := a.loadProfileNotesLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Label == "" && body.Phone == "" && body.Tags == "" {
		delete(a.profileNotes, body.ICCID)
	} else {
		a.profileNotes[body.ICCID] = profileNote{Label: body.Label, Phone: body.Phone, Tags: body.Tags}
	}
	if err := a.persistProfileNotesLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "本地备注已保存", "note": a.profileNotes[body.ICCID]})
}

func atCommandSucceeded(response string) bool {
	normalized := strings.ReplaceAll(strings.TrimSpace(response), "\r\n", "\n")
	return normalized == "OK" || strings.HasSuffix(normalized, "\nOK")
}

func (a *app) phonebookProbeCommand(command string, result *phonebookProbeResult) bool {
	response, err := a.runATCommand(command, 6*time.Second)
	if err != nil {
		result.Responses[command] = err.Error()
		return false
	}
	result.Responses[command] = strings.TrimSpace(response)
	return atCommandSucceeded(response)
}

// probeESIMPhonebook performs only AT test/read commands. It never writes a
// phonebook entry, so it is safe to use before enabling portable card notes.
func (a *app) probeESIMPhonebook(w http.ResponseWriter, _ *http.Request) {
	result := phonebookProbeResult{Responses: make(map[string]string)}

	if !a.phonebookProbeCommand(`AT+CPBS=?`, &result) {
		writeJSON(w, http.StatusOK, result)
		return
	}
	result.StorageSupported = strings.Contains(strings.ToUpper(result.Responses[`AT+CPBS=?`]), `"SM"`)
	if !result.StorageSupported {
		writeJSON(w, http.StatusOK, result)
		return
	}
	result.StorageSelected = a.phonebookProbeCommand(`AT+CPBS="SM"`, &result)
	if !result.StorageSelected {
		writeJSON(w, http.StatusOK, result)
		return
	}
	if a.phonebookProbeCommand(`AT+CPBS?`, &result) {
		result.StorageStatus = result.Responses[`AT+CPBS?`]
	}
	result.ReadSupported = a.phonebookProbeCommand(`AT+CPBR=?`, &result)
	result.WriteSupported = a.phonebookProbeCommand(`AT+CPBW=?`, &result)
	writeJSON(w, http.StatusOK, result)
}

const moduleNotePrefix = "VH1|"

func encodeModuleProfileNote(note moduleProfileNote) (string, error) {
	note.ICCID = strings.TrimSpace(note.ICCID)
	note.Label = strings.TrimSpace(note.Label)
	note.Phone = strings.TrimSpace(note.Phone)
	note.Tags = strings.TrimSpace(note.Tags)
	if note.ICCID == "" {
		return "", errors.New("iccid is required")
	}
	if len(note.Label) > 48 || len(note.Phone) > 40 || len(note.Tags) > 48 {
		return "", errors.New("模块资料名称、手机号或标签过长")
	}
	encode := func(value string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(value))
	}
	encoded := strings.Join([]string{moduleNotePrefix[:len(moduleNotePrefix)-1], note.ICCID, encode(note.Label), encode(note.Phone), encode(note.Tags)}, "|")
	if len(encoded) > 255 {
		return "", errors.New("模块通讯录记录超过容量")
	}
	return encoded, nil
}

func decodeModuleProfileNote(index int, text string) (moduleProfileNote, bool) {
	parts := strings.Split(text, "|")
	if len(parts) != 5 || parts[0] != strings.TrimSuffix(moduleNotePrefix, "|") || strings.TrimSpace(parts[1]) == "" {
		return moduleProfileNote{}, false
	}
	decode := func(value string) (string, bool) {
		data, err := base64.RawURLEncoding.DecodeString(value)
		return string(data), err == nil
	}
	label, labelOK := decode(parts[2])
	phone, phoneOK := decode(parts[3])
	tags, tagsOK := decode(parts[4])
	if !labelOK || !phoneOK || !tagsOK {
		return moduleProfileNote{}, false
	}
	return moduleProfileNote{Index: index, ICCID: parts[1], Label: label, Phone: phone, Tags: tags}, true
}

func (a *app) runATOK(command string, timeout time.Duration) (string, error) {
	response, err := a.runATCommand(command, timeout)
	if err != nil {
		return "", err
	}
	if !atCommandSucceeded(response) {
		return "", fmt.Errorf("%s: %s", command, strings.TrimSpace(response))
	}
	return response, nil
}

func parseMEPhonebookStatus(response string) (used, total int, err error) {
	re := regexp.MustCompile(`\+CPBS:\s*"ME",(\d+),(\d+)`)
	match := re.FindStringSubmatch(response)
	if len(match) != 3 {
		return 0, 0, errors.New("ME 通讯录容量未返回")
	}
	used, err = strconv.Atoi(match[1])
	if err != nil {
		return 0, 0, err
	}
	total, err = strconv.Atoi(match[2])
	return used, total, err
}

func parseMEPhonebookEntries(response string) []modulePhonebookEntry {
	re := regexp.MustCompile(`(?m)\+CPBR:\s*(\d+),"([^"]*)",\d+,"([^"]*)"`)
	entries := make([]modulePhonebookEntry, 0)
	for _, match := range re.FindAllStringSubmatch(response, -1) {
		index, err := strconv.Atoi(match[1])
		if err == nil {
			entries = append(entries, modulePhonebookEntry{Index: index, Number: match[2], Text: match[3]})
		}
	}
	return entries
}

func (a *app) readModuleESIMNotes() (map[string]moduleProfileNote, map[int]bool, int, int, error) {
	if _, err := a.runATOK(`AT+CPBS="ME"`, 6*time.Second); err != nil {
		return nil, nil, 0, 0, err
	}
	status, err := a.runATOK(`AT+CPBS?`, 6*time.Second)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	used, total, err := parseMEPhonebookStatus(status)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	notes := make(map[string]moduleProfileNote)
	occupied := make(map[int]bool)
	if used == 0 {
		return notes, occupied, used, total, nil
	}
	response, err := a.runATOK(fmt.Sprintf("AT+CPBR=1,%d", total), 20*time.Second)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	for _, entry := range parseMEPhonebookEntries(response) {
		occupied[entry.Index] = true
		if note, ok := decodeModuleProfileNote(entry.Index, entry.Text); ok {
			notes[note.ICCID] = note
		}
	}
	return notes, occupied, used, total, nil
}

func (a *app) listModuleESIMNotes(w http.ResponseWriter, _ *http.Request) {
	a.moduleNotesMu.Lock()
	defer a.moduleNotesMu.Unlock()
	notes, _, used, total, err := a.readModuleESIMNotes()
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("读取模块资料库失败: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes, "used": used, "total": total})
}

func (a *app) saveModuleESIMNote(w http.ResponseWriter, r *http.Request) {
	var body moduleProfileNote
	if !decodeJSON(w, r, &body) {
		return
	}
	a.moduleNotesMu.Lock()
	defer a.moduleNotesMu.Unlock()
	notes, occupied, _, total, err := a.readModuleESIMNotes()
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("读取模块资料库失败: %v", err))
		return
	}
	current, exists := notes[strings.TrimSpace(body.ICCID)]
	if strings.TrimSpace(body.Label) == "" && strings.TrimSpace(body.Phone) == "" && strings.TrimSpace(body.Tags) == "" {
		if !exists {
			writeJSON(w, http.StatusOK, map[string]string{"message": "模块资料库中没有此记录"})
			return
		}
		if _, err := a.runATOK(fmt.Sprintf("AT+CPBW=%d", current.Index), 8*time.Second); err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("删除模块资料失败: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "模块资料已删除"})
		return
	}
	encoded, err := encodeModuleProfileNote(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	index := current.Index
	if !exists {
		for candidate := 1; candidate <= total; candidate++ {
			if !occupied[candidate] {
				index = candidate
				break
			}
		}
	}
	if index == 0 {
		writeError(w, http.StatusConflict, "模块通讯录已满")
		return
	}
	command := fmt.Sprintf(`AT+CPBW=%d,"00000000000",129,"%s"`, index, encoded)
	if _, err := a.runATOK(command, 8*time.Second); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("写入模块资料失败: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "模块资料已保存", "index": index})
}

func (a *app) esimOverview(w http.ResponseWriter, _ *http.Request) {
	esimManager, _ := a.currentESIMManager()
	if esimManager == nil {
		writeError(w, http.StatusServiceUnavailable, "eSIM manager is unavailable")
		return
	}
	overview, err := esimManager.GetEsimOverview()
	if err != nil {
		if isPhysicalSIMESIMProbeError(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"card_type": "physical_sim",
				"message":   "当前卡片为实体卡，非 eSIM 卡片",
			})
			return
		}
		log.Printf("eSIM overview failed: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

// A normal physical SIM cannot open the GSMA eUICC management AIDs. The
// manager reports that as no eUICC discovered with an AT+CCHO ERROR; expose it
// as a neutral card type instead of leaking an implementation error to the UI.
func isPhysicalSIMESIMProbeError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "未发现任何 euicc") &&
		strings.Contains(message, "at+ccho") &&
		strings.Contains(message, "error")
}

func (a *app) esimHealth(w http.ResponseWriter, _ *http.Request) {
	esimManager, _ := a.currentESIMManager()
	if esimManager == nil {
		writeError(w, http.StatusServiceUnavailable, "eSIM manager is unavailable")
		return
	}
	overview, err := esimManager.GetEsimOverview()
	if err != nil {
		if isPhysicalSIMESIMProbeError(err) {
			writeJSON(w, http.StatusOK, map[string]any{"card_type": "physical_sim"})
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	var active *esim.ProfileItem
	for _, group := range overview.Profiles {
		for index := range group.Profiles {
			if group.Profiles[index].State == 1 {
				active = &group.Profiles[index]
				break
			}
		}
		if active != nil {
			break
		}
	}
	if active == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "eSIM 卡片已识别，但没有已启用的 Profile"})
		return
	}

	if err := a.ensureUSBAT(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	status, err := a.usbATStatus()
	if err != nil {
		a.resetUSBATIfGone(err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	registered := status.RegStatus == 1 || status.RegStatus == 5
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             status.SimInserted && registered,
		"active_profile": active,
		"module_iccid":   status.ICCID,
		"imsi":           status.IMSI,
		"operator":       status.Operator,
		"registration":   status.RegStatusText,
		"registered":     registered,
		"signal_dbm":     status.SignalDBM,
		"network_mode":   status.NetworkMode,
	})
}

func (a *app) switchESIM(w http.ResponseWriter, r *http.Request) {
	esimManager, switchAllowed := a.currentESIMManager()
	if esimManager == nil {
		writeError(w, http.StatusServiceUnavailable, "eSIM manager is unavailable")
		return
	}
	var body struct {
		ICCID string `json:"iccid"`
		AID   string `json:"aid"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.ICCID) == "" {
		writeError(w, http.StatusBadRequest, "iccid is required")
		return
	}
	if !switchAllowed && a.modem == nil {
		writeError(w, http.StatusServiceUnavailable, "USB AT eSIM/卡片当前暂不允许切换 Profile")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	result, err := esimManager.SwitchProfileWithResult(ctx, body.ICCID, body.AID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Enabling a Profile resets the eUICC, but the DJI modem can keep the
	// previous SIM session (and therefore its CNUM) until its own firmware is
	// restarted.  Reload it here so the newly enabled Profile actually becomes
	// the modem's active subscriber identity.
	rebootResponse, rebootErr := a.runATCommand("AT+CFUN=1,1", 3*time.Second)
	rebootRequested := rebootErr == nil
	rebootWarning := ""
	if rebootErr != nil {
		// A USB disconnect immediately after CFUN is expected on some firmware;
		// the command may already have been accepted before the bridge drops.
		upper := strings.ToUpper(rebootErr.Error())
		if strings.Contains(upper, "NO_DEVICE") || strings.Contains(upper, "NOT_FOUND") {
			rebootRequested = true
		} else {
			rebootWarning = rebootErr.Error()
			log.Printf("eSIM profile switched but module restart was not confirmed: %v", rebootErr)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"switch_accepted":         result.SwitchAccepted,
		"phase":                   result.Phase,
		"target_iccid":            result.TargetICCID,
		"recovery_pending":        result.RecoveryPending,
		"module_reboot_requested": rebootRequested,
		"module_reboot_response":  rebootResponse,
		"module_reboot_warning":   rebootWarning,
		"reconnect_wait_seconds":  10,
	})
}

func (a *app) renameESIMProfile(w http.ResponseWriter, r *http.Request) {
	esimManager, _ := a.currentESIMManager()
	if esimManager == nil {
		writeError(w, http.StatusServiceUnavailable, "eSIM manager is unavailable")
		return
	}
	var body struct {
		ICCID string `json:"iccid"`
		AID   string `json:"aid"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.ICCID = strings.TrimSpace(body.ICCID)
	body.Name = strings.TrimSpace(body.Name)
	if body.ICCID == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "iccid and name are required")
		return
	}
	if err := esimManager.RenameProfile(body.ICCID, body.Name, body.AID); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("修改 Profile 名称失败: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Profile 名称修改成功"})
}

func (a *app) deleteESIMProfile(w http.ResponseWriter, r *http.Request) {
	esimManager, _ := a.currentESIMManager()
	if esimManager == nil {
		writeError(w, http.StatusServiceUnavailable, "eSIM manager is unavailable")
		return
	}
	var body struct {
		ICCID string `json:"iccid"`
		AID   string `json:"aid"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.ICCID = strings.TrimSpace(body.ICCID)
	if body.ICCID == "" {
		writeError(w, http.StatusBadRequest, "iccid is required")
		return
	}
	result, err := esimManager.DeleteProfile(body.ICCID, body.AID)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("删除 Profile 失败: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) downloadESIMProfile(w http.ResponseWriter, r *http.Request) {
	esimManager, _ := a.currentESIMManager()
	if esimManager == nil {
		writeError(w, http.StatusServiceUnavailable, "eSIM manager is unavailable")
		return
	}
	var body struct {
		SMDP             string `json:"smdp"`
		MatchingID       string `json:"matching_id"`
		ConfirmationCode string `json:"confirmation_code"`
		AID              string `json:"aid"`
		IMEI             string `json:"imei"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.SMDP = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(body.SMDP, "https://"), "http://"))
	if body.SMDP == "" {
		writeError(w, http.StatusBadRequest, "smdp is required")
		return
	}
	if strings.TrimSpace(body.IMEI) == "" {
		writeError(w, http.StatusBadRequest, "imei is required for USB AT eSIM download")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	result, err := esimManager.DownloadProfile(ctx, body.AID, body.SMDP, body.MatchingID, body.ConfirmationCode, body.IMEI, func(event esim.DownloadProgressEvent) {
		log.Printf("eSIM download %d%% %s", event.Pct, event.Msg)
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("下载 Profile 失败: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
