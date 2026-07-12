package app

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Unknown-sir/Unknowntunnel/internal/config"
)

func TestEndToEndTCPAndUDPForwardsOverBothTransports(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef0123456789abcdef")

	tcpEcho, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpEcho.Close()
	go serveTCPEcho(tcpEcho)

	udpEcho, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udpEcho.Close()
	go serveUDPEcho(udpEcho)

	transportTCP := unusedTCPAddress(t)
	transportUDP := unusedUDPAddress(t)
	forwardTCP := unusedTCPAddress(t)
	forwardUDP := unusedUDPAddress(t)

	serverCfg := &config.Config{
		NodeID: "server", PeerID: "client", Role: "server",
		Auth: config.AuthConfig{SecretFile: "/unused/in-test"},
		Transport: config.TransportConfig{
			Mode: "both", Prefer: "udp", ListenTCP: transportTCP, ListenUDP: transportUDP,
		},
		Services: map[string]config.Service{
			"tcp-echo": {Network: "tcp", Address: tcpEcho.Addr().String()},
			"udp-echo": {Network: "udp", Address: udpEcho.LocalAddr().String()},
		},
	}
	clientCfg := &config.Config{
		NodeID: "client", PeerID: "server", Role: "client",
		Auth: config.AuthConfig{SecretFile: "/unused/in-test"},
		Transport: config.TransportConfig{
			Mode: "both", Prefer: "udp", ConnectTCP: transportTCP, ConnectUDP: transportUDP,
		},
		Forwards: []config.Forward{
			{Name: "tcp-in", Protocol: "tcp", Listen: forwardTCP, Service: "tcp-echo"},
			{Name: "udp-in", Protocol: "udp", Listen: forwardUDP, Service: "udp-echo"},
		},
	}
	if err := serverCfg.Validate(); err != nil {
		t.Fatalf("server config: %v", err)
	}
	if err := clientCfg.Validate(); err != nil {
		t.Fatalf("client config: %v", err)
	}

	serverCtx, cancelServer := context.WithCancel(context.Background())
	clientCtx, cancelClient := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	clientDone := make(chan error, 1)
	go func() { serverDone <- New(serverCfg, secret).Run(serverCtx) }()
	go func() { clientDone <- New(clientCfg, secret).Run(clientCtx) }()
	defer func() {
		cancelClient()
		cancelServer()
		waitAppStop(t, clientDone)
		waitAppStop(t, serverDone)
	}()

	tcpPayload := bytes.Repeat([]byte("tcp-forward-"), 600)
	waitForTCPEcho(t, forwardTCP, tcpPayload, 8*time.Second)

	udpPayload := bytes.Repeat([]byte("udp-fragment-"), 350)
	waitForUDPEcho(t, forwardUDP, udpPayload, 8*time.Second)
}

func serveTCPEcho(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

func serveUDPEcho(pc net.PacketConn) {
	buf := make([]byte, 65507)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		_, _ = pc.WriteTo(buf[:n], addr)
	}
}

func unusedTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

func unusedUDPAddress(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	return addr
}

func waitForTCPEcho(t *testing.T, address string, payload []byte, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 300*time.Millisecond)
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			if _, err = conn.Write(payload); err == nil {
				got := make([]byte, len(payload))
				if _, err = io.ReadFull(conn, got); err == nil && bytes.Equal(got, payload) {
					_ = conn.Close()
					return
				}
			}
			_ = conn.Close()
		}
		time.Sleep(75 * time.Millisecond)
	}
	t.Fatalf("TCP forward %s did not echo within %s", address, timeout)
}

func waitForUDPEcho(t *testing.T, address string, payload []byte, timeout time.Duration) {
	t.Helper()
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	got := make([]byte, 65507)
	for time.Now().Before(deadline) {
		_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
		if _, err := conn.Write(payload); err == nil {
			n, err := conn.Read(got)
			if err == nil && bytes.Equal(got[:n], payload) {
				return
			}
		}
	}
	t.Fatalf("UDP forward %s did not echo %d bytes within %s", address, len(payload), timeout)
}

func waitAppStop(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("app stopped with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("app did not stop within five seconds")
	}
}

func TestEndToEndUDPForwardOverUDPTransport(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef0123456789abcdef")
	udpEcho, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udpEcho.Close()
	go serveUDPEcho(udpEcho)

	transportUDP := unusedUDPAddress(t)
	forwardUDP := unusedUDPAddress(t)
	serverCfg := &config.Config{
		NodeID: "server", PeerID: "client", Role: "server",
		Auth:      config.AuthConfig{SecretFile: "/unused/in-test"},
		Transport: config.TransportConfig{Mode: "udp", Prefer: "udp", ListenUDP: transportUDP},
		Services: map[string]config.Service{
			"udp-echo": {Network: "udp", Address: udpEcho.LocalAddr().String()},
		},
	}
	clientCfg := &config.Config{
		NodeID: "client", PeerID: "server", Role: "client",
		Auth:      config.AuthConfig{SecretFile: "/unused/in-test"},
		Transport: config.TransportConfig{Mode: "udp", Prefer: "udp", ConnectUDP: transportUDP},
		Forwards: []config.Forward{
			{Name: "udp-in", Protocol: "udp", Listen: forwardUDP, Service: "udp-echo"},
		},
	}
	if err := serverCfg.Validate(); err != nil {
		t.Fatalf("server config: %v", err)
	}
	if err := clientCfg.Validate(); err != nil {
		t.Fatalf("client config: %v", err)
	}

	serverCtx, cancelServer := context.WithCancel(context.Background())
	clientCtx, cancelClient := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	clientDone := make(chan error, 1)
	go func() { serverDone <- New(serverCfg, secret).Run(serverCtx) }()
	time.Sleep(75 * time.Millisecond)
	go func() { clientDone <- New(clientCfg, secret).Run(clientCtx) }()
	defer func() {
		cancelClient()
		cancelServer()
		waitAppStop(t, clientDone)
		waitAppStop(t, serverDone)
	}()

	payload := bytes.Repeat([]byte("udp-only-fragment-"), 300)
	waitForUDPEcho(t, forwardUDP, payload, 8*time.Second)
}

func TestEndToEndTCPAndUDPForwardsOverTCPTransport(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef0123456789abcdef")

	tcpEcho, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpEcho.Close()
	go serveTCPEcho(tcpEcho)

	udpEcho, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udpEcho.Close()
	go serveUDPEcho(udpEcho)

	transportTCP := unusedTCPAddress(t)
	forwardTCP := unusedTCPAddress(t)
	forwardUDP := unusedUDPAddress(t)

	serverCfg := &config.Config{
		NodeID: "server", PeerID: "client", Role: "server",
		Auth:      config.AuthConfig{SecretFile: "/unused/in-test"},
		Transport: config.TransportConfig{Mode: "tcp", Prefer: "tcp", ListenTCP: transportTCP},
		Services: map[string]config.Service{
			"tcp-echo": {Network: "tcp", Address: tcpEcho.Addr().String()},
			"udp-echo": {Network: "udp", Address: udpEcho.LocalAddr().String()},
		},
	}
	clientCfg := &config.Config{
		NodeID: "client", PeerID: "server", Role: "client",
		Auth:      config.AuthConfig{SecretFile: "/unused/in-test"},
		Transport: config.TransportConfig{Mode: "tcp", Prefer: "tcp", ConnectTCP: transportTCP},
		Forwards: []config.Forward{
			{Name: "tcp-in", Protocol: "tcp", Listen: forwardTCP, Service: "tcp-echo"},
			{Name: "udp-in", Protocol: "udp", Listen: forwardUDP, Service: "udp-echo"},
		},
	}
	if err := serverCfg.Validate(); err != nil {
		t.Fatalf("server config: %v", err)
	}
	if err := clientCfg.Validate(); err != nil {
		t.Fatalf("client config: %v", err)
	}

	serverCtx, cancelServer := context.WithCancel(context.Background())
	clientCtx, cancelClient := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	clientDone := make(chan error, 1)
	go func() { serverDone <- New(serverCfg, secret).Run(serverCtx) }()
	go func() { clientDone <- New(clientCfg, secret).Run(clientCtx) }()
	defer func() {
		cancelClient()
		cancelServer()
		waitAppStop(t, clientDone)
		waitAppStop(t, serverDone)
	}()

	tcpPayload := bytes.Repeat([]byte("tcp-carrier-tcp-forward-"), 400)
	waitForTCPEcho(t, forwardTCP, tcpPayload, 8*time.Second)

	udpPayload := bytes.Repeat([]byte("tcp-carrier-udp-fragment-"), 2200)
	waitForUDPEcho(t, forwardUDP, udpPayload, 8*time.Second)
}
