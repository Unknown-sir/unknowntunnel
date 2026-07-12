package transport

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	handshakeMagic   = "UKH1"
	handshakeVersion = 1
	handshakeHello   = 1
	handshakeWelcome = 2
	maxClockSkew     = 5 * time.Minute
)

type handshake struct {
	Kind      uint8
	SessionID uint64
	Timestamp int64
	Nonce     []byte
	NodeID    string
	PeerID    string
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

func randomUint64() (uint64, error) {
	b, err := randomBytes(8)
	if err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint64(b)
	if v == 0 {
		v = 1
	}
	return v, nil
}

func encodeHandshake(h handshake, bindNonce, secret []byte) ([]byte, error) {
	if len(h.Nonce) != 32 {
		return nil, errors.New("handshake nonce must be 32 bytes")
	}
	if len(h.NodeID) == 0 || len(h.NodeID) > 64 || len(h.PeerID) == 0 || len(h.PeerID) > 64 {
		return nil, errors.New("invalid handshake node identifiers")
	}
	baseLen := 4 + 1 + 1 + 2 + 8 + 8 + 32 + 1 + 1 + len(h.NodeID) + len(h.PeerID)
	out := make([]byte, baseLen+sha256.Size)
	copy(out[:4], handshakeMagic)
	out[4] = handshakeVersion
	out[5] = h.Kind
	binary.BigEndian.PutUint64(out[8:16], h.SessionID)
	binary.BigEndian.PutUint64(out[16:24], uint64(h.Timestamp))
	copy(out[24:56], h.Nonce)
	out[56] = byte(len(h.NodeID))
	out[57] = byte(len(h.PeerID))
	off := 58
	copy(out[off:], h.NodeID)
	off += len(h.NodeID)
	copy(out[off:], h.PeerID)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(out[:baseLen])
	_, _ = mac.Write(bindNonce)
	copy(out[baseLen:], mac.Sum(nil))
	return out, nil
}

func decodeHandshake(data, bindNonce, secret []byte) (handshake, error) {
	var h handshake
	if len(data) < 58+sha256.Size {
		return h, errors.New("handshake is too short")
	}
	if string(data[:4]) != handshakeMagic || data[4] != handshakeVersion {
		return h, errors.New("invalid handshake header")
	}
	nodeLen := int(data[56])
	peerLen := int(data[57])
	baseLen := 58 + nodeLen + peerLen
	if nodeLen == 0 || peerLen == 0 || nodeLen > 64 || peerLen > 64 || len(data) != baseLen+sha256.Size {
		return h, errors.New("invalid handshake length")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(data[:baseLen])
	_, _ = mac.Write(bindNonce)
	if !hmac.Equal(data[baseLen:], mac.Sum(nil)) {
		return h, errors.New("handshake authentication failed")
	}
	h.Kind = data[5]
	h.SessionID = binary.BigEndian.Uint64(data[8:16])
	h.Timestamp = int64(binary.BigEndian.Uint64(data[16:24]))
	h.Nonce = append([]byte(nil), data[24:56]...)
	off := 58
	h.NodeID = string(data[off : off+nodeLen])
	off += nodeLen
	h.PeerID = string(data[off : off+peerLen])
	return h, nil
}

func validateHandshake(h handshake, wantKind uint8, wantNode, wantPeer string) error {
	if h.Kind != wantKind {
		return fmt.Errorf("unexpected handshake kind %d", h.Kind)
	}
	if h.NodeID != wantNode || h.PeerID != wantPeer {
		return fmt.Errorf("peer identity mismatch: got %q -> %q", h.NodeID, h.PeerID)
	}
	delta := time.Since(time.Unix(h.Timestamp, 0))
	if delta < -maxClockSkew || delta > maxClockSkew {
		return errors.New("peer clock differs by more than five minutes")
	}
	return nil
}

var (
	clientConfirm = []byte("Unknowntunnel client key confirmation v1")
	serverConfirm = []byte("Unknowntunnel server key confirmation v1")
)

func confirmSessionClient(session Session) error {
	if err := session.Send(clientConfirm); err != nil {
		return err
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case data := <-session.Receive():
		if !hmac.Equal(data, serverConfirm) {
			return errors.New("server key confirmation failed")
		}
		return nil
	case <-session.Done():
		return errors.New("session closed during server key confirmation")
	case <-timer.C:
		return errors.New("server key confirmation timed out")
	}
}

func confirmSessionServer(session Session) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case data := <-session.Receive():
		if !hmac.Equal(data, clientConfirm) {
			return errors.New("client key confirmation failed")
		}
		return session.Send(serverConfirm)
	case <-session.Done():
		return errors.New("session closed during client key confirmation")
	case <-timer.C:
		return errors.New("client key confirmation timed out")
	}
}
