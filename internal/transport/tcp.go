package transport

import (
	"context"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	maxTCPFrame     = 2 << 20
	tcpWriteTimeout = 15 * time.Second
	tcpReadIdle     = 45 * time.Second
)

type tcpSession struct {
	conn     net.Conn
	sendAEAD cipher.AEAD
	recvAEAD cipher.AEAD
	sendSeq  uint64
	recvSeq  uint64
	writeMu  sync.Mutex
	recvCh   chan []byte
	doneCh   chan error
	closeOne sync.Once
}

func newTCPSession(conn net.Conn, sendKey, recvKey []byte) (*tcpSession, error) {
	sendAEAD, err := newAEAD(sendKey)
	if err != nil {
		return nil, err
	}
	recvAEAD, err := newAEAD(recvKey)
	if err != nil {
		return nil, err
	}
	s := &tcpSession{
		conn:     conn,
		sendAEAD: sendAEAD,
		recvAEAD: recvAEAD,
		recvSeq:  1,
		recvCh:   make(chan []byte, 256),
		doneCh:   make(chan error, 1),
	}
	go s.readLoop()
	return s, nil
}

func (s *tcpSession) Kind() string           { return "tcp" }
func (s *tcpSession) Receive() <-chan []byte { return s.recvCh }
func (s *tcpSession) Done() <-chan error     { return s.doneCh }
func (s *tcpSession) Close() error           { s.finish(errors.New("session closed")); return nil }

func (s *tcpSession) Send(payload []byte) error {
	if len(payload) > maxTCPFrame {
		return errors.New("TCP tunnel frame exceeds maximum size")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(tcpWriteTimeout)); err != nil {
		s.finish(err)
		return err
	}
	defer func() { _ = s.conn.SetWriteDeadline(time.Time{}) }()
	s.sendSeq++
	seq := s.sendSeq
	aad := make([]byte, 8)
	binary.BigEndian.PutUint64(aad, seq)
	ciphertext := s.sendAEAD.Seal(nil, nonceFor(seq), payload, aad)
	frameLen := 8 + len(ciphertext)
	header := make([]byte, 4+8)
	binary.BigEndian.PutUint32(header[:4], uint32(frameLen))
	binary.BigEndian.PutUint64(header[4:], seq)

	if err := writeAll(s.conn, header); err != nil {
		s.finish(err)
		return err
	}
	if err := writeAll(s.conn, ciphertext); err != nil {
		s.finish(err)
		return err
	}
	return nil
}

func (s *tcpSession) readLoop() {
	for {
		if err := s.conn.SetReadDeadline(time.Now().Add(tcpReadIdle)); err != nil {
			s.finish(err)
			return
		}
		header := make([]byte, 12)
		if _, err := io.ReadFull(s.conn, header); err != nil {
			s.finish(err)
			return
		}
		frameLen := int(binary.BigEndian.Uint32(header[:4]))
		seq := binary.BigEndian.Uint64(header[4:])
		if frameLen < 8+s.recvAEAD.Overhead() || frameLen > maxTCPFrame+s.recvAEAD.Overhead()+8 {
			s.finish(errors.New("invalid encrypted TCP frame length"))
			return
		}
		if seq != s.recvSeq {
			s.finish(fmt.Errorf("unexpected TCP frame sequence: got %d, want %d", seq, s.recvSeq))
			return
		}
		ciphertext := make([]byte, frameLen-8)
		if _, err := io.ReadFull(s.conn, ciphertext); err != nil {
			s.finish(err)
			return
		}
		aad := make([]byte, 8)
		binary.BigEndian.PutUint64(aad, seq)
		plaintext, err := s.recvAEAD.Open(nil, nonceFor(seq), ciphertext, aad)
		if err != nil {
			s.finish(errors.New("TCP frame authentication failed"))
			return
		}
		s.recvSeq++
		select {
		case s.recvCh <- plaintext:
		case <-s.doneCh:
			return
		}
	}
}

func (s *tcpSession) finish(err error) {
	s.closeOne.Do(func() {
		_ = s.conn.Close()
		s.doneCh <- err
		close(s.doneCh)
	})
}

func dialTCPSession(ctx context.Context, addr, nodeID, peerID string, secret []byte) (Session, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 20 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
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
	if err := writeBlob(conn, hello); err != nil {
		_ = conn.Close()
		return nil, err
	}
	response, err := readBlob(conn, 512)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	welcome, err := decodeHandshake(response, clientNonce, secret)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := validateHandshake(welcome, handshakeWelcome, peerID, nodeID); err != nil {
		_ = conn.Close()
		return nil, err
	}
	keys := deriveKeys(secret, clientNonce, welcome.Nonce, nodeID, peerID)
	_ = conn.SetDeadline(time.Time{})
	session, err := newTCPSession(conn, keys.clientSend, keys.serverSend)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := confirmSessionClient(session); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func acceptTCPSession(conn net.Conn, nodeID, peerID string, secret []byte) (Session, error) {
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}
	request, err := readBlob(conn, 512)
	if err != nil {
		return nil, err
	}
	hello, err := decodeHandshake(request, nil, secret)
	if err != nil {
		return nil, err
	}
	if err := validateHandshake(hello, handshakeHello, peerID, nodeID); err != nil {
		return nil, err
	}
	serverNonce, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	welcomeBytes, err := encodeHandshake(handshake{
		Kind:      handshakeWelcome,
		Timestamp: time.Now().Unix(),
		Nonce:     serverNonce,
		NodeID:    nodeID,
		PeerID:    peerID,
	}, hello.Nonce, secret)
	if err != nil {
		return nil, err
	}
	if err := writeBlob(conn, welcomeBytes); err != nil {
		return nil, err
	}
	keys := deriveKeys(secret, hello.Nonce, serverNonce, peerID, nodeID)
	_ = conn.SetDeadline(time.Time{})
	session, err := newTCPSession(conn, keys.serverSend, keys.clientSend)
	if err != nil {
		return nil, err
	}
	if err := confirmSessionServer(session); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func writeBlob(w io.Writer, data []byte) error {
	if len(data) > 65535 {
		return errors.New("blob is too large")
	}
	header := []byte{byte(len(data) >> 8), byte(len(data))}
	if err := writeAll(w, header); err != nil {
		return err
	}
	return writeAll(w, data)
}

func readBlob(r io.Reader, max int) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	n := int(header[0])<<8 | int(header[1])
	if n <= 0 || n > max {
		return nil, errors.New("invalid blob length")
	}
	data := make([]byte, n)
	_, err := io.ReadFull(r, data)
	return data, err
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
