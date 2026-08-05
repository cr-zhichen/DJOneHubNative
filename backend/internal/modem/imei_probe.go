package modem

import (
	"errors"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// imeiCacheItem 存储 IMEI 缓存条目及对应的获取时间戳
type imeiCacheItem struct {
	IMEI string
	TS   time.Time
}

// imeiCache 提供线程安全的内存 IMEI 映射缓存，避免频繁通过串口发起硬件查询
var imeiCache struct {
	mu sync.RWMutex
	m  map[string]imeiCacheItem
}

// ProbeIMEICached 在 10 分钟缓存有效期内优先从内存缓存中获取指定 AT 串口的 IMEI；若未命中或过期，则调用底层串口方法探测
func ProbeIMEICached(atPort string, timeout time.Duration) (string, error) {
	atPort = strings.TrimSpace(atPort)
	if atPort == "" {
		return "", errors.New("empty at port")
	}

	imeiCache.mu.RLock()
	if imeiCache.m != nil {
		if it, ok := imeiCache.m[atPort]; ok {
			if it.IMEI != "" && time.Since(it.TS) < 10*time.Minute {
				imeiCache.mu.RUnlock()
				return it.IMEI, nil
			}
		}
	}
	imeiCache.mu.RUnlock()

	imei, err := ProbeIMEI(atPort, timeout)
	if err == nil && imei != "" {
		imeiCache.mu.Lock()
		if imeiCache.m == nil {
			imeiCache.m = make(map[string]imeiCacheItem)
		}
		imeiCache.m[atPort] = imeiCacheItem{IMEI: imei, TS: time.Now()}
		imeiCache.mu.Unlock()
	}
	return imei, err
}

// ProbeIMEI 通过打开底层 TTY 串口设备并执行 `AT+CGSN` 指令来实时探测模组的 IMEI 串号
func ProbeIMEI(atPort string, timeout time.Duration) (string, error) {
	atPort = strings.TrimSpace(atPort)
	if atPort == "" {
		return "", errors.New("empty at port")
	}
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}

	// 配置标准的 3 线异步串口波特率与帧校验格式
	mode := &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}

	p, err := serial.Open(atPort, mode)
	if err != nil {
		return "", err
	}
	defer p.Close()

	_ = p.SetReadTimeout(80 * time.Millisecond)

	deadline := time.Now().Add(timeout)
	buf := make([]byte, 1024)
	var acc strings.Builder

	write := func(s string) {
		_, _ = p.Write([]byte(s))
	}

	// 写入 AT 测试命令与查询 IMEI 的 AT+CGSN 命令
	write("AT\r\n")
	time.Sleep(40 * time.Millisecond)
	write("AT+CGSN\r\n")

	// 在指定的截止时间内轮询并解析串口输出内容
	for time.Now().Before(deadline) {
		n, rerr := p.Read(buf)
		if n > 0 {
			acc.Write(buf[:n])
			if imei := parseIMEI(acc.String()); imei != "" {
				return imei, nil
			}
		}
		if rerr != nil {
			if strings.Contains(strings.ToLower(rerr.Error()), "timeout") {
				continue
			}
		}
	}

	// 最终尝试解析一次累积的串口缓冲区
	if imei := parseIMEI(acc.String()); imei != "" {
		return imei, nil
	}
	return "", errors.New("imei probe timeout")
}
