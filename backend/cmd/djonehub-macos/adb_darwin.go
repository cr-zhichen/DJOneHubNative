//go:build darwin && cgo

package main

/*
#cgo pkg-config: libusb-1.0
#include <stdlib.h>
#include <libusb.h>
*/
import "C"

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// adbClient implements the small subset of the ADB wire protocol needed by
// the module voice runtime. It talks directly to the module's USB ADB
// interface, so users do not need Android platform-tools installed.
type adbClient struct {
	ctx              *C.libusb_context
	handle           *C.libusb_device_handle
	iface            int
	endpointIn       byte
	endpointOut      byte
	mu               sync.Mutex
	remoteMaxPayload int
	nextLocalID      uint32
	connected        bool
}

type adbStream struct {
	localID  uint32
	remoteID uint32
}

const (
	adbMaxPayload = 4096
	adbVersion    = 0x01000001
)

var (
	errADBAuthRequired = errors.New("模块 ADB 要求认证，无法自动部署通话运行时")
	errADBTimeout      = errors.New("adb timeout")
)

func openDJIUSBADB() (*adbClient, error) {
	var ctx *C.libusb_context
	if rc := C.libusb_init(&ctx); rc != 0 {
		return nil, fmt.Errorf("libusb init: %s", usbErrorName(rc))
	}
	var handle *C.libusb_device_handle
	identity := ""
	for _, ids := range usbDeviceIDs {
		handle = C.libusb_open_device_with_vid_pid(ctx, C.uint16_t(ids[0]), C.uint16_t(ids[1]))
		if handle != nil {
			identity = fmt.Sprintf("%04x:%04x", ids[0], ids[1])
			break
		}
	}
	if handle == nil {
		C.libusb_exit(ctx)
		return nil, errors.New("未找到 DJI/Quectel USB ADB 设备（2ca3:4006 或 2c7c:0125）")
	}
	dev := C.libusb_get_device(handle)
	if dev == nil {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, errors.New("libusb 设备句柄无效")
	}
	var config *C.struct_libusb_config_descriptor
	if rc := C.libusb_get_active_config_descriptor(dev, &config); rc != 0 {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, fmt.Errorf("读取 USB 配置描述符：%s", usbErrorName(rc))
	}
	defer C.libusb_free_config_descriptor(config)

	var target *usbATCandidate
	interfaces := unsafe.Slice(config._interface, int(config.bNumInterfaces))
	for _, intf := range interfaces {
		altsettings := unsafe.Slice(intf.altsetting, int(intf.num_altsetting))
		for _, alt := range altsettings {
			// Android's USB ADB function uses the vendor-specific ff/42/01
			// interface triple. Matching the full descriptor avoids claiming an
			// unrelated vendor interface that happens to have the same index.
			if byte(alt.bInterfaceClass) != 0xff ||
				byte(alt.bInterfaceSubClass) != 0x42 ||
				byte(alt.bInterfaceProtocol) != 0x01 {
				continue
			}
			var endpointIn, endpointOut byte
			endpoints := unsafe.Slice(alt.endpoint, int(alt.bNumEndpoints))
			for _, endpoint := range endpoints {
				attributes := byte(endpoint.bmAttributes) & byte(C.LIBUSB_TRANSFER_TYPE_MASK)
				if attributes != byte(C.LIBUSB_TRANSFER_TYPE_BULK) {
					continue
				}
				address := byte(endpoint.bEndpointAddress)
				if address&byte(C.LIBUSB_ENDPOINT_IN) != 0 {
					endpointIn = address
				} else {
					endpointOut = address
				}
			}
			if endpointIn != 0 && endpointOut != 0 {
				target = &usbATCandidate{
					iface:       int(alt.bInterfaceNumber),
					endpointIn:  endpointIn,
					endpointOut: endpointOut,
				}
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, fmt.Errorf("设备 %s 上没有找到 USB ADB 接口（interface 6/subclass 66）", identity)
	}
	if rc := C.libusb_claim_interface(handle, C.int(target.iface)); rc != 0 {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, fmt.Errorf("占用 USB ADB interface %d：%s", target.iface, usbErrorName(rc))
	}
	return &adbClient{
		ctx:              ctx,
		handle:           handle,
		iface:            target.iface,
		endpointIn:       target.endpointIn,
		endpointOut:      target.endpointOut,
		remoteMaxPayload: adbMaxPayload,
		nextLocalID:      1,
	}, nil
}

func (a *adbClient) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handle == nil {
		return
	}
	C.libusb_release_interface(a.handle, C.int(a.iface))
	C.libusb_close(a.handle)
	C.libusb_exit(a.ctx)
	a.handle = nil
	a.ctx = nil
}

func (a *adbClient) bulkWrite(payload []byte, timeout time.Duration) error {
	if len(payload) == 0 {
		return nil
	}
	var transferred C.int
	rc := C.libusb_bulk_transfer(
		a.handle,
		C.uchar(a.endpointOut),
		(*C.uchar)(unsafe.Pointer(&payload[0])),
		C.int(len(payload)),
		&transferred,
		C.uint(timeout.Milliseconds()),
	)
	if rc != 0 {
		return fmt.Errorf("USB ADB 写入：%s", usbErrorName(rc))
	}
	if int(transferred) != len(payload) {
		return fmt.Errorf("USB ADB 写入不完整：%d/%d", int(transferred), len(payload))
	}
	return nil
}

func (a *adbClient) bulkRead(buffer []byte, timeout time.Duration) (int, error) {
	var transferred C.int
	rc := C.libusb_bulk_transfer(
		a.handle,
		C.uchar(a.endpointIn),
		(*C.uchar)(unsafe.Pointer(&buffer[0])),
		C.int(len(buffer)),
		&transferred,
		C.uint(timeout.Milliseconds()),
	)
	if rc == C.LIBUSB_ERROR_TIMEOUT {
		return 0, errADBTimeout
	}
	if rc != 0 {
		return 0, fmt.Errorf("USB ADB 读取：%s", usbErrorName(rc))
	}
	return int(transferred), nil
}

func adbCommand(text string) uint32 {
	bytes := []byte(text)
	return uint32(bytes[0]) | uint32(bytes[1])<<8 | uint32(bytes[2])<<16 | uint32(bytes[3])<<24
}

func adbChecksum(payload []byte) uint32 {
	var sum uint32
	for _, value := range payload {
		sum += uint32(value)
	}
	return sum
}

func adbEncodeHeader(command, argument0, argument1 uint32, payload []byte) []byte {
	header := make([]byte, 24)
	lePutUint32(header[0:], command)
	lePutUint32(header[4:], argument0)
	lePutUint32(header[8:], argument1)
	lePutUint32(header[12:], uint32(len(payload)))
	lePutUint32(header[16:], adbChecksum(payload))
	lePutUint32(header[20:], command^0xffffffff)
	return header
}

func lePutUint32(buffer []byte, value uint32) {
	buffer[0] = byte(value)
	buffer[1] = byte(value >> 8)
	buffer[2] = byte(value >> 16)
	buffer[3] = byte(value >> 24)
}

func leUint32(buffer []byte) uint32 {
	return uint32(buffer[0]) | uint32(buffer[1])<<8 | uint32(buffer[2])<<16 | uint32(buffer[3])<<24
}

type adbMessage struct {
	command uint32
	arg0    uint32
	arg1    uint32
	payload []byte
}

func (a *adbClient) sendLocked(command, argument0, argument1 uint32, payload []byte, timeout time.Duration) error {
	if err := a.bulkWrite(adbEncodeHeader(command, argument0, argument1, payload), timeout); err != nil {
		return err
	}
	if len(payload) > 0 {
		return a.bulkWrite(payload, timeout)
	}
	return nil
}

func (a *adbClient) readExactlyLocked(length int, deadline time.Time) ([]byte, error) {
	if length == 0 {
		return []byte{}, nil
	}
	output := make([]byte, 0, length)
	for len(output) < length && time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining > 100*time.Millisecond {
			remaining = 100 * time.Millisecond
		}
		buffer := make([]byte, length-len(output))
		if len(buffer) > 4096 {
			buffer = buffer[:4096]
		}
		read, err := a.bulkRead(buffer, remaining)
		if err != nil {
			if errors.Is(err, errADBTimeout) {
				continue
			}
			return nil, err
		}
		if read > 0 {
			output = append(output, buffer[:read]...)
		}
	}
	if len(output) != length {
		return nil, fmt.Errorf("等待模块 ADB 数据超时（需要 %d 字节，得到 %d）", length, len(output))
	}
	return output, nil
}

func (a *adbClient) receiveLocked(deadline time.Time) (adbMessage, error) {
	header, err := a.readExactlyLocked(24, deadline)
	if err != nil {
		return adbMessage{}, err
	}
	length := leUint32(header[12:])
	if length > uint32(a.remoteMaxPayload) || length > adbMaxPayload {
		return adbMessage{}, fmt.Errorf("ADB 消息长度无效：%d", length)
	}
	payload, err := a.readExactlyLocked(int(length), deadline)
	if err != nil {
		return adbMessage{}, err
	}
	command := leUint32(header[0:])
	if command^0xffffffff != leUint32(header[20:]) {
		return adbMessage{}, errors.New("ADB 消息 magic 不匹配")
	}
	if adbChecksum(payload) != leUint32(header[16:]) {
		return adbMessage{}, errors.New("ADB 消息校验和不匹配")
	}
	return adbMessage{command: command, arg0: leUint32(header[4:]), arg1: leUint32(header[8:]), payload: payload}, nil
}

func (a *adbClient) connectLocked() error {
	if a.connected {
		return nil
	}
	const (
		commandCNXN = 0x4e584e43
		commandAUTH = 0x48545541
		commandWRTE = 0x45545257
		commandOKAY = 0x59414b4f
		commandCLSE = 0x45534c43
	)
	banner := append([]byte("host::DJOneHub"), 0)
	sendConnect := func() error {
		return a.sendLocked(commandCNXN, adbVersion, adbMaxPayload, banner, 2*time.Second)
	}
	if err := sendConnect(); err != nil {
		return err
	}
	deadline := time.Now().Add(8 * time.Second)
	staleMessages := 0
	for time.Now().Before(deadline) {
		message, err := a.receiveLocked(deadline)
		if err != nil {
			return err
		}
		switch message.command {
		case commandAUTH:
			return errADBAuthRequired
		case commandCNXN:
			if message.arg1 > 0 {
				if int(message.arg1) < a.remoteMaxPayload {
					a.remoteMaxPayload = int(message.arg1)
				}
				a.connected = true
				return nil
			}
		case commandWRTE, commandOKAY, commandCLSE:
			staleMessages++
			if staleMessages > 64 {
				return errors.New("ADB 旧流无法清理")
			}
			if message.arg0 != 0 && message.arg1 != 0 {
				if err := a.sendLocked(commandCLSE, message.arg1, message.arg0, nil, 2*time.Second); err != nil {
					return err
				}
			}
			if err := sendConnect(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("模块未接受 ADB CNXN（command=0x%08X）", message.command)
		}
	}
	return errors.New("等待模块接受 ADB CNXN 超时")
}

func (a *adbClient) openServiceLocked(service string) (adbStream, error) {
	const (
		commandCNXN = 0x4e584e43
		commandOKAY = 0x59414b4f
		commandOPEN = 0x4e45504f
		commandWRTE = 0x45545257
		commandCLSE = 0x45534c43
	)
	payload := append([]byte(service), 0)
	if len(payload) > a.remoteMaxPayload {
		return adbStream{}, errors.New("ADB 服务命令过长")
	}
	localID := a.nextLocalID
	a.nextLocalID++
	if a.nextLocalID == 0 {
		a.nextLocalID = 1
	}
	if err := a.sendLocked(commandOPEN, localID, 0, payload, 2*time.Second); err != nil {
		return adbStream{}, err
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		message, err := a.receiveLocked(deadline)
		if err != nil {
			return adbStream{}, err
		}
		switch message.command {
		case commandOKAY:
			if message.arg0 != 0 && message.arg1 == localID && len(message.payload) == 0 {
				return adbStream{localID: localID, remoteID: message.arg0}, nil
			}
		case commandCNXN:
			if message.arg1 > 0 && int(message.arg1) < a.remoteMaxPayload {
				a.remoteMaxPayload = int(message.arg1)
			}
		case commandCLSE:
			if message.arg1 == localID {
				return adbStream{}, errors.New("模块拒绝 ADB 服务")
			}
			if message.arg0 != 0 && message.arg1 != 0 {
				if err := a.sendLocked(commandCLSE, message.arg1, message.arg0, nil, 2*time.Second); err != nil {
					return adbStream{}, err
				}
			}
		case commandWRTE:
			if message.arg0 != 0 && message.arg1 != 0 {
				if err := a.sendLocked(commandCLSE, message.arg1, message.arg0, nil, 2*time.Second); err != nil {
					return adbStream{}, err
				}
			}
		default:
			return adbStream{}, errors.New("模块拒绝 ADB 服务")
		}
	}
	return adbStream{}, errors.New("等待模块打开 ADB 服务超时")
}

func (a *adbClient) writeStreamLocked(stream adbStream, data []byte, timeout time.Duration) error {
	const (
		commandWRTE = 0x45545257
		commandOKAY = 0x59414b4f
	)
	if len(data) > a.remoteMaxPayload {
		return errors.New("ADB sync 数据块过大")
	}
	if err := a.sendLocked(commandWRTE, stream.localID, stream.remoteID, data, timeout); err != nil {
		return err
	}
	message, err := a.receiveLocked(time.Now().Add(10 * time.Second))
	if err != nil {
		return err
	}
	if message.command != commandOKAY || message.arg0 != stream.remoteID || message.arg1 != stream.localID || len(message.payload) != 0 {
		return errors.New("模块未确认 ADB 数据块")
	}
	return nil
}

func (a *adbClient) closeStreamLocked(stream adbStream) error {
	const (
		commandCLSE = 0x45534c43
		commandWRTE = 0x45545257
		commandOKAY = 0x59414b4f
	)
	if err := a.sendLocked(commandCLSE, stream.localID, stream.remoteID, nil, 2*time.Second); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		message, err := a.receiveLocked(deadline)
		if err != nil {
			return err
		}
		if message.command == commandCLSE && message.arg0 == stream.remoteID && message.arg1 == stream.localID && len(message.payload) == 0 {
			return nil
		}
		if message.command == commandWRTE && message.arg0 == stream.remoteID && message.arg1 == stream.localID {
			if err := a.sendLocked(commandOKAY, stream.localID, stream.remoteID, nil, 2*time.Second); err != nil {
				return err
			}
			continue
		}
		return errors.New("ADB sync 关闭响应无效")
	}
	return errors.New("等待模块关闭 ADB sync 流超时")
}

func (a *adbClient) shellChecked(command string, timeout time.Duration) (string, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handle == nil {
		return "", 0, errors.New("ADB 通道未打开")
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if err := a.connectLocked(); err != nil {
		return "", 0, err
	}
	token := adbToken()
	wrapper := "{ " + command + "; }; __dj_status=$?; printf '\\n__DJ_STATUS_" + token + "_%u__\\n' \"$__dj_status\""
	stream, err := a.openServiceLocked("shell:" + wrapper)
	if err != nil {
		return "", 0, err
	}
	var output strings.Builder
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		message, err := a.receiveLocked(deadline)
		if err != nil {
			a.connected = false
			return output.String(), 0, err
		}
		switch message.command {
		case 0x45545257: // WRTE
			if message.arg0 == stream.remoteID && message.arg1 == stream.localID {
				output.Write(message.payload)
				if err := a.sendLocked(0x59414b4f, stream.localID, stream.remoteID, nil, 2*time.Second); err != nil {
					return output.String(), 0, err
				}
			}
		case 0x45534c43: // CLSE
			if message.arg1 != stream.localID {
				continue
			}
			if message.arg0 != 0 {
				_ = a.sendLocked(0x45534c43, stream.localID, stream.remoteID, nil, 2*time.Second)
			}
			status, ok := parseADBStatus(output.String(), token)
			if !ok {
				a.connected = false
				return output.String(), 0, errors.New("模块 shell 没有返回退出状态")
			}
			return stripADBStatus(output.String(), token), status, nil
		}
	}
	a.connected = false
	return output.String(), 0, errors.New("等待模块 shell 超时")
}

func adbToken() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", bytes)
}

func parseADBStatus(raw, token string) (int, bool) {
	prefix := "__DJ_STATUS_" + token + "_"
	index := strings.LastIndex(raw, prefix)
	if index < 0 {
		return 0, false
	}
	rest := raw[index+len(prefix):]
	end := strings.Index(rest, "__")
	if end < 0 {
		return 0, false
	}
	status, err := strconvAtoiStrict(rest[:end])
	return status, err == nil
}

func stripADBStatus(raw, token string) string {
	prefix := "__DJ_STATUS_" + token + "_"
	index := strings.LastIndex(raw, prefix)
	if index < 0 {
		return strings.TrimSpace(raw)
	}
	return strings.TrimSpace(raw[:index])
}

func strconvAtoiStrict(value string) (int, error) {
	status, err := strconv.Atoi(value)
	if err != nil || status < 0 || status > 255 {
		if err == nil {
			err = errors.New("ADB shell 退出状态超出范围")
		}
		return 0, err
	}
	return status, nil
}

func adbSyncDonePacket(modificationTime uint32) []byte {
	packet := make([]byte, 8)
	copy(packet[:4], "DONE")
	// In the ADB sync protocol DONE is exceptional: the second word is the
	// modification time itself, not a payload length, and no payload follows.
	lePutUint32(packet[4:], modificationTime)
	return packet
}

func (a *adbClient) push(data []byte, remotePath string, mode uint32, timeout time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handle == nil {
		return errors.New("ADB 通道未打开")
	}
	if strings.ContainsAny(remotePath, ",\x00") {
		return errors.New("ADB push 目标路径无效")
	}
	if err := a.connectLocked(); err != nil {
		return err
	}
	stream, err := a.openServiceLocked("sync:")
	if err != nil {
		return err
	}
	sendName := []byte(fmt.Sprintf("%s,%d", remotePath, mode))
	if err := a.writeSyncLocked(stream, "SEND", sendName, timeout); err != nil {
		_ = a.closeStreamLocked(stream)
		return err
	}
	chunkCapacity := a.remoteMaxPayload - 8
	for offset := 0; offset < len(data); offset += chunkCapacity {
		end := offset + chunkCapacity
		if end > len(data) {
			end = len(data)
		}
		if err := a.writeSyncLocked(stream, "DATA", data[offset:end], timeout); err != nil {
			_ = a.closeStreamLocked(stream)
			return err
		}
	}
	if err := a.writeStreamLocked(stream, adbSyncDonePacket(uint32(time.Now().Unix())), timeout); err != nil {
		_ = a.closeStreamLocked(stream)
		return err
	}
	var response []byte
	deadline := time.Now().Add(20 * time.Second)
	for len(response) < 8 && time.Now().Before(deadline) {
		message, err := a.receiveLocked(deadline)
		if err != nil {
			_ = a.closeStreamLocked(stream)
			return err
		}
		if message.command == 0x45545257 && message.arg0 == stream.remoteID && message.arg1 == stream.localID {
			response = append(response, message.payload...)
			if err := a.sendLocked(0x59414b4f, stream.localID, stream.remoteID, nil, 2*time.Second); err != nil {
				return err
			}
		} else if message.command == 0x45534c43 {
			return errors.New("ADB sync 提前关闭")
		}
	}
	if len(response) < 8 {
		_ = a.closeStreamLocked(stream)
		return errors.New("等待模块 ADB sync 响应超时")
	}
	identifier := string(response[:4])
	value := leUint32(response[4:8])
	if identifier == "FAIL" {
		detail := ""
		if int(value) > 0 && len(response) >= 8+int(value) {
			detail = string(response[8 : 8+value])
		}
		_ = a.closeStreamLocked(stream)
		return fmt.Errorf("模块拒绝文件传输：%s", detail)
	}
	if identifier != "OKAY" || value != 0 {
		_ = a.closeStreamLocked(stream)
		return errors.New("ADB sync 返回无效状态")
	}
	return a.closeStreamLocked(stream)
}

func (a *adbClient) writeSyncLocked(stream adbStream, identifier string, payload []byte, timeout time.Duration) error {
	packet := make([]byte, 8+len(payload))
	copy(packet[:4], identifier)
	lePutUint32(packet[4:8], uint32(len(payload)))
	copy(packet[8:], payload)
	return a.writeStreamLocked(stream, packet, timeout)
}
