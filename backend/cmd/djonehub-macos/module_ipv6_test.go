//go:build darwin && cgo

package main

import "testing"

func TestParseIPv6Setting(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		enabled bool
		state   string
	}{
		{
			name: "automatic（模块默认状态，RA 下发地址）",
			input: "Manual Configuration\nIP address: 192.168.225.22\nSubnet mask: 255.255.255.0\n" +
				"Router: 192.168.225.1\nIPv6: Automatic\nIPv6 IP address: none\nIPv6 Router: none\n",
			enabled: true,
			state:   "Automatic",
		},
		{
			name:    "已关闭",
			input:   "Manual Configuration\nIP address: 192.168.225.22\nIPv6: Off\n",
			enabled: false,
			state:   "Off",
		},
		{
			name:    "手动配置的静态 IPv6",
			input:   "Manual Configuration\nIPv6: Manual\nIPv6 IP address: 240e:478::1\n",
			enabled: true,
			state:   "Manual",
		},
		{
			name:    "仅链路本地",
			input:   "IPv6: Link-local only\n",
			enabled: true,
			state:   "Link-local only",
		},
		{
			name:    "中文界面关闭",
			input:   "手动配置\nIPv6: 关闭\n",
			enabled: false,
			state:   "关闭",
		},
		{
			name:    "没有 IPv6 字段时按关闭处理",
			input:   "Manual Configuration\nIP address: 10.0.0.2\n",
			enabled: false,
			state:   "",
		},
		{
			name:    "未知取值保守认为开启",
			input:   "IPv6: SomethingNew\n",
			enabled: true,
			state:   "SomethingNew",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enabled, state := parseIPv6Setting(c.input)
			if enabled != c.enabled || state != c.state {
				t.Fatalf("parseIPv6Setting() = (%v, %q), want (%v, %q)", enabled, state, c.enabled, c.state)
			}
		})
	}
}
