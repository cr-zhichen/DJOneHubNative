package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const routingSOCKSCheckTimeout = 3 * time.Second

type routingSOCKSCheckResult struct {
	Available bool   `json:"available"`
	Address   string `json:"address,omitempty"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

func checkRoutingSOCKS(ctx context.Context, config routingSOCKSConfig) routingSOCKSCheckResult {
	config.Server = strings.TrimSpace(config.Server)
	config.Username = strings.TrimSpace(config.Username)
	if err := validateRoutingSOCKSCheckConfig(config); err != nil {
		return routingSOCKSCheckResult{Message: err.Error()}
	}

	address := net.JoinHostPort(config.Server, strconv.Itoa(config.Port))
	startedAt := time.Now()
	dialer := net.Dialer{Timeout: routingSOCKSCheckTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return routingSOCKSCheckResult{
			Address: address,
			Message: fmt.Sprintf("无法连接 %s：%v", address, err),
		}
	}
	defer connection.Close()

	deadline := time.Now().Add(routingSOCKSCheckTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return routingSOCKSCheckResult{Address: address, Message: "无法设置 SOCKS5 检测超时：" + err.Error()}
	}
	if err := performRoutingSOCKSHandshake(connection, config); err != nil {
		return routingSOCKSCheckResult{
			Address: address,
			Message: "SOCKS5 握手失败：" + err.Error(),
		}
	}

	latency := time.Since(startedAt).Milliseconds()
	if latency < 1 {
		latency = 1
	}
	message := "SOCKS5 握手成功"
	if config.Username != "" || config.Password != "" {
		message = "SOCKS5 握手及认证成功"
	}
	return routingSOCKSCheckResult{
		Available: true,
		Address:   address,
		Message:   message,
		LatencyMS: latency,
	}
}

func validateRoutingSOCKSCheckConfig(config routingSOCKSConfig) error {
	if config.Server == "" {
		return errors.New("请先填写 SOCKS5 服务器")
	}
	if strings.ContainsAny(config.Server, "\r\n\t ") || strings.Contains(config.Server, "://") {
		return errors.New("SOCKS5 服务器只填写主机名或 IP，不要包含协议和路径")
	}
	if config.Port < 1 || config.Port > 65535 {
		return errors.New("SOCKS5 端口必须在 1–65535 之间")
	}
	if config.Username == "" && config.Password != "" {
		return errors.New("填写 SOCKS5 密码时也需要填写用户名")
	}
	if len([]byte(config.Username)) > 255 {
		return errors.New("SOCKS5 用户名不能超过 255 字节")
	}
	if len([]byte(config.Password)) > 255 {
		return errors.New("SOCKS5 密码不能超过 255 字节")
	}
	return nil
}

func performRoutingSOCKSHandshake(connection net.Conn, config routingSOCKSConfig) error {
	method := byte(0x00)
	if config.Username != "" || config.Password != "" {
		method = 0x02
	}
	if err := writeRoutingSOCKSFrame(connection, []byte{0x05, 0x01, method}); err != nil {
		return fmt.Errorf("发送协商请求失败：%w", err)
	}

	response := make([]byte, 2)
	if _, err := io.ReadFull(connection, response); err != nil {
		return fmt.Errorf("读取协商响应失败：%w", err)
	}
	if response[0] != 0x05 {
		return fmt.Errorf("服务返回了非 SOCKS5 协议版本 0x%02x", response[0])
	}
	if response[1] == 0xff {
		return errors.New("服务不接受当前认证方式")
	}
	if response[1] != method {
		return fmt.Errorf("服务选择了未提供的认证方式 0x%02x", response[1])
	}
	if method != 0x02 {
		return nil
	}

	username := []byte(config.Username)
	password := []byte(config.Password)
	authentication := make([]byte, 0, 3+len(username)+len(password))
	authentication = append(authentication, 0x01, byte(len(username)))
	authentication = append(authentication, username...)
	authentication = append(authentication, byte(len(password)))
	authentication = append(authentication, password...)
	if err := writeRoutingSOCKSFrame(connection, authentication); err != nil {
		return fmt.Errorf("发送认证请求失败：%w", err)
	}
	if _, err := io.ReadFull(connection, response); err != nil {
		return fmt.Errorf("读取认证响应失败：%w", err)
	}
	if response[0] != 0x01 {
		return fmt.Errorf("服务返回了未知的认证协议版本 0x%02x", response[0])
	}
	if response[1] != 0x00 {
		return errors.New("用户名或密码未通过认证")
	}
	return nil
}

func writeRoutingSOCKSFrame(writer io.Writer, frame []byte) error {
	for len(frame) > 0 {
		written, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrUnexpectedEOF
		}
		frame = frame[written:]
	}
	return nil
}
