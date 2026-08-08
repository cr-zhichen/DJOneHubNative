package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCheckRoutingSOCKSNoAuthentication(t *testing.T) {
	config, finished := startRoutingSOCKSTestServer(t, "", "", true)
	result := checkRoutingSOCKS(context.Background(), config)
	if !result.Available || !strings.Contains(result.Message, "握手成功") {
		t.Fatalf("result=%+v, want available SOCKS5 server", result)
	}
	if result.LatencyMS < 1 {
		t.Fatalf("latency=%d, want positive milliseconds", result.LatencyMS)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestCheckRoutingSOCKSUsernamePasswordAuthentication(t *testing.T) {
	config, finished := startRoutingSOCKSTestServer(t, "tester", "secret", true)
	result := checkRoutingSOCKS(context.Background(), config)
	if !result.Available || !strings.Contains(result.Message, "认证成功") {
		t.Fatalf("result=%+v, want authenticated SOCKS5 server", result)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestCheckRoutingSOCKSReportsAuthenticationFailure(t *testing.T) {
	config, finished := startRoutingSOCKSTestServer(t, "tester", "wrong", false)
	result := checkRoutingSOCKS(context.Background(), config)
	if result.Available || !strings.Contains(result.Message, "用户名或密码") {
		t.Fatalf("result=%+v, want authentication failure", result)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestCheckRoutingSOCKSRejectsMissingAddressWithoutDialing(t *testing.T) {
	result := checkRoutingSOCKS(context.Background(), routingSOCKSConfig{})
	if result.Available || !strings.Contains(result.Message, "服务器") {
		t.Fatalf("result=%+v, want missing server validation", result)
	}
}

func startRoutingSOCKSTestServer(
	t *testing.T,
	username string,
	password string,
	authenticationSucceeds bool,
) (routingSOCKSConfig, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	finished := make(chan error, 1)
	go func() {
		defer listener.Close()
		connection, err := listener.Accept()
		if err != nil {
			finished <- fmt.Errorf("accept: %w", err)
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))

		header := make([]byte, 2)
		if _, err := io.ReadFull(connection, header); err != nil {
			finished <- fmt.Errorf("read negotiation header: %w", err)
			return
		}
		methods := make([]byte, int(header[1]))
		if _, err := io.ReadFull(connection, methods); err != nil {
			finished <- fmt.Errorf("read negotiation methods: %w", err)
			return
		}
		wantMethod := byte(0x00)
		if username != "" || password != "" {
			wantMethod = 0x02
		}
		if header[0] != 0x05 || len(methods) != 1 || methods[0] != wantMethod {
			finished <- fmt.Errorf("negotiation=%v methods=%v, want SOCKS5 method 0x%02x", header, methods, wantMethod)
			return
		}
		if _, err := connection.Write([]byte{0x05, wantMethod}); err != nil {
			finished <- fmt.Errorf("write negotiation response: %w", err)
			return
		}
		if wantMethod == 0x00 {
			finished <- nil
			return
		}

		authHeader := make([]byte, 2)
		if _, err := io.ReadFull(connection, authHeader); err != nil {
			finished <- fmt.Errorf("read authentication header: %w", err)
			return
		}
		user := make([]byte, int(authHeader[1]))
		if _, err := io.ReadFull(connection, user); err != nil {
			finished <- fmt.Errorf("read username: %w", err)
			return
		}
		passwordLength := make([]byte, 1)
		if _, err := io.ReadFull(connection, passwordLength); err != nil {
			finished <- fmt.Errorf("read password length: %w", err)
			return
		}
		pass := make([]byte, int(passwordLength[0]))
		if _, err := io.ReadFull(connection, pass); err != nil {
			finished <- fmt.Errorf("read password: %w", err)
			return
		}
		if authHeader[0] != 0x01 || string(user) != username || string(pass) != password {
			finished <- fmt.Errorf("authentication user=%q password=%q", user, pass)
			return
		}
		status := byte(0x01)
		if authenticationSucceeds {
			status = 0x00
		}
		if _, err := connection.Write([]byte{0x01, status}); err != nil {
			finished <- fmt.Errorf("write authentication response: %w", err)
			return
		}
		finished <- nil
	}()

	address := listener.Addr().(*net.TCPAddr)
	return routingSOCKSConfig{
		Server:   address.IP.String(),
		Port:     address.Port,
		Username: username,
		Password: password,
	}, finished
}
