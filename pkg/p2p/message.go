package p2p

import (
	"encoding/binary"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
)

const (
	SteakVersion2 = 2
)

// SteakMessage is the P2P broadcast payload for a single overlay topic admission.
// The topic name is implicit (carried by the GossipSub topic).
type SteakMessage struct {
	Version      byte
	Txid         chainhash.Hash
	Instructions overlay.AdmittanceInstructions
	TxSize       uint32 // transaction size in bytes
	PriceSats    uint64 // price quote for GASP fulfillment of this tx
}

// Serialize encodes a SteakMessage into binary wire format:
//
//	[1 byte version = 2]
//	[32 bytes txid]
//	[varint count][uint32 OutputsToAdmit...]
//	[varint count][uint32 CoinsToRetain...]
//	[varint count][uint32 CoinsRemoved...]
//	[varint count][32-byte AncillaryTxids...]
//	[uint32 txSize]
//	[uint64 priceSats]
func (m *SteakMessage) Serialize() []byte {
	size := 1 + chainhash.HashSize // version + txid
	size += varintSize(len(m.Instructions.OutputsToAdmit)) + len(m.Instructions.OutputsToAdmit)*4
	size += varintSize(len(m.Instructions.CoinsToRetain)) + len(m.Instructions.CoinsToRetain)*4
	size += varintSize(len(m.Instructions.CoinsRemoved)) + len(m.Instructions.CoinsRemoved)*4
	size += varintSize(len(m.Instructions.AncillaryTxids)) + len(m.Instructions.AncillaryTxids)*chainhash.HashSize
	size += 4 + 8 // txSize + priceSats

	buf := make([]byte, 0, size)
	buf = append(buf, SteakVersion2)
	buf = append(buf, m.Txid[:]...)
	buf = appendUint32List(buf, m.Instructions.OutputsToAdmit)
	buf = appendUint32List(buf, m.Instructions.CoinsToRetain)
	buf = appendUint32List(buf, m.Instructions.CoinsRemoved)
	buf = appendHashList(buf, m.Instructions.AncillaryTxids)
	buf = binary.LittleEndian.AppendUint32(buf, m.TxSize)
	buf = binary.LittleEndian.AppendUint64(buf, m.PriceSats)

	return buf
}

// DeserializeSteakMessage decodes a binary wire format payload into a SteakMessage.
func DeserializeSteakMessage(data []byte) (*SteakMessage, error) {
	if len(data) < 1+chainhash.HashSize+4+4+8 {
		return nil, fmt.Errorf("message too short: need at least %d bytes, got %d", 1+chainhash.HashSize+16, len(data))
	}

	m := &SteakMessage{}
	m.Version = data[0]
	if m.Version != SteakVersion2 {
		return nil, fmt.Errorf("unsupported version: %d", m.Version)
	}
	offset := 1

	copy(m.Txid[:], data[offset:offset+chainhash.HashSize])
	offset += chainhash.HashSize

	var err error

	m.Instructions.OutputsToAdmit, offset, err = readUint32List(data, offset)
	if err != nil {
		return nil, fmt.Errorf("OutputsToAdmit: %w", err)
	}

	m.Instructions.CoinsToRetain, offset, err = readUint32List(data, offset)
	if err != nil {
		return nil, fmt.Errorf("CoinsToRetain: %w", err)
	}

	m.Instructions.CoinsRemoved, offset, err = readUint32List(data, offset)
	if err != nil {
		return nil, fmt.Errorf("CoinsRemoved: %w", err)
	}

	m.Instructions.AncillaryTxids, offset, err = readHashList(data, offset)
	if err != nil {
		return nil, fmt.Errorf("AncillaryTxids: %w", err)
	}

	if offset+12 > len(data) {
		return nil, fmt.Errorf("payment fields truncated: need 12 bytes, got %d", len(data)-offset)
	}
	m.TxSize = binary.LittleEndian.Uint32(data[offset:])
	offset += 4
	m.PriceSats = binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	if offset != len(data) {
		return nil, fmt.Errorf("trailing bytes: consumed %d of %d", offset, len(data))
	}

	return m, nil
}

func appendUint32List(buf []byte, vals []uint32) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(vals)))
	for _, v := range vals {
		buf = binary.LittleEndian.AppendUint32(buf, v)
	}
	return buf
}

func appendHashList(buf []byte, hashes []*chainhash.Hash) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(hashes)))
	for _, h := range hashes {
		buf = append(buf, h[:]...)
	}
	return buf
}

func readUint32List(data []byte, offset int) ([]uint32, int, error) {
	count, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, offset, fmt.Errorf("invalid varint at offset %d", offset)
	}
	offset += n

	needed := int(count) * 4
	if offset+needed > len(data) {
		return nil, offset, fmt.Errorf("need %d bytes for %d uint32s, got %d", needed, count, len(data)-offset)
	}

	vals := make([]uint32, count)
	for i := range vals {
		vals[i] = binary.LittleEndian.Uint32(data[offset:])
		offset += 4
	}
	return vals, offset, nil
}

func readHashList(data []byte, offset int) ([]*chainhash.Hash, int, error) {
	count, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, offset, fmt.Errorf("invalid varint at offset %d", offset)
	}
	offset += n

	needed := int(count) * chainhash.HashSize
	if offset+needed > len(data) {
		return nil, offset, fmt.Errorf("need %d bytes for %d hashes, got %d", needed, count, len(data)-offset)
	}

	hashes := make([]*chainhash.Hash, count)
	for i := range hashes {
		h := new(chainhash.Hash)
		copy(h[:], data[offset:])
		offset += chainhash.HashSize
		hashes[i] = h
	}
	return hashes, offset, nil
}

func varintSize(n int) int {
	return len(binary.AppendUvarint(nil, uint64(n)))
}
