package transport

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func TestTCPSessionRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverCh := make(chan Session, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		session, err := acceptTCPSession(conn, "server", "client", secret)
		if err != nil {
			errCh <- err
			return
		}
		serverCh <- session
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := dialTCPSession(ctx, listener.Addr().String(), "client", "server", secret)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var server Session
	select {
	case server = <-serverCh:
	case err := <-errCh:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer server.Close()

	want := []byte("hello over encrypted TCP")
	if err := client.Send(want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-server.Receive():
		if !bytes.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	reply := []byte("reply")
	if err := server.Send(reply); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-client.Receive():
		if !bytes.Equal(got, reply) {
			t.Fatalf("got %q, want %q", got, reply)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestUDPSessionRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	serverCh := make(chan Session, 1)
	server, err := startUDPServer("127.0.0.1:0", "server", "client", secret, func(s Session) {
		serverCh <- s
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client, err := dialUDPSession(ctx, server.conn.LocalAddr().String(), "client", "server", secret)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var peer Session
	select {
	case peer = <-serverCh:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer peer.Close()

	udpPeer, ok := peer.(*udpSession)
	if !ok {
		t.Fatal("expected UDP session implementation")
	}
	bad := append(makeUDPHeader(udpPacketData, udpPeer.sessionID, 999, 16), make([]byte, 16)...)
	udpPeer.deliverPacket(bad)
	select {
	case <-peer.Done():
		t.Fatal("unauthenticated UDP packet closed the session")
	case <-time.After(50 * time.Millisecond):
	}

	want := bytes.Repeat([]byte{0x5a}, 1400)
	if err := client.Send(want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-peer.Receive():
		if !bytes.Equal(got, want) {
			t.Fatalf("received UDP payload mismatch: %d bytes", len(got))
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	reply := []byte("udp reply")
	if err := peer.Send(reply); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-client.Receive():
		if !bytes.Equal(got, reply) {
			t.Fatalf("got %q, want %q", got, reply)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
