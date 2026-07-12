package transport

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

type keySet struct {
	clientSend []byte
	serverSend []byte
	clientAck  []byte
	serverAck  []byte
}

func deriveKeys(secret, clientNonce, serverNonce []byte, clientID, serverID string) keySet {
	saltInput := make([]byte, 0, len(clientNonce)+len(serverNonce)+len(clientID)+len(serverID))
	saltInput = append(saltInput, clientNonce...)
	saltInput = append(saltInput, serverNonce...)
	saltInput = append(saltInput, clientID...)
	saltInput = append(saltInput, 0)
	saltInput = append(saltInput, serverID...)
	salt := sha256.Sum256(saltInput)
	prk := hkdfExtract(salt[:], secret)
	return keySet{
		clientSend: hkdfExpand(prk, []byte("unknowntunnel/client-to-server/data"), 32),
		serverSend: hkdfExpand(prk, []byte("unknowntunnel/server-to-client/data"), 32),
		clientAck:  hkdfExpand(prk, []byte("unknowntunnel/client-to-server/ack"), 32),
		serverAck:  hkdfExpand(prk, []byte("unknowntunnel/server-to-client/ack"), 32),
	}
}

func hkdfExtract(salt, input []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write(input)
	return mac.Sum(nil)
}

func hkdfExpand(prk, info []byte, length int) []byte {
	var result []byte
	var previous []byte
	for counter := byte(1); len(result) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		_, _ = mac.Write(previous)
		_, _ = mac.Write(info)
		_, _ = mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		result = append(result, previous...)
	}
	return result[:length]
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid AES-256 key length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonceFor(seq uint64) []byte {
	nonce := make([]byte, 12)
	copy(nonce[:4], []byte{'U', 'T', 'N', '1'})
	binary.BigEndian.PutUint64(nonce[4:], seq)
	return nonce
}
