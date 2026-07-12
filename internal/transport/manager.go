package transport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/Unknown-sir/Unknowntunnel/internal/config"
)

const maxConcurrentTCPHandshakes = 128

type Manager struct {
	cfg      *config.Config
	secret   []byte
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	sessions map[string]Session
	incoming chan []byte
	closers  []func() error
	wg       sync.WaitGroup
}

func NewManager(cfg *config.Config, secret []byte) *Manager {
	return &Manager{
		cfg:      cfg,
		secret:   append([]byte(nil), secret...),
		sessions: make(map[string]Session),
		incoming: make(chan []byte, 1024),
	}
}

func (m *Manager) Start(parent context.Context) error {
	m.ctx, m.cancel = context.WithCancel(parent)
	if m.cfg.Role == "server" {
		if m.uses("tcp") {
			if err := m.startTCPListener(); err != nil {
				m.Close()
				return err
			}
		}
		if m.uses("udp") {
			server, err := startUDPServer(m.cfg.Transport.ListenUDP, m.cfg.NodeID, m.cfg.PeerID, m.secret, m.addSession)
			if err != nil {
				m.Close()
				return fmt.Errorf("listen UDP transport: %w", err)
			}
			m.closers = append(m.closers, server.Close)
			log.Printf("UDP transport listening on %s", m.cfg.Transport.ListenUDP)
		}
		return nil
	}
	if m.uses("tcp") {
		m.wg.Add(1)
		go m.clientLoop("tcp")
	}
	if m.uses("udp") {
		m.wg.Add(1)
		go m.clientLoop("udp")
	}
	return nil
}

func (m *Manager) Incoming() <-chan []byte { return m.incoming }

func (m *Manager) Ready(kind string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[kind]
	return ok
}

func (m *Manager) Send(data []byte) error {
	// In both mode both carriers stay connected, but each message uses one path.
	// The preferred path is attempted first; if that Send call returns an error,
	// the same encoded message is attempted on the standby carrier. This avoids
	// duplicate bandwidth while still handling immediately detected failures.
	order := []string{m.cfg.Transport.Prefer}
	if m.cfg.Transport.Mode == "both" {
		if m.cfg.Transport.Prefer == "udp" {
			order = append(order, "tcp")
		} else {
			order = append(order, "udp")
		}
	}
	var lastErr error
	for _, kind := range order {
		m.mu.RLock()
		session := m.sessions[kind]
		m.mu.RUnlock()
		if session == nil {
			continue
		}
		if err := session.Send(data); err == nil {
			return nil
		} else {
			lastErr = err
			m.removeSession(kind, session)
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return ErrNoSession
}

func (m *Manager) Close() error {
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Lock()
	for _, session := range m.sessions {
		_ = session.Close()
	}
	m.sessions = make(map[string]Session)
	m.mu.Unlock()
	for _, closeFn := range m.closers {
		_ = closeFn()
	}
	m.wg.Wait()
	return nil
}

func (m *Manager) uses(kind string) bool {
	return m.cfg.Transport.Mode == kind || m.cfg.Transport.Mode == "both"
}

func (m *Manager) startTCPListener() error {
	listener, err := net.Listen("tcp", m.cfg.Transport.ListenTCP)
	if err != nil {
		return fmt.Errorf("listen TCP transport: %w", err)
	}
	m.closers = append(m.closers, listener.Close)
	handshakeSlots := make(chan struct{}, maxConcurrentTCPHandshakes)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-m.ctx.Done():
					return
				default:
					log.Printf("TCP transport accept error: %v", err)
					continue
				}
			}
			select {
			case handshakeSlots <- struct{}{}:
			default:
				_ = conn.Close()
				continue
			}
			m.wg.Add(1)
			go func(conn net.Conn) {
				defer m.wg.Done()
				defer func() { <-handshakeSlots }()
				session, err := acceptTCPSession(conn, m.cfg.NodeID, m.cfg.PeerID, m.secret)
				if err != nil {
					_ = conn.Close()
					log.Printf("rejected TCP tunnel peer %s: %v", conn.RemoteAddr(), err)
					return
				}
				m.addSession(session)
			}(conn)
		}
	}()
	log.Printf("TCP transport listening on %s", m.cfg.Transport.ListenTCP)
	return nil
}

func (m *Manager) clientLoop(kind string) {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}
		var (
			session Session
			err     error
		)
		if kind == "tcp" {
			session, err = dialTCPSession(m.ctx, m.cfg.Transport.ConnectTCP, m.cfg.NodeID, m.cfg.PeerID, m.secret)
		} else {
			session, err = dialUDPSession(m.ctx, m.cfg.Transport.ConnectUDP, m.cfg.NodeID, m.cfg.PeerID, m.secret)
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("%s transport connection failed: %v", kind, err)
			}
			if !sleepContext(m.ctx, 2*time.Second) {
				return
			}
			continue
		}
		m.addSession(session)
		select {
		case <-m.ctx.Done():
			_ = session.Close()
			return
		case <-session.Done():
		}
		if !sleepContext(m.ctx, time.Second) {
			return
		}
	}
}

func (m *Manager) addSession(session Session) {
	kind := session.Kind()
	m.mu.Lock()
	if m.ctx == nil || m.ctx.Err() != nil {
		m.mu.Unlock()
		_ = session.Close()
		return
	}
	old := m.sessions[kind]
	m.sessions[kind] = session
	m.mu.Unlock()
	if old != nil && old != session {
		_ = old.Close()
	}
	log.Printf("authenticated %s tunnel session is ready", kind)
	go func() {
		for {
			select {
			case <-m.ctx.Done():
				_ = session.Close()
				return
			case <-session.Done():
				m.removeSession(kind, session)
				return
			case data := <-session.Receive():
				select {
				case m.incoming <- data:
				case <-m.ctx.Done():
					return
				}
			}
		}
	}()
}

func (m *Manager) removeSession(kind string, session Session) {
	m.mu.Lock()
	if m.sessions[kind] == session {
		delete(m.sessions, kind)
		log.Printf("%s tunnel session disconnected", kind)
	}
	m.mu.Unlock()
	_ = session.Close()
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
