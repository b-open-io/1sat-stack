package p2p

import (
	"encoding/binary"
	"fmt"
)

// GASP stream protocol methods
const (
	MethodInitialRequest  byte = 0x01
	MethodInitialResponse byte = 0x02
	MethodInitialReplyReq byte = 0x03
	MethodInitialReplyRes byte = 0x04
	MethodRequestNode     byte = 0x05
	MethodNode            byte = 0x06
	MethodNodeResponse    byte = 0x07
)

// GASP stream protocol status codes
const (
	StatusRequest         byte = 0x00
	StatusOK              byte = 0x01
	StatusError           byte = 0x02
	StatusPaymentRequired byte = 0x03
	StatusNotFound        byte = 0x04
)

// Envelope wraps a GASP message with method, status, topic, and payment fields.
// This is the overlay P2P transport framing — not part of the GASP spec itself.
//
//	[1 byte  version]
//	[1 byte  method]
//	[1 byte  status]
//	[varint  topic len][topic bytes]
//	[uint64  price]
//	[varint  payment len][payment bytes]
//	[varint  body len][body bytes]
type Envelope struct {
	Version byte
	Method  byte
	Status  byte
	Topic   string
	Price   uint64
	Payment []byte
	Body    []byte
}

// Serialize encodes an Envelope into binary wire format.
func (e *Envelope) Serialize() []byte {
	size := 3 + 8 // version + method + status + price
	size += varintSize(len(e.Topic)) + len(e.Topic)
	size += varintSize(len(e.Payment)) + len(e.Payment)
	size += varintSize(len(e.Body)) + len(e.Body)

	buf := make([]byte, 0, size)
	buf = append(buf, e.Version, e.Method, e.Status)
	buf = binary.AppendUvarint(buf, uint64(len(e.Topic)))
	buf = append(buf, e.Topic...)
	buf = binary.LittleEndian.AppendUint64(buf, e.Price)
	buf = binary.AppendUvarint(buf, uint64(len(e.Payment)))
	buf = append(buf, e.Payment...)
	buf = binary.AppendUvarint(buf, uint64(len(e.Body)))
	buf = append(buf, e.Body...)

	return buf
}

// DeserializeEnvelope decodes a binary wire format payload into an Envelope.
func DeserializeEnvelope(data []byte) (*Envelope, error) {
	if len(data) < 3+8 {
		return nil, fmt.Errorf("envelope too short: need at least 11 bytes, got %d", len(data))
	}

	e := &Envelope{
		Version: data[0],
		Method:  data[1],
		Status:  data[2],
	}
	offset := 3

	// Topic
	topicLen, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, fmt.Errorf("invalid topic length varint at offset %d", offset)
	}
	offset += n
	if offset+int(topicLen) > len(data) {
		return nil, fmt.Errorf("topic truncated: need %d bytes, got %d", topicLen, len(data)-offset)
	}
	e.Topic = string(data[offset : offset+int(topicLen)])
	offset += int(topicLen)

	// Price
	if offset+8 > len(data) {
		return nil, fmt.Errorf("price truncated at offset %d", offset)
	}
	e.Price = binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	// Payment
	paymentLen, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, fmt.Errorf("invalid payment length varint at offset %d", offset)
	}
	offset += n
	if offset+int(paymentLen) > len(data) {
		return nil, fmt.Errorf("payment truncated: need %d bytes, got %d", paymentLen, len(data)-offset)
	}
	if paymentLen > 0 {
		e.Payment = make([]byte, paymentLen)
		copy(e.Payment, data[offset:offset+int(paymentLen)])
	}
	offset += int(paymentLen)

	// Body
	bodyLen, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, fmt.Errorf("invalid body length varint at offset %d", offset)
	}
	offset += n
	if offset+int(bodyLen) > len(data) {
		return nil, fmt.Errorf("body truncated: need %d bytes, got %d", bodyLen, len(data)-offset)
	}
	if bodyLen > 0 {
		e.Body = make([]byte, bodyLen)
		copy(e.Body, data[offset:offset+int(bodyLen)])
	}
	offset += int(bodyLen)

	if offset != len(data) {
		return nil, fmt.Errorf("trailing bytes: consumed %d of %d", offset, len(data))
	}

	return e, nil
}
