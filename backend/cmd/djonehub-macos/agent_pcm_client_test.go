package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAgentPCMClientCancellationDuringHandshakeClosesPromptly(t *testing.T) {
	socketPath := filepath.Join("/Users/macmini/.hermes/cache/scratch", "agent-pcm-handshake-test.sock")
	_ = os.Remove(socketPath)
	defer os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}
		close(serverDone)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	clientDone := make(chan error, 1)
	go func() { clientDone <- runAgentPCMClient(ctx, socketPath, nil) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-clientDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("client did not cancel during handshake")
	}
	select {
	case <-serverDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handshake socket remained open")
	}
}

func TestRunAgentPCMClientSendsDownlinkFrame(t *testing.T) {
	socketPath := filepath.Join("/Users/macmini/.hermes/cache/scratch", "agent-pcm-downlink-test.sock")
	_ = os.Remove(socketPath)
	defer os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	downlink := make([]byte, agentPCMFrameBytes)
	downlink[0] = 0x34
	downlink[1] = 0x12
	serverFrame := make(chan []byte, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		hello := make([]byte, agentPCMHeaderBytes)
		if _, err := io.ReadFull(conn, hello); err != nil {
			serverErr <- err
			return
		}
		nonce := []byte("0123456789abcdef")
		if _, err := conn.Write(testAgentPCMFrame(agentPCMAck, 0, nonce, nil)); err != nil {
			serverErr <- err
			return
		}
		frame, sequence, frameNonce, payload, err := readAgentPCMFrame(conn)
		if err != nil {
			serverErr <- err
			return
		}
		if frame != agentPCMDownlink || sequence != 1 || !bytes.Equal(frameNonce, nonce) {
			serverErr <- &testProtocolError{"unexpected downlink frame metadata"}
			return
		}
		serverFrame <- payload
		serverErr <- nil
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientErr := make(chan error, 1)
	downlinkFrames := make(chan []byte, 1)
	downlinkFrames <- downlink
	go func() {
		clientErr <- runAgentPCMClientWithDownlink(ctx, socketPath, nil, downlinkFrames)
	}()
	select {
	case payload := <-serverFrame:
		if !bytes.Equal(payload, downlink) {
			t.Fatalf("unexpected downlink payload")
		}
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for downlink frame")
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	if err := <-clientErr; err != nil && err != context.Canceled && !errors.Is(err, io.EOF) {
		t.Fatalf("client error: %v", err)
	}
}

func TestRunAgentPCMClientStopsSenderWhenPeerSendsInvalidFrame(t *testing.T) {
	socketPath := filepath.Join("/Users/macmini/.hermes/cache/scratch", "agent-pcm-invalid-frame-test.sock")
	_ = os.Remove(socketPath)
	defer os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		hello := make([]byte, agentPCMHeaderBytes)
		if _, readErr := io.ReadFull(conn, hello); readErr != nil {
			return
		}
		nonce := []byte("0123456789abcdef")
		if _, writeErr := conn.Write(testAgentPCMFrame(agentPCMAck, 0, nonce, nil)); writeErr != nil {
			return
		}
		invalid := testAgentPCMFrame(agentPCMModule, 99, nonce, make([]byte, agentPCMFrameBytes))
		if _, writeErr := conn.Write(invalid); writeErr != nil {
			return
		}
		_, _ = io.Copy(io.Discard, conn)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	clientErr := make(chan error, 1)
	downlinkFrames := make(chan []byte, 1)
	go func() {
		clientErr <- runAgentPCMClientWithDownlink(ctx, socketPath, nil, downlinkFrames)
	}()
	select {
	case err := <-clientErr:
		if err == nil || !strings.Contains(err.Error(), "invalid Agent PCM module frame") {
			t.Fatalf("unexpected invalid-frame error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("client did not stop sender after invalid peer frame")
	}
	select {
	case <-serverDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server connection remained open after client failure")
	}
}

func TestWriteAgentPCMFrameRetriesShortWrites(t *testing.T) {
	writer := &shortFrameWriter{limit: 7}
	nonce := []byte("0123456789abcdef")
	payload := make([]byte, 320)
	if err := writeAgentPCMFrame(writer, agentPCMModule, 1, nonce, payload); err != nil {
		t.Fatal(err)
	}
	if writer.Len() != 352 {
		t.Fatalf("short write truncated frame: got %d bytes", writer.Len())
	}
}

type shortFrameWriter struct {
	bytes.Buffer
	limit int
}

func (w *shortFrameWriter) Write(payload []byte) (int, error) {
	if len(payload) > w.limit {
		payload = payload[:w.limit]
	}
	return w.Buffer.Write(payload)
}

func TestRunAgentPCMClientHandshakesAndReceivesModulePCM(t *testing.T) {
	socketPath := filepath.Join("/Users/macmini/.hermes/cache/scratch", "agent-pcm-client-test.sock")
	_ = os.Remove(socketPath)
	defer os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		hello := make([]byte, 32)
		if _, err := io.ReadFull(conn, hello); err != nil {
			serverErr <- err
			return
		}
		if string(hello[:4]) != "DJA1" || hello[4] != 1 || hello[5] != 1 || binary.BigEndian.Uint32(hello[8:12]) != 0 {
			serverErr <- &testProtocolError{"invalid hello"}
			return
		}
		nonce := []byte("0123456789abcdef")
		if _, err := conn.Write(testAgentPCMFrame(2, 0, nonce, nil)); err != nil {
			serverErr <- err
			return
		}
		pcm := make([]byte, 320)
		pcm[8] = 0x34
		pcm[9] = 0x12
		if _, err := conn.Write(testAgentPCMFrame(4, 1, nonce, pcm)); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	received := make(chan []byte, 1)
	clientErr := make(chan error, 1)
	go func() {
		clientErr <- runAgentPCMClient(ctx, socketPath, func(frame []byte) {
			received <- append([]byte(nil), frame...)
			cancel()
		})
	}()

	select {
	case pcm := <-received:
		if len(pcm) != 320 || pcm[8] != 0x34 || pcm[9] != 0x12 {
			t.Fatalf("unexpected pcm frame: len=%d", len(pcm))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for module PCM")
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	if err := <-clientErr; err != nil && err != context.Canceled {
		t.Fatalf("client error: %v", err)
	}
}

type testProtocolError struct{ message string }

func (e *testProtocolError) Error() string { return e.message }

func testAgentPCMFrame(frameType byte, sequence uint32, nonce, payload []byte) []byte {
	frame := make([]byte, 32+len(payload))
	copy(frame[:4], []byte("DJA1"))
	frame[4] = 1
	frame[5] = frameType
	binary.BigEndian.PutUint32(frame[8:12], sequence)
	binary.BigEndian.PutUint32(frame[12:16], uint32(len(payload)))
	copy(frame[16:32], nonce)
	copy(frame[32:], payload)
	return frame
}
