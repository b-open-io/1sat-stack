package p2p

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// RequestNodeBody is the binary body for a REQUEST_NODE message.
//
//	[32 bytes graphID txid][uint32 graphID index]
//	[32 bytes outpoint txid][uint32 outpoint index]
//	[1 byte metadata]
type RequestNodeBody struct {
	GraphID  transaction.Outpoint
	Outpoint transaction.Outpoint
	Metadata bool
}

const requestNodeBodySize = chainhash.HashSize + 4 + chainhash.HashSize + 4 + 1 // 69

func (b *RequestNodeBody) Serialize() []byte {
	buf := make([]byte, 0, requestNodeBodySize)
	buf = append(buf, b.GraphID.Txid[:]...)
	buf = binary.LittleEndian.AppendUint32(buf, b.GraphID.Index)
	buf = append(buf, b.Outpoint.Txid[:]...)
	buf = binary.LittleEndian.AppendUint32(buf, b.Outpoint.Index)
	if b.Metadata {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	return buf
}

func DeserializeRequestNodeBody(data []byte) (*RequestNodeBody, error) {
	if len(data) != requestNodeBodySize {
		return nil, fmt.Errorf("RequestNodeBody: want %d bytes, got %d", requestNodeBodySize, len(data))
	}
	b := &RequestNodeBody{}
	copy(b.GraphID.Txid[:], data[0:32])
	b.GraphID.Index = binary.LittleEndian.Uint32(data[32:36])
	copy(b.Outpoint.Txid[:], data[36:68])
	b.Outpoint.Index = binary.LittleEndian.Uint32(data[68:72])
	b.Metadata = data[72] != 0
	return b, nil
}

// NodeBody is the binary body for a NODE message.
// Used as both RequestNode response and SubmitNode request.
//
//	[32 bytes graphID txid][uint32 graphID index]
//	[uint32 outputIndex]
//	[varint len][rawTx bytes]
//	[varint len][proof bytes]
//	[varint len][txMetadata bytes]
//	[varint len][outputMetadata bytes]
//	[varint input count]
//	  [varint len][hash bytes]  // repeated
type NodeBody struct {
	GraphID        transaction.Outpoint
	OutputIndex    uint32
	RawTx          []byte
	Proof          []byte // empty if unconfirmed
	TxMetadata     []byte
	OutputMetadata []byte
	Inputs         [][]byte // input hash strings as raw bytes
}

func (b *NodeBody) Serialize() []byte {
	size := chainhash.HashSize + 4 + 4 // graphID + outputIndex
	size += varintSize(len(b.RawTx)) + len(b.RawTx)
	size += varintSize(len(b.Proof)) + len(b.Proof)
	size += varintSize(len(b.TxMetadata)) + len(b.TxMetadata)
	size += varintSize(len(b.OutputMetadata)) + len(b.OutputMetadata)
	size += varintSize(len(b.Inputs))
	for _, input := range b.Inputs {
		size += varintSize(len(input)) + len(input)
	}

	buf := make([]byte, 0, size)
	buf = append(buf, b.GraphID.Txid[:]...)
	buf = binary.LittleEndian.AppendUint32(buf, b.GraphID.Index)
	buf = binary.LittleEndian.AppendUint32(buf, b.OutputIndex)
	buf = appendBytes(buf, b.RawTx)
	buf = appendBytes(buf, b.Proof)
	buf = appendBytes(buf, b.TxMetadata)
	buf = appendBytes(buf, b.OutputMetadata)
	buf = binary.AppendUvarint(buf, uint64(len(b.Inputs)))
	for _, input := range b.Inputs {
		buf = appendBytes(buf, input)
	}
	return buf
}

func DeserializeNodeBody(data []byte) (*NodeBody, error) {
	if len(data) < chainhash.HashSize+4+4 {
		return nil, fmt.Errorf("NodeBody too short: need at least %d bytes, got %d", chainhash.HashSize+8, len(data))
	}

	b := &NodeBody{}
	offset := 0

	copy(b.GraphID.Txid[:], data[offset:offset+chainhash.HashSize])
	offset += chainhash.HashSize
	b.GraphID.Index = binary.LittleEndian.Uint32(data[offset:])
	offset += 4
	b.OutputIndex = binary.LittleEndian.Uint32(data[offset:])
	offset += 4

	var err error
	b.RawTx, offset, err = readBytes(data, offset)
	if err != nil {
		return nil, fmt.Errorf("rawTx: %w", err)
	}
	b.Proof, offset, err = readBytes(data, offset)
	if err != nil {
		return nil, fmt.Errorf("proof: %w", err)
	}
	b.TxMetadata, offset, err = readBytes(data, offset)
	if err != nil {
		return nil, fmt.Errorf("txMetadata: %w", err)
	}
	b.OutputMetadata, offset, err = readBytes(data, offset)
	if err != nil {
		return nil, fmt.Errorf("outputMetadata: %w", err)
	}

	inputCount, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, fmt.Errorf("invalid input count varint at offset %d", offset)
	}
	offset += n

	b.Inputs = make([][]byte, inputCount)
	for i := range b.Inputs {
		b.Inputs[i], offset, err = readBytes(data, offset)
		if err != nil {
			return nil, fmt.Errorf("input[%d]: %w", i, err)
		}
	}

	if offset != len(data) {
		return nil, fmt.Errorf("trailing bytes: consumed %d of %d", offset, len(data))
	}
	return b, nil
}

// InitialRequestBody is the binary body for an INITIAL_REQUEST message.
//
//	[uint32 version]
//	[float64 since]
//	[uint32 limit]
type InitialRequestBody struct {
	Version uint32
	Since   float64
	Limit   uint32
}

const initialRequestBodySize = 4 + 8 + 4 // 16

func (b *InitialRequestBody) Serialize() []byte {
	buf := make([]byte, 0, initialRequestBodySize)
	buf = binary.LittleEndian.AppendUint32(buf, b.Version)
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(b.Since))
	buf = binary.LittleEndian.AppendUint32(buf, b.Limit)
	return buf
}

func DeserializeInitialRequestBody(data []byte) (*InitialRequestBody, error) {
	if len(data) != initialRequestBodySize {
		return nil, fmt.Errorf("InitialRequestBody: want %d bytes, got %d", initialRequestBodySize, len(data))
	}
	return &InitialRequestBody{
		Version: binary.LittleEndian.Uint32(data[0:4]),
		Since:   math.Float64frombits(binary.LittleEndian.Uint64(data[4:12])),
		Limit:   binary.LittleEndian.Uint32(data[12:16]),
	}, nil
}

// UTXOEntry is a single entry in a UTXO list.
//
//	[32 bytes txid][uint32 outputIndex][float64 score]
type UTXOEntry struct {
	Txid        chainhash.Hash
	OutputIndex uint32
	Score       float64
}

const utxoEntrySize = chainhash.HashSize + 4 + 8 // 44

// InitialResponseBody is the binary body for an INITIAL_RESPONSE message.
//
//	[float64 since]
//	[varint count][UTXOEntry...]
type InitialResponseBody struct {
	Since    float64
	UTXOList []UTXOEntry
}

func (b *InitialResponseBody) Serialize() []byte {
	size := 8 + varintSize(len(b.UTXOList)) + len(b.UTXOList)*utxoEntrySize
	buf := make([]byte, 0, size)
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(b.Since))
	buf = binary.AppendUvarint(buf, uint64(len(b.UTXOList)))
	for _, u := range b.UTXOList {
		buf = append(buf, u.Txid[:]...)
		buf = binary.LittleEndian.AppendUint32(buf, u.OutputIndex)
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(u.Score))
	}
	return buf
}

func DeserializeInitialResponseBody(data []byte) (*InitialResponseBody, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("InitialResponseBody too short: got %d bytes", len(data))
	}
	b := &InitialResponseBody{}
	b.Since = math.Float64frombits(binary.LittleEndian.Uint64(data[0:8]))
	offset := 8

	b.UTXOList, offset = readUTXOList(data, offset)
	if offset < 0 {
		return nil, fmt.Errorf("InitialResponseBody: invalid UTXO list")
	}
	if offset != len(data) {
		return nil, fmt.Errorf("trailing bytes: consumed %d of %d", offset, len(data))
	}
	return b, nil
}

// InitialReplyBody is the binary body for INITIAL_REPLY_REQ and INITIAL_REPLY_RES messages.
//
//	[varint count][UTXOEntry...]
type InitialReplyBody struct {
	UTXOList []UTXOEntry
}

func (b *InitialReplyBody) Serialize() []byte {
	size := varintSize(len(b.UTXOList)) + len(b.UTXOList)*utxoEntrySize
	buf := make([]byte, 0, size)
	buf = binary.AppendUvarint(buf, uint64(len(b.UTXOList)))
	for _, u := range b.UTXOList {
		buf = append(buf, u.Txid[:]...)
		buf = binary.LittleEndian.AppendUint32(buf, u.OutputIndex)
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(u.Score))
	}
	return buf
}

func DeserializeInitialReplyBody(data []byte) (*InitialReplyBody, error) {
	b := &InitialReplyBody{}
	offset := 0
	b.UTXOList, offset = readUTXOList(data, offset)
	if offset < 0 {
		return nil, fmt.Errorf("InitialReplyBody: invalid UTXO list")
	}
	if offset != len(data) {
		return nil, fmt.Errorf("trailing bytes: consumed %d of %d", offset, len(data))
	}
	return b, nil
}

// NodeResponseBody is the binary body for a NODE_RESPONSE message.
//
//	[varint count]
//	  [32 bytes txid][uint32 index][1 byte metadata]  // repeated
type NodeResponseBody struct {
	RequestedInputs []RequestedInput
}

type RequestedInput struct {
	Outpoint transaction.Outpoint
	Metadata bool
}

const requestedInputSize = chainhash.HashSize + 4 + 1 // 37

func (b *NodeResponseBody) Serialize() []byte {
	size := varintSize(len(b.RequestedInputs)) + len(b.RequestedInputs)*requestedInputSize
	buf := make([]byte, 0, size)
	buf = binary.AppendUvarint(buf, uint64(len(b.RequestedInputs)))
	for _, ri := range b.RequestedInputs {
		buf = append(buf, ri.Outpoint.Txid[:]...)
		buf = binary.LittleEndian.AppendUint32(buf, ri.Outpoint.Index)
		if ri.Metadata {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}
	return buf
}

func DeserializeNodeResponseBody(data []byte) (*NodeResponseBody, error) {
	b := &NodeResponseBody{}
	offset := 0

	count, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, fmt.Errorf("invalid count varint at offset %d", offset)
	}
	offset += n

	needed := int(count) * requestedInputSize
	if offset+needed > len(data) {
		return nil, fmt.Errorf("need %d bytes for %d inputs, got %d", needed, count, len(data)-offset)
	}

	b.RequestedInputs = make([]RequestedInput, count)
	for i := range b.RequestedInputs {
		copy(b.RequestedInputs[i].Outpoint.Txid[:], data[offset:offset+chainhash.HashSize])
		offset += chainhash.HashSize
		b.RequestedInputs[i].Outpoint.Index = binary.LittleEndian.Uint32(data[offset:])
		offset += 4
		b.RequestedInputs[i].Metadata = data[offset] != 0
		offset++
	}

	if offset != len(data) {
		return nil, fmt.Errorf("trailing bytes: consumed %d of %d", offset, len(data))
	}
	return b, nil
}

// helpers

func appendBytes(buf, data []byte) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(data)))
	return append(buf, data...)
}

func readBytes(data []byte, offset int) ([]byte, int, error) {
	length, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, offset, fmt.Errorf("invalid length varint at offset %d", offset)
	}
	offset += n
	if offset+int(length) > len(data) {
		return nil, offset, fmt.Errorf("need %d bytes, got %d", length, len(data)-offset)
	}
	if length == 0 {
		return nil, offset, nil
	}
	result := make([]byte, length)
	copy(result, data[offset:offset+int(length)])
	return result, offset + int(length), nil
}

func readUTXOList(data []byte, offset int) ([]UTXOEntry, int) {
	count, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return nil, -1
	}
	offset += n

	needed := int(count) * utxoEntrySize
	if offset+needed > len(data) {
		return nil, -1
	}

	entries := make([]UTXOEntry, count)
	for i := range entries {
		copy(entries[i].Txid[:], data[offset:offset+chainhash.HashSize])
		offset += chainhash.HashSize
		entries[i].OutputIndex = binary.LittleEndian.Uint32(data[offset:])
		offset += 4
		entries[i].Score = math.Float64frombits(binary.LittleEndian.Uint64(data[offset:]))
		offset += 8
	}
	return entries, offset
}
