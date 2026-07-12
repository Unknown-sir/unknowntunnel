package transport

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Unknown-sir/Unknowntunnel/internal/config"
)

type managerFakeSession struct {
	kind      string
	sendErr   error
	mu        sync.Mutex
	sendCount int
	closed    bool
	recv      chan []byte
	done      chan error
}

func newManagerFakeSession(kind string, sendErr error) *managerFakeSession {
	return &managerFakeSession{
		kind:    kind,
		sendErr: sendErr,
		recv:    make(chan []byte),
		done:    make(chan error),
	}
}

func (s *managerFakeSession) Kind() string           { return s.kind }
func (s *managerFakeSession) Receive() <-chan []byte { return s.recv }
func (s *managerFakeSession) Done() <-chan error     { return s.done }
func (s *managerFakeSession) Send([]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendCount++
	return s.sendErr
}
func (s *managerFakeSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *managerFakeSession) snapshot() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendCount, s.closed
}

func TestManagerBothUsesOnlyPreferredWhenHealthy(t *testing.T) {
	m := NewManager(&config.Config{Transport: config.TransportConfig{Mode: "both", Prefer: "udp"}}, []byte("01234567890123456789012345678901"))
	udp := newManagerFakeSession("udp", nil)
	tcp := newManagerFakeSession("tcp", nil)
	m.sessions["udp"] = udp
	m.sessions["tcp"] = tcp

	if err := m.Send([]byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if count, _ := udp.snapshot(); count != 1 {
		t.Fatalf("preferred UDP send count = %d, want 1", count)
	}
	if count, _ := tcp.snapshot(); count != 0 {
		t.Fatalf("standby TCP send count = %d, want 0", count)
	}
}

func TestManagerBothFailsOverAndRemovesBrokenPreferred(t *testing.T) {
	wantErr := errors.New("primary failed")
	m := NewManager(&config.Config{Transport: config.TransportConfig{Mode: "both", Prefer: "udp"}}, []byte("01234567890123456789012345678901"))
	udp := newManagerFakeSession("udp", wantErr)
	tcp := newManagerFakeSession("tcp", nil)
	m.sessions["udp"] = udp
	m.sessions["tcp"] = tcp

	if err := m.Send([]byte("hello")); err != nil {
		t.Fatalf("Send should succeed through standby: %v", err)
	}
	if count, closed := udp.snapshot(); count != 1 || !closed {
		t.Fatalf("broken UDP snapshot = count %d closed %v, want 1/true", count, closed)
	}
	if count, _ := tcp.snapshot(); count != 1 {
		t.Fatalf("standby TCP send count = %d, want 1", count)
	}
	m.mu.RLock()
	_, stillPresent := m.sessions["udp"]
	m.mu.RUnlock()
	if stillPresent {
		t.Fatal("broken preferred session was not removed")
	}
}

func TestManagerRejectsSessionAfterShutdown(t *testing.T) {
	m := NewManager(&config.Config{}, []byte("01234567890123456789012345678901"))
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	cancel()
	session := newManagerFakeSession("tcp", nil)
	m.addSession(session)
	_, closed := session.snapshot()
	if !closed {
		t.Fatal("session accepted after manager shutdown")
	}
	m.mu.RLock()
	_, present := m.sessions["tcp"]
	m.mu.RUnlock()
	if present {
		t.Fatal("shutdown manager retained a new session")
	}
}
