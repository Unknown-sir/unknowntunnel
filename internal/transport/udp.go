package transport

import (
	"context"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	udpDataMagic       = "UKD1"
	udpDataVersion     = 1
	udpPacketData      = 1
	udpPacketACK       = 2
	udpHeaderLen       = 28
	maxUDPPlaintext    = 1400
	maxUDPReorder      = 4096
	maxUDPPending      = 8192
	udpRetransmitAfter = 350 * time.Millisecond
	udpMaxRetries      = 18
	maxUDPSessions     = 64
)

type pendingDatagram struct {
	packet  []byte
	last    time.Time
	retries int
}

type udpSession struct {
	sessionID uint64
	writeFn   func([]byte) error
	closeFn   func() error
	sendAEAD  cipher.AEAD
	recvAEAD  cipher.AEAD
	sendACK   []byte
	recvACK   []byte
	sendSeq   atomic.Uint64
	packetCh  chan []byte
	recvCh    chan []byte
	doneCh    chan error
	pendingMu sync.Mutex
	pending   map[uint64]*pendingDatagram
	slots     chan struct{}
	recvMu    sync.Mutex
	expected  uint64
	reorder   map[uint64][]byte
	closed    atomic.Bool
	closeOnce sync.Once
}

func newUDPSession(sessionID uint64, sendKey, recvKey, sendACK, recvACK []byte, writeFn func([]byte) error, closeFn func() error) (*udpSession, error) {
	sendAEAD, err := newAEAD(sendKey)
	if err != nil {
		return nil, err
	}
	recvAEAD, err := newAEAD(recvKey)
	if err != nil {
		return nil, err
	}
	s := &udpSession{
		sessionID: sessionID,
		writeFn:   writeFn,
		closeFn:   closeFn,
		sendAEAD:  sendAEAD,
		recvAEAD:  recvAEAD,
		sendACK:   append([]byte(nil), sendACK...),
		recvACK:   append([]byte(nil), recvACK...),
		packetCh:  make(chan []byte, 1024),
		recvCh:    make(chan []byte, 256),
		doneCh:    make(chan error, 1),
		pending:   make(map[uint64]*pendingDatagram),
		slots:     make(chan struct{}, maxUDPPending),
		expected:  1,
		reorder:   make(map[uint64][]byte),
	}
	go s.packetLoop()
	go s.retransmitLoop()
	return s, nil
}

func (s *udpSession) Kind() string           { return "udp" }
func (s *udpSession) Receive() <-chan []byte { return s.recvCh }
func (s *udpSession) Done() <-chan error     { return s.doneCh }
func (s *udpSession) Close() error           { s.finish(errors.New("session closed")); return nil }

func (s *udpSession) Send(payload []byte) error {
	if s.closed.Load() {
		return errors.New("UDP tunnel session is closed")
	}
	if len(payload) > maxUDPPlaintext {
		return fmt.Errorf("UDP tunnel message is %d bytes; maximum is %d", len(payload), maxUDPPlaintext)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case s.slots <- struct{}{}:
	case <-s.doneCh:
		return errors.New("UDP tunnel session is closed")
	case <-timer.C:
		return errors.New("UDP tunnel retransmission queue remained full for five seconds")
	}

	seq := s.sendSeq.Add(1)
	header := makeUDPHeader(udpPacketData, s.sessionID, seq, len(payload)+s.sendAEAD.Overhead())
	ciphertext := s.sendAEAD.Seal(nil, nonceFor(seq), payload, header)
	packet := append(header, ciphertext...)

	s.pendingMu.Lock()
	s.pending[seq] = &pendingDatagram{packet: append([]byte(nil), packet...), last: time.Now()}
	s.pendingMu.Unlock()
	if err := s.writeFn(packet); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, seq)
		s.pendingMu.Unlock()
		<-s.slots
		s.finish(err)
		return err
	}
	return nil
}

func (s *udpSession) deliverPacket(packet []byte) {
	if s.closed.Load() {
		return
	}
	select {
	case s.packetCh <- append([]byte(nil), packet...):
	default:
		s.finish(errors.New("UDP receive queue overflow"))
	}
}

func (s *udpSession) packetLoop() {
	for {
		select {
		case <-s.doneCh:
			return
		case packet := <-s.packetCh:
			if err := s.handlePacket(packet); err != nil {
				s.finish(err)
				return
			}
		}
	}
}

func (s *udpSession) handlePacket(packet []byte) error {
	if len(packet) < udpHeaderLen {
		return nil
	}
	if string(packet[:4]) != udpDataMagic || packet[4] != udpDataVersion {
		return nil
	}
	kind := packet[5]
	sessionID := binary.BigEndian.Uint64(packet[8:16])
	seq := binary.BigEndian.Uint64(packet[16:24])
	payloadLen := int(binary.BigEndian.Uint16(packet[24:26]))
	if sessionID != s.sessionID || payloadLen != len(packet)-udpHeaderLen {
		return nil
	}
	switch kind {
	case udpPacketACK:
		if payloadLen != 16 {
			return nil
		}
		mac := hmac.New(sha256.New, s.recvACK)
		_, _ = mac.Write(packet[:udpHeaderLen])
		if !hmac.Equal(packet[udpHeaderLen:], mac.Sum(nil)[:16]) {
			return nil
		}
		s.pendingMu.Lock()
		_, existed := s.pending[seq]
		if existed {
			delete(s.pending, seq)
		}
		s.pendingMu.Unlock()
		if existed {
			<-s.slots
		}
		return nil
	case udpPacketData:
		if payloadLen < s.recvAEAD.Overhead() {
			return nil
		}
		plaintext, err := s.recvAEAD.Open(nil, nonceFor(seq), packet[udpHeaderLen:], packet[:udpHeaderLen])
		if err != nil {
			return nil
		}
		if err := s.sendAck(seq); err != nil {
			return err
		}
		return s.acceptOrdered(seq, plaintext)
	default:
		return nil
	}
}

func (s *udpSession) sendAck(seq uint64) error {
	header := makeUDPHeader(udpPacketACK, s.sessionID, seq, 16)
	mac := hmac.New(sha256.New, s.sendACK)
	_, _ = mac.Write(header)
	packet := append(header, mac.Sum(nil)[:16]...)
	return s.writeFn(packet)
}

func (s *udpSession) acceptOrdered(seq uint64, plaintext []byte) error {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	if seq < s.expected {
		return nil
	}
	if seq-s.expected > maxUDPReorder {
		return nil
	}
	if seq > s.expected {
		if _, exists := s.reorder[seq]; !exists {
			s.reorder[seq] = append([]byte(nil), plaintext...)
		}
		return nil
	}
	if err := s.publish(plaintext); err != nil {
		return err
	}
	s.expected++
	for {
		next, ok := s.reorder[s.expected]
		if !ok {
			break
		}
		delete(s.reorder, s.expected)
		if err := s.publish(next); err != nil {
			return err
		}
		s.expected++
	}
	return nil
}

func (s *udpSession) publish(payload []byte) error {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case s.recvCh <- append([]byte(nil), payload...):
		return nil
	case <-s.doneCh:
		return errors.New("UDP session closed")
	case <-timer.C:
		return errors.New("UDP application receive queue is blocked")
	}
}

func (s *udpSession) retransmitLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if s.closed.Load() {
			return
		}
		now := time.Now()
		var resend [][]byte
		s.pendingMu.Lock()
		for _, item := range s.pending {
			if now.Sub(item.last) < udpRetransmitAfter {
				continue
			}
			if item.retries >= udpMaxRetries {
				s.pendingMu.Unlock()
				s.finish(errors.New("UDP tunnel peer stopped acknowledging packets"))
				return
			}
			item.retries++
			item.last = now
			resend = append(resend, append([]byte(nil), item.packet...))
		}
		s.pendingMu.Unlock()
		for _, packet := range resend {
			if err := s.writeFn(packet); err != nil {
				s.finish(err)
				return
			}
		}
	}
}

func (s *udpSession) finish(err error) {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.closeFn != nil {
			_ = s.closeFn()
		}
		s.doneCh <- err
		close(s.doneCh)
	})
}

func makeUDPHeader(kind uint8, sessionID, seq uint64, payloadLen int) []byte {
	header := make([]byte, udpHeaderLen)
	copy(header[:4], udpDataMagic)
	header[4] = udpDataVersion
	header[5] = kind
	binary.BigEndian.PutUint64(header[8:16], sessionID)
	binary.BigEndian.PutUint64(header[16:24], seq)
	binary.BigEndian.PutUint16(header[24:26], uint16(payloadLen))
	return header
}

type udpServer struct {
	conn          *net.UDPConn
	nodeID        string
	peerID        string
	secret        []byte
	onSession     func(Session)
	mu            sync.Mutex
	sessions      map[uint64]*udpSession
	sessionRemote map[uint64]string
	byRemote      map[string]uint64
	closed        atomic.Bool
}

func startUDPServer(addr, nodeID, peerID string, secret []byte, onSession func(Session)) (*udpServer, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	s := &udpServer{
		conn:          conn,
		nodeID:        nodeID,
		peerID:        peerID,
		secret:        append([]byte(nil), secret...),
		onSession:     onSession,
		sessions:      make(map[uint64]*udpSession),
		sessionRemote: make(map[uint64]string),
		byRemote:      make(map[string]uint64),
	}
	go s.readLoop()
	return s, nil
}

func (s *udpServer) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	_ = s.conn.Close()
	s.mu.Lock()
	for _, session := range s.sessions {
		_ = session.Close()
	}
	s.sessions = map[uint64]*udpSession{}
	s.sessionRemote = map[uint64]string{}
	s.byRemote = map[string]uint64{}
	s.mu.Unlock()
	return nil
}

func (s *udpServer) readLoop() {
	buf := make([]byte, 2048)
	for {
		n, remote, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if !s.closed.Load() {
				// The manager owns lifecycle reporting; a listener error simply stops UDP accepts.
			}
			return
		}
		packet := append([]byte(nil), buf[:n]...)
		if len(packet) >= 4 && string(packet[:4]) == handshakeMagic {
			s.handleHello(remote, packet)
			continue
		}
		if len(packet) < udpHeaderLen || string(packet[:4]) != udpDataMagic {
			continue
		}
		sessionID := binary.BigEndian.Uint64(packet[8:16])
		s.mu.Lock()
		session := s.sessions[sessionID]
		validRemote := s.sessionRemote[sessionID] == remote.String()
		s.mu.Unlock()
		if session != nil && validRemote {
			session.deliverPacket(packet)
		}
	}
}

func (s *udpServer) handleHello(remote *net.UDPAddr, packet []byte) {
	hello, err := decodeHandshake(packet, nil, s.secret)
	if err != nil || validateHandshake(hello, handshakeHello, s.peerID, s.nodeID) != nil {
		return
	}
	s.mu.Lock()
	atCapacity := len(s.sessions) >= maxUDPSessions
	s.mu.Unlock()
	if atCapacity {
		return
	}
	sessionID, err := randomUint64()
	if err != nil {
		return
	}
	serverNonce, err := randomBytes(32)
	if err != nil {
		return
	}
	welcome, err := encodeHandshake(handshake{
		Kind:      handshakeWelcome,
		SessionID: sessionID,
		Timestamp: time.Now().Unix(),
		Nonce:     serverNonce,
		NodeID:    s.nodeID,
		PeerID:    s.peerID,
	}, hello.Nonce, s.secret)
	if err != nil {
		return
	}
	keys := deriveKeys(s.secret, hello.Nonce, serverNonce, s.peerID, s.nodeID)
	remoteCopy := *remote
	session, err := newUDPSession(
		sessionID,
		keys.serverSend,
		keys.clientSend,
		keys.serverAck,
		keys.clientAck,
		func(data []byte) error { _, err := s.conn.WriteToUDP(data, &remoteCopy); return err },
		nil,
	)
	if err != nil {
		return
	}

	remoteKey := remote.String()
	s.mu.Lock()
	s.sessions[sessionID] = session
	s.sessionRemote[sessionID] = remoteKey
	s.mu.Unlock()

	if _, err := s.conn.WriteToUDP(welcome, remote); err != nil {
		_ = session.Close()
		return
	}
	go func() {
		<-session.Done()
		s.mu.Lock()
		if s.sessions[sessionID] == session {
			delete(s.sessions, sessionID)
			delete(s.sessionRemote, sessionID)
			if s.byRemote[remoteKey] == sessionID {
				delete(s.byRemote, remoteKey)
			}
		}
		s.mu.Unlock()
	}()
	go func() {
		if err := confirmSessionServer(session); err != nil {
			_ = session.Close()
			return
		}
		var old *udpSession
		s.mu.Lock()
		if s.sessions[sessionID] != session {
			s.mu.Unlock()
			_ = session.Close()
			return
		}
		if oldID := s.byRemote[remoteKey]; oldID != 0 && oldID != sessionID {
			old = s.sessions[oldID]
		}
		s.byRemote[remoteKey] = sessionID
		s.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		s.onSession(session)
	}()
}

func dialUDPSession(ctx context.Context, addr, nodeID, peerID string, secret []byte) (Session, error) {
	remote, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		return nil, err
	}
	clientNonce, err := randomBytes(32)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	hello, err := encodeHandshake(handshake{
		Kind:      handshakeHello,
		Timestamp: time.Now().Unix(),
		Nonce:     clientNonce,
		NodeID:    nodeID,
		PeerID:    peerID,
	}, nil, secret)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	buf := make([]byte, 1024)
	var welcome handshake
	for attempt := 0; attempt < 10; attempt++ {
		select {
		case <-ctx.Done():
			_ = conn.Close()
			return nil, ctx.Err()
		default:
		}
		if _, err := conn.Write(hello); err != nil {
			_ = conn.Close()
			return nil, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			_ = conn.Close()
			return nil, err
		}
		welcome, err = decodeHandshake(buf[:n], clientNonce, secret)
		if err != nil {
			continue
		}
		if err := validateHandshake(welcome, handshakeWelcome, peerID, nodeID); err != nil || welcome.SessionID == 0 {
			continue
		}
		break
	}
	if welcome.SessionID == 0 {
		_ = conn.Close()
		return nil, errors.New("UDP handshake timed out")
	}
	_ = conn.SetReadDeadline(time.Time{})
	keys := deriveKeys(secret, clientNonce, welcome.Nonce, nodeID, peerID)
	session, err := newUDPSession(
		welcome.SessionID,
		keys.clientSend,
		keys.serverSend,
		keys.clientAck,
		keys.serverAck,
		func(data []byte) error { _, err := conn.Write(data); return err },
		conn.Close,
	)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	go func() {
		readBuf := make([]byte, 2048)
		for {
			n, err := conn.Read(readBuf)
			if err != nil {
				session.finish(err)
				return
			}
			session.deliverPacket(readBuf[:n])
		}
	}()
	if err := confirmSessionClient(session); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}
