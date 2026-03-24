package wasmparse

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/b-open-io/1sat-stack/proto/parsepb"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"google.golang.org/protobuf/proto"
)

// Module wraps a compiled parse_tx.wasm module and provides a Go-friendly
// interface for calling parse_beef.
type Module struct {
	runtime        wazero.Runtime
	module         api.Module
	alloc          api.Function
	dealloc        api.Function
	parseBtn       api.Function
	parseAtomicBtn api.Function
}

// New compiles and instantiates the parse_tx.wasm module.
func New(ctx context.Context, wasmBytes []byte) (*Module, error) {
	r := wazero.NewRuntime(ctx)

	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	compiled, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("compile wasm: %w", err)
	}

	mod, err := r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("instantiate wasm: %w", err)
	}

	alloc := mod.ExportedFunction("alloc")
	dealloc := mod.ExportedFunction("dealloc")
	parseBeef := mod.ExportedFunction("parse_beef")
	parseAtomicBeef := mod.ExportedFunction("parse_atomic_beef")

	if alloc == nil || dealloc == nil || parseBeef == nil || parseAtomicBeef == nil {
		r.Close(ctx)
		return nil, fmt.Errorf("wasm module missing required exports (alloc, dealloc, parse_beef, parse_atomic_beef)")
	}

	return &Module{
		runtime:        r,
		module:         mod,
		alloc:          alloc,
		dealloc:        dealloc,
		parseBtn:       parseBeef,
		parseAtomicBtn: parseAtomicBeef,
	}, nil
}

// ParseBeef sends raw BEEF bytes to the WASM module and returns the decoded result.
func (m *Module) ParseBeef(ctx context.Context, beefBytes []byte) (*parsepb.BeefParseResult, error) {
	return m.callWasm(ctx, m.parseBtn, beefBytes)
}

// ParseAtomicBeef sends proto-encoded AtomicBeef bytes to the WASM module.
func (m *Module) ParseAtomicBeef(ctx context.Context, protoBytes []byte) (*parsepb.BeefParseResult, error) {
	return m.callWasm(ctx, m.parseAtomicBtn, protoBytes)
}

// ParseTransaction converts a *Transaction to AtomicBeef and parses via WASM.
func (m *Module) ParseTransaction(ctx context.Context, tx *transaction.Transaction) (*parsepb.BeefParseResult, error) {
	atomic, err := TransactionToAtomicBeef(tx)
	if err != nil {
		return nil, fmt.Errorf("convert to atomic beef: %w", err)
	}
	protoBytes, err := proto.Marshal(atomic)
	if err != nil {
		return nil, fmt.Errorf("encode atomic beef: %w", err)
	}
	return m.ParseAtomicBeef(ctx, protoBytes)
}

func (m *Module) callWasm(ctx context.Context, fn api.Function, input []byte) (*parsepb.BeefParseResult, error) {
	defer m.dealloc.Call(ctx) //nolint:errcheck

	results, err := m.alloc.Call(ctx, uint64(len(input)))
	if err != nil {
		return nil, fmt.Errorf("alloc: %w", err)
	}
	ptr := uint32(results[0])
	if ptr == 0 {
		return nil, fmt.Errorf("alloc returned null")
	}

	if !m.module.Memory().Write(ptr, input) {
		return nil, fmt.Errorf("failed to write %d bytes at offset %d", len(input), ptr)
	}

	results, err = fn.Call(ctx, uint64(ptr), uint64(len(input)))
	if err != nil {
		return nil, fmt.Errorf("wasm call: %w", err)
	}
	resultPtr := uint32(results[0])
	if resultPtr == 0 {
		return nil, fmt.Errorf("wasm call returned null")
	}

	lenBytes, ok := m.module.Memory().Read(resultPtr, 4)
	if !ok {
		return nil, fmt.Errorf("failed to read result length at offset %d", resultPtr)
	}
	pbLen := binary.LittleEndian.Uint32(lenBytes)

	pbBytes, ok := m.module.Memory().Read(resultPtr+4, pbLen)
	if !ok {
		return nil, fmt.Errorf("failed to read %d protobuf bytes at offset %d", pbLen, resultPtr+4)
	}

	var result parsepb.BeefParseResult
	if err := proto.Unmarshal(pbBytes, &result); err != nil {
		return nil, fmt.Errorf("decode protobuf: %w", err)
	}

	return &result, nil
}

// Close releases all WASM resources.
func (m *Module) Close(ctx context.Context) error {
	return m.runtime.Close(ctx)
}
