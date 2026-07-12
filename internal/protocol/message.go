package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	TypeIPPacket uint8 = iota + 1
	TypeOpenTCP
	TypeTCPStatus
	TypeTCPData
	TypeTCPClose
	TypeUDPData
	TypePing
	TypePong
)

const (
	messageMagic   = "UMSG"
	messageVersion = 1
	maxNameLen     = 255
	maxErrorLen    = 1024
	maxPayloadLen  = 1 << 20
)

type Message struct {
	ID      uint64
	Type    uint8
	ConnID  uint64
	Seq     uint64
	Name    string
	Error   string
	Payload []byte
}

func Encode(m Message) ([]byte, error) {
	if m.Type < TypeIPPacket || m.Type > TypePong {
		return nil, fmt.Errorf("invalid message type %d", m.Type)
	}
	if len(m.Name) > maxNameLen {
		return nil, errors.New("message name is too long")
	}
	if len(m.Error) > maxErrorLen {
		return nil, errors.New("message error is too long")
	}
	if len(m.Payload) > maxPayloadLen {
		return nil, errors.New("message payload is too large")
	}
	buf := bytes.NewBuffer(make([]byte, 0, 40+len(m.Name)+len(m.Error)+len(m.Payload)))
	buf.WriteString(messageMagic)
	buf.WriteByte(messageVersion)
	buf.WriteByte(m.Type)
	_ = binary.Write(buf, binary.BigEndian, uint16(0))
	_ = binary.Write(buf, binary.BigEndian, m.ID)
	_ = binary.Write(buf, binary.BigEndian, m.ConnID)
	_ = binary.Write(buf, binary.BigEndian, m.Seq)
	_ = binary.Write(buf, binary.BigEndian, uint16(len(m.Name)))
	_ = binary.Write(buf, binary.BigEndian, uint16(len(m.Error)))
	_ = binary.Write(buf, binary.BigEndian, uint32(len(m.Payload)))
	buf.WriteString(m.Name)
	buf.WriteString(m.Error)
	buf.Write(m.Payload)
	return buf.Bytes(), nil
}

func Decode(data []byte) (Message, error) {
	var m Message
	if len(data) < 40 {
		return m, io.ErrUnexpectedEOF
	}
	if string(data[:4]) != messageMagic {
		return m, errors.New("invalid message magic")
	}
	if data[4] != messageVersion {
		return m, fmt.Errorf("unsupported message version %d", data[4])
	}
	m.Type = data[5]
	m.ID = binary.BigEndian.Uint64(data[8:16])
	m.ConnID = binary.BigEndian.Uint64(data[16:24])
	m.Seq = binary.BigEndian.Uint64(data[24:32])
	nameLen := int(binary.BigEndian.Uint16(data[32:34]))
	errLen := int(binary.BigEndian.Uint16(data[34:36]))
	payloadLen := int(binary.BigEndian.Uint32(data[36:40]))
	if nameLen > maxNameLen || errLen > maxErrorLen || payloadLen > maxPayloadLen {
		return m, errors.New("message field exceeds limit")
	}
	total := 40 + nameLen + errLen + payloadLen
	if total != len(data) {
		return m, fmt.Errorf("invalid message length: header expects %d, got %d", total, len(data))
	}
	off := 40
	m.Name = string(data[off : off+nameLen])
	off += nameLen
	m.Error = string(data[off : off+errLen])
	off += errLen
	m.Payload = append([]byte(nil), data[off:off+payloadLen]...)
	return m, nil
}
