// Package gasp provides GASP (Graph Aware Sync Protocol) implementations for queue-based sync.
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

// QueueGASPRemote implements gasp.Remote by reading from a sorted set queue and BEEF storage
// instead of fetching from a remote HTTP peer. This enables local queue-based GASP sync.
type QueueGASPRemote struct {
	queueKey    []byte        // Store key for the queue (e.g., "q:tok:{tokenId}")
	store       store.Store   // Store for queue operations
	beefStorage *beef.Storage // BEEF transaction storage
}

// NewQueueGASPRemote creates a new QueueGASPRemote for the given queue key.
func NewQueueGASPRemote(queueKey string, s store.Store, beefStorage *beef.Storage) *QueueGASPRemote {
	return &QueueGASPRemote{
		queueKey:    []byte(queueKey),
		store:       s,
		beefStorage: beefStorage,
	}
}

// GetInitialResponse returns UTXOs from the queue as a GASP initial response.
// The queue members are txids scored by block height; we convert them to Output structs.
func (r *QueueGASPRemote) GetInitialResponse(ctx context.Context, request *gasp.InitialRequest) (*gasp.InitialResponse, error) {
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
func (r *QueueGASPRemote) RequestNode(ctx context.Context, graphID, outpoint *transaction.Outpoint, _ bool) (*gasp.Node, error) {
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

// GetInitialReply is not needed for queue-based sync (unidirectional).
// Returns an empty reply since we're pulling from a local queue, not syncing with a peer.
func (r *QueueGASPRemote) GetInitialReply(_ context.Context, _ *gasp.InitialResponse) (*gasp.InitialReply, error) {
	return &gasp.InitialReply{UTXOList: []*gasp.Output{}}, nil
}

// SubmitNode is not needed for queue-based sync (we're pulling, not pushing).
// Returns an empty response since there's no peer to submit to.
func (r *QueueGASPRemote) SubmitNode(_ context.Context, _ *gasp.Node) (*gasp.NodeResponse, error) {
	return &gasp.NodeResponse{RequestedInputs: map[transaction.Outpoint]*gasp.NodeResponseData{}}, nil
}
