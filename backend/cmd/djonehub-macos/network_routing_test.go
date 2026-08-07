package main

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func testRoutingModuleInterface() routingInterfaceInfo {
	return routingInterfaceInfo{
		Name:        "en8",
		IPv4:        "192.168.225.20",
		IPv4Gateway: "192.168.225.1",
		IPv6:        "240e:471:c30:6ad4::20",
	}
}

func TestRoutingServicePlistUsesFixedPrivilegedExecutables(t *testing.T) {
	manager := &routingManager{
		coreConfigPath: "/Users/test/Library/Application Support/DJOneHubNative/network&core.json",
		coreLogPath:    "/Users/test/Library/Application Support/DJOneHubNative/network-core.log",
		userHome:       "/Users/test",
	}
	plist, err := manager.routingServicePlist()
	if err != nil {
		t.Fatalf("build plist: %v", err)
	}
	decoder := xml.NewDecoder(strings.NewReader(string(plist)))
	for {
		if _, err := decoder.Token(); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("plist is not valid XML: %v", err)
		}
	}
	encoded := string(plist)
	for _, expected := range []string{
		routingServiceHelperPath,
		routingServiceCorePath,
		routingServiceSocketPath,
		routingServiceLogPath,
		"<key>KeepAlive</key>",
	} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("plist missing %q", expected)
		}
	}
	if strings.Contains(encoded, "network&core.json") || !strings.Contains(encoded, "network&amp;core.json") {
		t.Fatalf("plist path was not XML escaped: %s", encoded)
	}
}

func TestDefaultRoutingConfigIsInactiveConfigurationOnly(t *testing.T) {
	config := defaultRoutingConfig()
	if config.Mode != routingModeIndependent {
		t.Fatalf("mode=%q, want independent", config.Mode)
	}
	if config.Version != routingConfigVersion || config.DefaultAction != routingActionSystemDirect {
		t.Fatalf("version=%d default=%q, want v%d system direct", config.Version, config.DefaultAction, routingConfigVersion)
	}
	if len(config.Applications) != 0 {
		t.Fatalf("applications=%d, want none", len(config.Applications))
	}
	if config.ClashListenPort != 17890 {
		t.Fatalf("clash port=%d, want 17890", config.ClashListenPort)
	}
}

func TestNormalizeRoutingConfigMigratesVersionOneDefaultExit(t *testing.T) {
	config := defaultRoutingConfig()
	config.Version = 1
	config.DefaultAction = ""
	config.Applications = []routingApplication{{
		ID: "browser", Name: "Browser", BundlePath: "/Applications/Browser.app", Action: routingActionModuleDirect,
	}}
	normalized, err := normalizeRoutingConfig(config)
	if err != nil {
		t.Fatalf("normalize legacy config: %v", err)
	}
	if normalized.Version != routingConfigVersion || normalized.DefaultAction != routingActionSystemDirect {
		t.Fatalf("migrated config=%+v", normalized)
	}
	if len(normalized.Applications) != 1 || normalized.Applications[0].Action != routingActionModuleDirect {
		t.Fatalf("legacy application rules were not preserved: %+v", normalized.Applications)
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

func TestNormalizeRoutingConfigAllowsSOCKSAsDefaultExit(t *testing.T) {
	config := defaultRoutingConfig()
	config.DefaultAction = routingActionSystemSOCKS
	normalized, err := normalizeRoutingConfig(config)
	if err != nil {
		t.Fatalf("normalize SOCKS default exit: %v", err)
	}
	if normalized.DefaultAction != routingActionSystemSOCKS {
		t.Fatalf("default=%q, want system SOCKS", normalized.DefaultAction)
	}
}

func TestNormalizeRoutingConfigRequiresSOCKSForDefaultExit(t *testing.T) {
	config := defaultRoutingConfig()
	config.DefaultAction = routingActionSystemSOCKS
	config.SystemSOCKS = routingSOCKSConfig{}
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

func TestParseRoutingGlobalIPv6AddressPrefersStableGlobalAddress(t *testing.T) {
	ifconfigOutput := `en8: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	inet6 fe80::1cc1:50d0:49c6:7ac9%en8 prefixlen 64 secured scopeid 0x40
	inet6 240e:471:c30:6ad4:c1c:5610:2327:b051 prefixlen 64 autoconf secured
	inet6 240e:471:c30:6ad4:dd4:fdf3:bee2:eea2 prefixlen 64 autoconf temporary
	inet 192.168.225.27 netmask 0xffffff00 broadcast 192.168.225.255
	status: active`
	got := parseRoutingGlobalIPv6Address(ifconfigOutput)
	if got != "240e:471:c30:6ad4:c1c:5610:2327:b051" {
		t.Fatalf("IPv6=%q, want stable global address", got)
	}
}

func TestParseRoutingGlobalIPv6AddressFallsBackToTemporary(t *testing.T) {
	ifconfigOutput := `
	inet6 fe80::1%en8 prefixlen 64 scopeid 0x40
	inet6 fd00::20 prefixlen 64 autoconf secured
	inet6 240e:471:c30:6ad4::21 prefixlen 64 autoconf temporary`
	got := parseRoutingGlobalIPv6Address(ifconfigOutput)
	if got != "240e:471:c30:6ad4::21" {
		t.Fatalf("IPv6=%q, want temporary global address", got)
	}
}

func TestParseRoutingScopedDefaultRouteRequiresMatchingInterfaceAndGateway(t *testing.T) {
	valid := `
   route to: ::
destination: ::
    gateway: fe80::1%en8
  interface: en8`
	if !parseRoutingScopedDefaultRoute(valid, "en8") {
		t.Fatal("matching scoped IPv6 default route must be accepted")
	}
	if parseRoutingScopedDefaultRoute(valid, "en7") {
		t.Fatal("route for another interface must be rejected")
	}
	if got := parseRoutingScopedDefaultGateway(valid, "en8"); got != "fe80::1%en8" {
		t.Fatalf("gateway=%q, want scoped IPv6 gateway", got)
	}
}

func TestBuildIndependentCoreConfigKeepsThreeExitsSeparate(t *testing.T) {
	config := defaultRoutingConfig()
	config.Applications = []routingApplication{
		{ID: "module", Name: "Module App", BundlePath: "/Applications/Module.app", Action: routingActionModuleDirect},
		{ID: "proxy", Name: "Proxy App", BundlePath: "/Applications/Proxy.app", Action: routingActionSystemSOCKS},
		{ID: "system", Name: "System App", BundlePath: "/Applications/System.app", Action: routingActionSystemDirect},
	}
	data, err := buildIndependentCoreConfig(
		config,
		testRoutingModuleInterface(),
		"en7",
		nil,
	)
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
	if outbounds["module-direct"]["inet4_bind_address"] != "192.168.225.20" {
		t.Fatalf("module direct=%v, want module IPv4 binding", outbounds["module-direct"])
	}
	if outbounds["module-direct"]["inet6_bind_address"] != "240e:471:c30:6ad4::20" {
		t.Fatalf("module direct=%v, want module IPv6 binding", outbounds["module-direct"])
	}
	if outbounds["system-direct"]["bind_interface"] != "en7" {
		t.Fatalf("system direct=%v, want en7 binding", outbounds["system-direct"])
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
	foundDNSHijack := false
	for _, value := range rules {
		rule := value.(map[string]any)
		if rule["action"] == "hijack-dns" {
			foundDNSHijack = true
			if rule["port"] != float64(53) {
				t.Fatalf("DNS hijack rule=%v, want port 53", rule)
			}
			patterns := rule["process_path_regex"].([]any)
			if len(patterns) != 1 || patterns[0] != wants["module-direct"] {
				t.Fatalf("DNS hijack patterns=%v, want module application", patterns)
			}
			continue
		}
		outbound := rule["outbound"].(string)
		patterns := rule["process_path_regex"].([]any)
		if len(patterns) != 1 || patterns[0] != wants[outbound] {
			t.Fatalf("rule %s patterns=%v, want %q", outbound, patterns, wants[outbound])
		}
	}
	if !foundDNSHijack {
		t.Fatal("module application DNS must be hijacked")
	}
	dns := document["dns"].(map[string]any)
	dnsServers := indexedObjects(t, dns["servers"], "tag")
	moduleDNS := dnsServers["module-dns"]
	if moduleDNS["type"] != "udp" || moduleDNS["server"] != "192.168.225.1" || moduleDNS["detour"] != "module-direct" {
		t.Fatalf("module DNS=%v, want module gateway through module direct", moduleDNS)
	}
	inbound := document["inbounds"].([]any)[0].(map[string]any)
	if inbound["stack"] != "gvisor" {
		t.Fatalf("TUN stack=%v, want gvisor for macOS IPv4 TCP", inbound["stack"])
	}
	exclusions := inbound["route_exclude_address"].([]any)
	if !containsAnyString(exclusions, "198.18.0.0/15") {
		t.Fatalf("route exclusions=%v, want OpenClash Fake-IP bypass", exclusions)
	}
	if route["final"] != "system-direct" {
		t.Fatalf("final=%v, want system-direct", route["final"])
	}
	if route["find_process"] != true {
		t.Fatalf("find_process=%v, want true for application rules", route["find_process"])
	}
}

func TestBuildIndependentCoreConfigUsesSelectedDefaultExit(t *testing.T) {
	testCases := []struct {
		action         string
		want           string
		bypassPatterns []string
	}{
		{action: routingActionSystemDirect, want: "system-direct"},
		{action: routingActionModuleDirect, want: "module-direct"},
		{
			action:         routingActionSystemSOCKS,
			want:           "system-socks",
			bypassPatterns: []string{`^/Applications/LocalProxy\.app/`},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.action, func(t *testing.T) {
			config := defaultRoutingConfig()
			config.DefaultAction = testCase.action
			data, err := buildIndependentCoreConfig(
				config,
				testRoutingModuleInterface(),
				"en7",
				testCase.bypassPatterns,
			)
			if err != nil {
				t.Fatalf("build config: %v", err)
			}
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatalf("decode config: %v", err)
			}
			route := document["route"].(map[string]any)
			if route["final"] != testCase.want {
				t.Fatalf("final=%v, want %s", route["final"], testCase.want)
			}
			outbounds := indexedObjects(t, document["outbounds"], "tag")
			if _, exists := outbounds[testCase.want]; !exists {
				t.Fatalf("default outbound %q is missing: %v", testCase.want, outbounds)
			}
			_, hasDNS := document["dns"]
			if hasDNS != (testCase.action == routingActionModuleDirect) {
				t.Fatalf("default=%s DNS presence=%v", testCase.action, hasDNS)
			}
			if testCase.action == routingActionModuleDirect {
				rules := route["rules"].([]any)
				if len(rules) != 1 || rules[0].(map[string]any)["action"] != "hijack-dns" {
					t.Fatalf("module default rules=%v, want catch-all DNS hijack", rules)
				}
				if _, exists := rules[0].(map[string]any)["process_path_regex"]; exists {
					t.Fatalf("module default DNS hijack must be catch-all: %v", rules[0])
				}
			}
		})
	}
}

func TestBuildIndependentCoreConfigPrioritizesLocalSOCKSProcessBypass(t *testing.T) {
	config := defaultRoutingConfig()
	config.DefaultAction = routingActionSystemSOCKS
	config.Applications = []routingApplication{{
		ID: "proxy", Name: "Local Proxy", BundlePath: "/Applications/LocalProxy.app", Action: routingActionModuleDirect,
	}}
	bypassPattern := `^/Applications/LocalProxy\.app/`
	data, err := buildIndependentCoreConfig(
		config,
		testRoutingModuleInterface(),
		"en7",
		[]string{bypassPattern},
	)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	rules := document["route"].(map[string]any)["rules"].([]any)
	firstRule := rules[0].(map[string]any)
	if firstRule["outbound"] != "system-direct" {
		t.Fatalf("first outbound=%v, want SOCKS process bypass through system direct", firstRule["outbound"])
	}
	patterns := firstRule["process_path_regex"].([]any)
	if len(patterns) != 1 || patterns[0] != bypassPattern {
		t.Fatalf("first rule patterns=%v, want %q", patterns, bypassPattern)
	}
}

func TestBuildIndependentCoreConfigRequiresLocalSOCKSProcessBypass(t *testing.T) {
	config := defaultRoutingConfig()
	config.DefaultAction = routingActionSystemSOCKS
	_, err := buildIndependentCoreConfig(
		config,
		testRoutingModuleInterface(),
		"en7",
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "进程旁路") {
		t.Fatalf("error=%v, want missing local SOCKS process bypass", err)
	}
}

func TestBuildIndependentCoreConfigAllowsRemoteSOCKSDefaultWithoutProcessBypass(t *testing.T) {
	config := defaultRoutingConfig()
	config.DefaultAction = routingActionSystemSOCKS
	config.SystemSOCKS.Server = "proxy.example.com"
	data, err := buildIndependentCoreConfig(
		config,
		testRoutingModuleInterface(),
		"en7",
		nil,
	)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	outbound := indexedObjects(t, document["outbounds"], "tag")["system-socks"]
	if outbound["bind_interface"] != "en7" {
		t.Fatalf("remote SOCKS=%v, want system interface binding", outbound)
	}
}

func TestParseLSOFProcessIDs(t *testing.T) {
	pids := parseLSOFProcessIDs("p42\np7\np42\np1\npbad\n")
	if len(pids) != 2 || pids[0] != 7 || pids[1] != 42 {
		t.Fatalf("pids=%v, want [7 42]", pids)
	}
}

func TestDarwinProcessPathAndRoutingPattern(t *testing.T) {
	processPath, err := darwinProcessPath(os.Getpid())
	if err != nil {
		t.Fatalf("resolve current process path: %v", err)
	}
	pattern := routingProcessPathPattern(processPath)
	matched, err := regexp.MatchString(pattern, processPath)
	if err != nil || !matched {
		t.Fatalf("pattern=%q path=%q matched=%v err=%v", pattern, processPath, matched, err)
	}
	if got := routingProcessPathPattern("/Applications/Clash Verge.app/Contents/MacOS/Clash Verge"); got != `^/Applications/Clash Verge\.app/` {
		t.Fatalf("bundle pattern=%q", got)
	}
}

func TestResolveLoopbackSOCKSBypassPatternsFindsListenerOwner(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	patterns, err := resolveLoopbackSOCKSBypassPatterns(port)
	if err != nil {
		t.Fatalf("resolve listener owner: %v", err)
	}
	processPath, err := darwinProcessPath(os.Getpid())
	if err != nil {
		t.Fatalf("resolve current process path: %v", err)
	}
	want := routingProcessPathPattern(processPath)
	found := false
	for _, pattern := range patterns {
		if pattern == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("patterns=%v, want current process pattern %q", patterns, want)
	}
}

func TestBuildClashManagedCoreConfigHasNoTUN(t *testing.T) {
	data, err := buildClashManagedCoreConfig(defaultRoutingConfig(), testRoutingModuleInterface())
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
	if outbounds["module-direct"]["inet4_bind_address"] != "192.168.225.20" {
		t.Fatalf("outbound=%v, want module IPv4 binding", outbounds["module-direct"])
	}
	if outbounds["module-direct"]["inet6_bind_address"] != "240e:471:c30:6ad4::20" {
		t.Fatalf("outbound=%v, want module IPv6 binding", outbounds["module-direct"])
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
	moduleDefault := defaultRoutingConfig()
	moduleDefault.DefaultAction = routingActionModuleDirect
	socksDefault := defaultRoutingConfig()
	socksDefault.DefaultAction = routingActionSystemSOCKS
	bypassPatterns := []string{`^/Applications/LocalProxy\.app/`}

	module := testRoutingModuleInterface()
	testCases := []struct {
		name  string
		build func() ([]byte, error)
	}{
		{name: "independent", build: func() ([]byte, error) {
			return buildIndependentCoreConfig(independent, module, "en7", nil)
		}},
		{name: "independent-module-default", build: func() ([]byte, error) {
			return buildIndependentCoreConfig(moduleDefault, module, "en7", nil)
		}},
		{name: "independent-socks-default", build: func() ([]byte, error) {
			return buildIndependentCoreConfig(socksDefault, module, "en7", bypassPatterns)
		}},
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

func TestGeneratedIndependentConfigPassesRoutingHelperCheck(t *testing.T) {
	helperPath := os.Getenv("DJONEHUB_TEST_ROUTING_HELPER")
	if helperPath == "" {
		t.Skip("set DJONEHUB_TEST_ROUTING_HELPER to run the privileged helper integration check")
	}
	applicationRules := defaultRoutingConfig()
	applicationRules.Applications = []routingApplication{
		{ID: "module", Name: "Module App", BundlePath: "/Applications/Module.app", Action: routingActionModuleDirect},
		{ID: "proxy", Name: "Proxy App", BundlePath: "/Applications/Proxy.app", Action: routingActionSystemSOCKS},
		{ID: "system", Name: "System App", BundlePath: "/Applications/System.app", Action: routingActionSystemDirect},
	}
	moduleDefault := defaultRoutingConfig()
	moduleDefault.DefaultAction = routingActionModuleDirect
	socksDefault := defaultRoutingConfig()
	socksDefault.DefaultAction = routingActionSystemSOCKS
	for name, testCase := range map[string]struct {
		config         routingConfig
		bypassPatterns []string
	}{
		"application-rules": {config: applicationRules},
		"module-default":    {config: moduleDefault},
		"socks-default": {
			config:         socksDefault,
			bypassPatterns: []string{`^/Applications/LocalProxy\.app/`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := buildIndependentCoreConfig(
				testCase.config,
				testRoutingModuleInterface(),
				"en7",
				testCase.bypassPatterns,
			)
			if err != nil {
				t.Fatalf("build config: %v", err)
			}
			directory := t.TempDir()
			configPath := filepath.Join(directory, "config.json")
			if err := os.WriteFile(configPath, data, 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			output, err := exec.Command(helperPath, "-check-config", configPath).CombinedOutput()
			if err != nil {
				t.Fatalf("routing helper check: %v\n%s", err, output)
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

func TestParseRoutingServiceStatus(t *testing.T) {
	status, err := parseRoutingServiceStatus(routingServiceProtocolVersion + " running 4321")
	if err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if status.Version != routingServiceProtocolVersion || status.State != "running" || status.CorePID != 4321 {
		t.Fatalf("status=%+v", status)
	}
	if _, err := parseRoutingServiceStatus("broken"); err == nil {
		t.Fatal("invalid status must fail")
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

func containsAnyString(values []any, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
