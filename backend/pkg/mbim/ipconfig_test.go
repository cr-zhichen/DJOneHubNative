package mbim

import (
	"net"
	"testing"
)

func TestParseIPConfigurationIPv4(t *testing.T) {
	const fixed = 60
	addrOff := fixed
	gwOff := addrOff + 8
	dnsOff := gwOff + 4
	buf := make([]byte, dnsOff+4)

	le.PutUint32(buf[0:], 0)
	le.PutUint32(buf[4:], 0x0F)
	le.PutUint32(buf[12:], 1)
	le.PutUint32(buf[16:], uint32(addrOff))
	le.PutUint32(buf[28:], uint32(gwOff))
	le.PutUint32(buf[36:], 1)
	le.PutUint32(buf[40:], uint32(dnsOff))
	le.PutUint32(buf[52:], 1500)

	le.PutUint32(buf[addrOff:], 24)
	copy(buf[addrOff+4:], net.IPv4(10, 0, 0, 5).To4())
	copy(buf[gwOff:], net.IPv4(10, 0, 0, 1).To4())
	copy(buf[dnsOff:], net.IPv4(8, 8, 8, 8).To4())

	cfg, err := parseIPConfiguration(buf)
	if err != nil {
		t.Fatalf("parseIPConfiguration: %v", err)
	}
	if cfg.IPv4Address != "10.0.0.5" || cfg.IPv4PrefixLength != 24 {
		t.Fatalf("addr = %s/%d, want 10.0.0.5/24", cfg.IPv4Address, cfg.IPv4PrefixLength)
	}
	if cfg.IPv4Gateway != "10.0.0.1" {
		t.Fatalf("gw = %s, want 10.0.0.1", cfg.IPv4Gateway)
	}
	if len(cfg.IPv4DNS) != 1 || cfg.IPv4DNS[0] != "8.8.8.8" {
		t.Fatalf("dns = %v, want [8.8.8.8]", cfg.IPv4DNS)
	}
	if cfg.IPv4MTU != 1500 {
		t.Fatalf("mtu = %d, want 1500", cfg.IPv4MTU)
	}
}

func TestParseIPConfigurationIPv6(t *testing.T) {
	const fixed = 60
	addrOff := fixed
	buf := make([]byte, addrOff+20)
	le.PutUint32(buf[20:], 1)
	le.PutUint32(buf[24:], uint32(addrOff))
	le.PutUint32(buf[addrOff:], 64)
	copy(buf[addrOff+4:], net.ParseIP("2001:db8::5").To16())

	cfg, err := parseIPConfiguration(buf)
	if err != nil {
		t.Fatalf("parseIPConfiguration: %v", err)
	}
	if cfg.IPv6Address != "2001:db8::5" || cfg.IPv6PrefixLength != 64 {
		t.Fatalf("v6 addr = %s/%d, want 2001:db8::5/64", cfg.IPv6Address, cfg.IPv6PrefixLength)
	}
}
