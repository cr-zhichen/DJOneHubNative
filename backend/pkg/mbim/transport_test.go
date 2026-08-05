package mbim

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPacketConnTransportRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	tr := newStreamTransport(client)
	defer tr.Close()

	want := encodeClose(0x44)
	errc := make(chan error, 1)
	go func() {
		msg, err := newStreamTransport(server).ReadMessage()
		if err != nil {
			errc <- err
			return
		}
		if !bytes.Equal(msg, want) {
			errc <- fmt.Errorf("server read %x, want %x", msg, want)
			return
		}
		_, err = server.Write(msg)
		errc <- err
	}()

	if err := tr.WriteMessage(want); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}
	got, err := tr.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip = %x, want %x", got, want)
	}
	if err := <-errc; err != nil && err != io.EOF {
		t.Fatalf("echo failed: %v", err)
	}
}

func TestEncodeProxyConfig(t *testing.T) {
	got := encodeProxyConfigInfo("/dev/cdc-wdm0", 30)
	want := []byte{
		0x0c, 0x00, 0x00, 0x00,
		0x1a, 0x00, 0x00, 0x00,
		0x1e, 0x00, 0x00, 0x00,
		'/', 0x00, 'd', 0x00, 'e', 0x00, 'v', 0x00, '/', 0x00,
		'c', 0x00, 'd', 0x00, 'c', 0x00, '-', 0x00, 'w', 0x00,
		'd', 0x00, 'm', 0x00, '0', 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("proxy config mismatch\n got=%x\nwant=%x", got, want)
	}
}

func TestDialAutoFallsBackToDirectWhenNoProxy(t *testing.T) {
	devicePath := filepath.Join(t.TempDir(), "missing-cdc-wdm")
	name := fmt.Sprintf("mbim-proxy-absent-%d-%d", os.Getpid(), time.Now().UnixNano())

	_, err := dialWith(dialOptions{
		mode:       "auto",
		devicePath: devicePath,
		proxyName:  name,
	})
	if err == nil {
		t.Fatal("dialWith should fail when proxy and direct device are absent")
	}
	if !strings.Contains(err.Error(), devicePath) {
		t.Fatalf("error %q should mention direct device path %q", err, devicePath)
	}
}
