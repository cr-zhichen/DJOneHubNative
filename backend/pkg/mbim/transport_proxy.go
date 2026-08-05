package mbim

import (
	"fmt"
	"net"
	"strings"
	"unicode/utf16"
)

const defaultProxyName = "mbim-proxy"

type dialOptions struct {
	mode       string
	devicePath string
	proxyName  string
}

type proxyConfigurer interface {
	needsProxyConfig() (string, bool)
}

type proxyTransport struct {
	*streamTransport
	devicePath string
}

func (t *proxyTransport) needsProxyConfig() (string, bool) {
	return t.devicePath, t.devicePath != ""
}

func encodeProxyConfigInfo(devicePath string, timeoutSecs uint32) []byte {
	path := utf16.Encode([]rune(devicePath))
	pathBytes := make([]byte, len(path)*2)
	for i, unit := range path {
		le.PutUint16(pathBytes[i*2:], unit)
	}

	b := make([]byte, 12+len(pathBytes))
	le.PutUint32(b[0:], 12)
	le.PutUint32(b[4:], uint32(len(pathBytes)))
	le.PutUint32(b[8:], timeoutSecs)
	copy(b[12:], pathBytes)
	return b
}

func dialWith(opts dialOptions) (Transport, error) {
	mode := strings.ToLower(opts.mode)
	switch mode {
	case "", "auto":
		if tr, err := dialProxy(opts.devicePath, opts.proxyName); err == nil {
			return tr, nil
		}
		return openDirect(opts.devicePath)
	case "proxy":
		return dialProxy(opts.devicePath, opts.proxyName)
	case "direct":
		return openDirect(opts.devicePath)
	default:
		return nil, fmt.Errorf("mbim: unknown dial mode %q", opts.mode)
	}
}

func dialProxy(devicePath, proxyName string) (Transport, error) {
	if proxyName == "" {
		proxyName = defaultProxyName
	}
	conn, err := net.Dial("unix", "@"+proxyName)
	if err != nil {
		return nil, fmt.Errorf("mbim: dial proxy @%s: %w", proxyName, err)
	}
	return &proxyTransport{
		streamTransport: newStreamTransport(conn),
		devicePath:      devicePath,
	}, nil
}
