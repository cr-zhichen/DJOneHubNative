package mbim

import (
	"context"
	"fmt"
	"net"
)

type IPConfiguration struct {
	IPv4Address      string
	IPv4PrefixLength uint32
	IPv4Gateway      string
	IPv4DNS          []string
	IPv4MTU          uint32

	IPv6Address      string
	IPv6PrefixLength uint32
	IPv6Gateway      string
	IPv6DNS          []string
	IPv6MTU          uint32
}

func ipAt(b []byte, off int, n int) (string, error) {
	if off <= 0 {
		return "", nil
	}
	if off+n > len(b) {
		return "", fmt.Errorf("mbim: ip at %d len %d out of range (buf %d)", off, n, len(b))
	}
	return net.IP(append([]byte(nil), b[off:off+n]...)).String(), nil
}

func parseIPConfiguration(info []byte) (IPConfiguration, error) {
	if len(info) < 60 {
		return IPConfiguration{}, fmt.Errorf("mbim: IP_CONFIGURATION response too short len=%d", len(info))
	}
	r := newInfoReader(info)
	var cfg IPConfiguration

	if v4Count, _ := r.u32At(12); v4Count > 0 {
		addrOff, _ := r.u32At(16)
		if int(addrOff)+8 <= len(info) {
			cfg.IPv4PrefixLength = le.Uint32(info[addrOff:])
			cfg.IPv4Address, _ = ipAt(info, int(addrOff)+4, 4)
		}
	}
	if gwOff, _ := r.u32At(28); gwOff > 0 {
		cfg.IPv4Gateway, _ = ipAt(info, int(gwOff), 4)
	}
	if dnsCount, _ := r.u32At(36); dnsCount > 0 {
		dnsOff, _ := r.u32At(40)
		for i := uint32(0); i < dnsCount; i++ {
			if s, err := ipAt(info, int(dnsOff)+int(i)*4, 4); err == nil && s != "" {
				cfg.IPv4DNS = append(cfg.IPv4DNS, s)
			}
		}
	}
	cfg.IPv4MTU, _ = r.u32At(52)

	if v6Count, _ := r.u32At(20); v6Count > 0 {
		addrOff, _ := r.u32At(24)
		if int(addrOff)+20 <= len(info) {
			cfg.IPv6PrefixLength = le.Uint32(info[addrOff:])
			cfg.IPv6Address, _ = ipAt(info, int(addrOff)+4, 16)
		}
	}
	if gwOff, _ := r.u32At(32); gwOff > 0 {
		cfg.IPv6Gateway, _ = ipAt(info, int(gwOff), 16)
	}
	if dnsCount, _ := r.u32At(44); dnsCount > 0 {
		dnsOff, _ := r.u32At(48)
		for i := uint32(0); i < dnsCount; i++ {
			if s, err := ipAt(info, int(dnsOff)+int(i)*16, 16); err == nil && s != "" {
				cfg.IPv6DNS = append(cfg.IPv6DNS, s)
			}
		}
	}
	cfg.IPv6MTU, _ = r.u32At(56)

	return cfg, nil
}

func QueryIPConfiguration(ctx context.Context, d *Device, sessionID uint32) (IPConfiguration, error) {
	info := make([]byte, 60)
	le.PutUint32(info[0:], sessionID)
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectIPConfiguration, CommandTypeQuery, info)
	if err != nil {
		return IPConfiguration{}, err
	}
	if resp.Status != 0 {
		return IPConfiguration{}, &StatusError{Op: "IP_CONFIGURATION", Status: resp.Status}
	}
	return parseIPConfiguration(resp.InfoBuffer)
}
