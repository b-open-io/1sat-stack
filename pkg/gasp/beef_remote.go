// Package gasp provides GASP (Graph Aware Sync Protocol) implementations for topic-based sync.
package gasp

import (
	"context"
	"fmt"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// BeefRemote implements gasp.Remote by reading from local BEEF storage.
// This is used when transaction data is available locally (e.g., from JungleBus dispatcher).
type BeefRemote struct {
	queueKey    []byte        // Store key for initial response queries (optional)
	store       store.Store   // Store for queue operations
	beefStorage *beef.Storage // BEEF transaction storage
}

// NewBeefRemote creates a new BeefRemote for reading from local BEEF storage.
// The queueKey is optional - only needed if using GetInitialResponse for bulk sync.
func NewBeefRemote(beefStorage *beef.Storage, s store.Store, queueKey string) *BeefRemote {
	var key []byte
	if queueKey != "" {
		key = []byte(queueKey)
	}
	return &BeefRemote{
		queueKey:    key,
		store:       s,
		beefStorage: beefStorage,
	}
}

// GetInitialResponse returns UTXOs from the queue as a GASP initial response.
// The queue members are txids scored by block height; we convert them to Output structs.
func (r *BeefRemote) GetInitialResponse(ctx context.Context, request *gasp.InitialRequest) (*gasp.InitialResponse, error) {
	// Query the queue for members with score > since
	scoreRange := store.ScoreRange{
		Min:          &request.Since,
		MinExclusive: true, // Exclude the 'since' value itself
	}
	if request.Limit > 0 {
		scoreRange.Count = int64(request.Limit)
	}

	members, err := r.store.ZRange(ctx, r.queueKey, scoreRange)
	if err != nil {
		return nil, fmt.Errorf("failed to query queue: %w", err)
	}

	utxoList := make([]*gasp.Output, 0, len(members))
	var maxScore float64

	for _, member := range members {
		txid, err := chainhash.NewHashFromHex(string(member.Member))
		if err != nil {
			continue // Skip invalid txids
		}

		// Queue stores txids - we need to load the tx to find outputs
		// For now, we create an output for index 0 as a starting point
		// GASP will discover additional outputs during graph traversal
		utxoList = append(utxoList, &gasp.Output{
			Txid:        *txid,
			OutputIndex: 0,
			Score:       member.Score,
		})

		if member.Score > maxScore {
			maxScore = member.Score
		}
	}

	return &gasp.InitialResponse{
		UTXOList: utxoList,
		Since:    maxScore,
	}, nil
}

// RequestNode loads raw transaction and proof from BEEF storage and returns as a GASP Node.
func (r *BeefRemote) RequestNode(ctx context.Context, graphID, outpoint *transaction.Outpoint, _ bool) (*gasp.Node, error) {
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

// GetInitialReply is not needed for local BEEF sync (unidirectional).
// Returns an empty reply since we're reading from local storage, not syncing with a peer.
func (r *BeefRemote) GetInitialReply(_ context.Context, _ *gasp.InitialResponse) (*gasp.InitialReply, error) {
	return &gasp.InitialReply{UTXOList: []*gasp.Output{}}, nil
}

// SubmitNode is not needed for local BEEF sync (we're reading, not writing).
// Returns an empty response since there's no peer to submit to.
func (r *BeefRemote) SubmitNode(_ context.Context, _ *gasp.Node) (*gasp.NodeResponse, error) {
	return &gasp.NodeResponse{RequestedInputs: map[transaction.Outpoint]*gasp.NodeResponseData{}}, nil
}
