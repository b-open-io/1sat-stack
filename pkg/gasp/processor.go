package gasp

import (
	"context"
	"fmt"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Processor wraps GASP for processing individual outputs with dependency resolution.
// Unlike batch GASP sync, this processes outputs one at a time, handling dependencies
// automatically before submitting to the overlay engine.
type Processor struct {
	topic       string
	beefStorage *beef.Storage
	engine      *engine.Engine
	gasp        *gasp.GASP
	seenNodes   *sync.Map
}

// NewProcessor creates a new GASP processor for the given topic.
func NewProcessor(topic string, beefStorage *beef.Storage, eng *engine.Engine) *Processor {
	p := &Processor{
		topic:       topic,
		beefStorage: beefStorage,
		engine:      eng,
		seenNodes:   &sync.Map{},
	}

	// Create GASP with our custom remote that reads from BEEF storage
	// and overlay storage that submits to the engine
	remote := &processorRemote{beefStorage: beefStorage}
	storage := engine.NewOverlayGASPStorage(topic, eng, nil)

	logPrefix := fmt.Sprintf("[GASP %s] ", topic)
	p.gasp = gasp.NewGASP(gasp.Params{
		Storage:        storage,
		Remote:         remote,
		Unidirectional: true,
		Topic:          topic,
		Concurrency:    8,
		LogPrefix:      &logPrefix,
	})

	return p
}

// ProcessOutput processes a single output with full dependency resolution.
// GASP will automatically fetch and process any required input transactions
// before submitting this output to the overlay engine.
func (p *Processor) ProcessOutput(ctx context.Context, outpoint *transaction.Outpoint) error {
	return p.gasp.ProcessUTXOToCompletion(ctx, outpoint, nil, p.seenNodes)
}

// ProcessTransaction processes all outputs in a transaction for the given token.
// It loads the transaction, finds BSV21 outputs matching the token, and processes each.
func (p *Processor) ProcessTransaction(ctx context.Context, txid *chainhash.Hash, outputIndices []uint32) error {
	for _, vout := range outputIndices {
		outpoint := &transaction.Outpoint{
			Txid:  *txid,
			Index: vout,
		}
		if err := p.ProcessOutput(ctx, outpoint); err != nil {
			return fmt.Errorf("failed to process output %s:%d: %w", txid.String(), vout, err)
		}
	}
	return nil
}

// processorRemote implements gasp.Remote for local BEEF storage access.
// It only needs RequestNode - other methods are no-ops for unidirectional processing.
type processorRemote struct {
	beefStorage *beef.Storage
}

func (r *processorRemote) GetInitialResponse(_ context.Context, _ *gasp.InitialRequest) (*gasp.InitialResponse, error) {
	// Not used for direct processing
	return &gasp.InitialResponse{UTXOList: []*gasp.Output{}}, nil
}

func (r *processorRemote) GetInitialReply(_ context.Context, _ *gasp.InitialResponse) (*gasp.InitialReply, error) {
	return &gasp.InitialReply{UTXOList: []*gasp.Output{}}, nil
}

func (r *processorRemote) RequestNode(ctx context.Context, graphID, outpoint *transaction.Outpoint, _ bool) (*gasp.Node, error) {
	if graphID == nil {
		graphID = outpoint
	}

	// Load raw tx bytes directly - more efficient than parsing full transaction
	rawTx, err := r.beefStorage.LoadRawTx(ctx, &outpoint.Txid)
	if err != nil {
		return nil, fmt.Errorf("failed to load raw tx %s: %w", outpoint.Txid.String(), err)
	}

	node := &gasp.Node{
		GraphID:     graphID,
		RawTx:       fmt.Sprintf("%x", rawTx),
		OutputIndex: outpoint.Index,
	}

	// Load proof separately - may not exist for unconfirmed txs
	proof, err := r.beefStorage.LoadProof(ctx, &outpoint.Txid)
	if err != nil && err != beef.ErrNotFound {
		return nil, fmt.Errorf("failed to load proof %s: %w", outpoint.Txid.String(), err)
	}
	if len(proof) > 0 {
		proofHex := fmt.Sprintf("%x", proof)
		node.Proof = &proofHex
	}

	return node, nil
}

func (r *processorRemote) SubmitNode(_ context.Context, _ *gasp.Node) (*gasp.NodeResponse, error) {
	return &gasp.NodeResponse{RequestedInputs: map[transaction.Outpoint]*gasp.NodeResponseData{}}, nil
}
