package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultRoutingConfigIsInactiveConfigurationOnly(t *testing.T) {
	config := defaultRoutingConfig()
	if config.Mode != routingModeIndependent {
		t.Fatalf("mode=%q, want independent", config.Mode)
	}
	if len(config.Applications) != 0 {
		t.Fatalf("applications=%d, want none", len(config.Applications))
	}
	if config.ClashListenPort != 17890 {
		t.Fatalf("clash port=%d, want 17890", config.ClashListenPort)
	}
}

func TestCloneRoutingConfigKeepsEmptyApplicationsAsJSONArray(t *testing.T) {
	cloned := cloneRoutingConfig(defaultRoutingConfig())
	data, err := json.Marshal(cloned)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"applications":null`) {
		t.Fatalf("empty applications must be a JSON array: %s", data)
	}
}

func TestNormalizeRoutingConfigRequiresSOCKSForAssignedApplication(t *testing.T) {
	config := defaultRoutingConfig()
	config.SystemSOCKS = routingSOCKSConfig{}
	config.Applications = []routingApplication{{
		ID:         "browser",
		Name:       "Browser",
		BundlePath: "/Applications/Browser.app",
		Action:     routingActionSystemSOCKS,
	}}
	_, err := normalizeRoutingConfig(config)
	if err == nil || !strings.Contains(err.Error(), "SOCKS") {
		t.Fatalf("error=%v, want missing SOCKS validation", err)
	}
}

func TestNormalizeRoutingConfigRejectsExecutableOutsideBundle(t *testing.T) {
	config := defaultRoutingConfig()
	config.Applications = []routingApplication{{
		ID:             "browser",
		Name:           "Browser",
		BundlePath:     "/Applications/Browser.app",
		ExecutablePath: "/usr/bin/curl",
		Action:         routingActionModuleDirect,
	}}
	_, err := normalizeRoutingConfig(config)
	if err == nil || !strings.Contains(err.Error(), "不在应用包内") {
		t.Fatalf("error=%v, want executable containment validation", err)
	}
}

func TestBuildIndependentCoreConfigKeepsThreeExitsSeparate(t *testing.T) {
	config := defaultRoutingConfig()
	config.Applications = []routingApplication{
		{ID: "module", Name: "Module App", BundlePath: "/Applications/Module.app", Action: routingActionModuleDirect},
		{ID: "proxy", Name: "Proxy App", BundlePath: "/Applications/Proxy.app", Action: routingActionSystemSOCKS},
		{ID: "system", Name: "System App", BundlePath: "/Applications/System.app", Action: routingActionSystemDirect},
	}
	data, err := buildIndependentCoreConfig(config, routingInterfaceInfo{Name: "en8", IPv4: "192.168.225.20"})
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	outbounds := indexedObjects(t, document["outbounds"], "tag")
	if outbounds["module-direct"]["bind_interface"] != "en8" {
		t.Fatalf("module direct=%v, want en8 binding", outbounds["module-direct"])
	}
	if _, exists := outbounds["system-direct"]["bind_interface"]; exists {
		t.Fatalf("system direct must use auto-detected system interface: %v", outbounds["system-direct"])
	}
	if outbounds["system-socks"]["bind_interface"] != "lo0" {
		t.Fatalf("local SOCKS=%v, want loopback binding", outbounds["system-socks"])
	}

	route := document["route"].(map[string]any)
	rules := route["rules"].([]any)
	wants := map[string]string{
		"module-direct": `^/Applications/Module\.app/`,
		"system-socks":  `^/Applications/Proxy\.app/`,
		"system-direct": `^/Applications/System\.app/`,
	}
	for _, value := range rules {
		rule := value.(map[string]any)
		outbound := rule["outbound"].(string)
		patterns := rule["process_path_regex"].([]any)
		if len(patterns) != 1 || patterns[0] != wants[outbound] {
			t.Fatalf("rule %s patterns=%v, want %q", outbound, patterns, wants[outbound])
		}
	}
	if route["final"] != "system-direct" {
		t.Fatalf("final=%v, want system-direct", route["final"])
	}
}

func TestBuildClashManagedCoreConfigHasNoTUN(t *testing.T) {
	data, err := buildClashManagedCoreConfig(defaultRoutingConfig(), routingInterfaceInfo{Name: "en8", IPv4: "192.168.225.20"})
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	inbounds := document["inbounds"].([]any)
	if got := inbounds[0].(map[string]any)["type"]; got != "socks" {
		t.Fatalf("inbound=%v, want socks", got)
	}
	outbounds := indexedObjects(t, document["outbounds"], "tag")
	if outbounds["module-direct"]["bind_interface"] != "en8" {
		t.Fatalf("outbound=%v, want en8 binding", outbounds["module-direct"])
	}
	encoded := string(data)
	if strings.Contains(encoded, `"type": "tun"`) || strings.Contains(encoded, "auto_route") {
		t.Fatalf("Clash managed config must not create a TUN:\n%s", encoded)
	}
}

func TestGeneratedRoutingConfigsPassPinnedCoreCheck(t *testing.T) {
	corePath := os.Getenv("DJONEHUB_TEST_SING_BOX")
	if corePath == "" {
		t.Skip("set DJONEHUB_TEST_SING_BOX to run the sing-box integration check")
	}

	independent := defaultRoutingConfig()
	independent.Applications = []routingApplication{
		{ID: "module", Name: "Module App", BundlePath: "/Applications/Module.app", Action: routingActionModuleDirect},
		{ID: "proxy", Name: "Proxy App", BundlePath: "/Applications/Proxy.app", Action: routingActionSystemSOCKS},
		{ID: "system", Name: "System App", BundlePath: "/Applications/System.app", Action: routingActionSystemDirect},
	}
	clash := defaultRoutingConfig()
	clash.Mode = routingModeClash

	module := routingInterfaceInfo{Name: "en8", IPv4: "192.168.225.20"}
	testCases := []struct {
		name  string
		build func() ([]byte, error)
	}{
		{name: "independent", build: func() ([]byte, error) { return buildIndependentCoreConfig(independent, module) }},
		{name: "clash", build: func() ([]byte, error) { return buildClashManagedCoreConfig(clash, module) }},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			data, err := testCase.build()
			if err != nil {
				t.Fatalf("build config: %v", err)
			}
			directory := t.TempDir()
			configPath := filepath.Join(directory, "config.json")
			if err := os.WriteFile(configPath, data, 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			output, err := exec.Command(
				corePath, "check", "--disable-color", "-D", directory, "-c", configPath,
			).CombinedOutput()
			if err != nil {
				t.Fatalf("sing-box check: %v\n%s", err, output)
			}
		})
	}
}

func TestParseRoutingConflictsDetectsFullCaptureButNotSplitVPN(t *testing.T) {
	routes := `
Destination        Gateway            Flags               Netif Expire
default            192.168.1.1        UGScg                 en7
1                  198.18.0.1         UGSc                utun4
2/7                198.18.0.1         UGSc                utun4
10/8               10.0.0.1           UGSc                utun7
128.0/1            198.18.0.1         UGSc                utun4
`
	conflicts := parseRoutingConflicts(routes)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts=%v, want only full-capture utun4", conflicts)
	}
	if conflicts[0].Interface != "utun4" {
		t.Fatalf("interface=%q, want utun4", conflicts[0].Interface)
	}
	if strings.Contains(strings.Join(conflicts[0].Destinations, ","), "10/8") {
		t.Fatalf("corporate split route should not be treated as full capture: %v", conflicts[0])
	}
}

func TestRoutingSupervisorControlProtocol(t *testing.T) {
	socketPath := shortTestSocketPath(t, "status")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	stop := make(chan string, 1)
	go serveRoutingSupervisorControl(listener, 4321, stop)

	status, err := sendRoutingSupervisorCommand(socketPath, "STATUS")
	if err != nil || status != "4321" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	if _, err := sendRoutingSupervisorCommand(socketPath, "STOP"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case reason := <-stop:
		if reason != "control" {
			t.Fatalf("stop reason=%q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("control STOP was not delivered")
	}
}

func indexedObjects(t *testing.T, value any, key string) map[string]map[string]any {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("value=%T, want []any", value)
	}
	result := make(map[string]map[string]any, len(items))
	for _, item := range items {
		object := item.(map[string]any)
		result[object[key].(string)] = object
	}
	return result
}

func TestSupervisorProtocolRejectsUnknownCommand(t *testing.T) {
	socketPath := shortTestSocketPath(t, "unknown")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go serveRoutingSupervisorControl(listener, 1, make(chan string, 1))

	connection, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("RELOAD\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	response, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(response, "ERROR") {
		t.Fatalf("response=%q, want ERROR", response)
	}
}

func shortTestSocketPath(t *testing.T, label string) string {
	t.Helper()
	path := filepath.Join("/tmp", fmt.Sprintf("djrt-%d-%s.sock", os.Getpid(), label))
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}
