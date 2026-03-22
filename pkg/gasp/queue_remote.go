// Package gasp provides GASP (Graph Aware Sync Protocol) implementations for queue-based sync.
package gasp

import (
	"context"
	"fmt"
	"strconv"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/redis/go-redis/v9"
)

// QueueGASPRemote implements gasp.Remote by reading from a sorted set queue and BEEF storage
// instead of fetching from a remote HTTP peer. This enables local queue-based GASP sync.
type QueueGASPRemote struct {
	queueKey    string        // Store key for the queue (e.g., "q:tok:{tokenId}")
	redis       *redis.Client // Redis for queue operations
	beefStorage *beef.Storage // BEEF transaction storage
}

// NewQueueGASPRemote creates a new QueueGASPRemote for the given queue key.
func NewQueueGASPRemote(queueKey string, r *redis.Client, beefStorage *beef.Storage) *QueueGASPRemote {
	return &QueueGASPRemote{
		queueKey:    queueKey,
		redis:       r,
		beefStorage: beefStorage,
	}
}

// GetInitialResponse returns UTXOs from the queue as a GASP initial response.
func (r *QueueGASPRemote) GetInitialResponse(ctx context.Context, request *gasp.InitialRequest) (*gasp.InitialResponse, error) {
	opt := &redis.ZRangeBy{
		Min: "(" + strconv.FormatFloat(request.Since, 'f', -1, 64),
		Max: "+inf",
	}
	if request.Limit > 0 {
		opt.Count = int64(request.Limit)
	}

	members, err := r.redis.ZRangeByScoreWithScores(ctx, r.queueKey, opt).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to query queue: %w", err)
	}

	utxoList := make([]*gasp.Output, 0, len(members))
	var maxScore float64

	for _, member := range members {
		txid, err := chainhash.NewHashFromHex(member.Member.(string))
		if err != nil {
			continue
		}

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

	rawTx, err := r.beefStorage.LoadRawTx(ctx, &outpoint.Txid)
	if err != nil {
		return nil, fmt.Errorf("failed to load raw tx %s: %w", outpoint.Txid.String(), err)
	}

	node := &gasp.Node{
		GraphID:     graphID,
		RawTx:       fmt.Sprintf("%x", rawTx),
		OutputIndex: outpoint.Index,
	}

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
func (r *QueueGASPRemote) GetInitialReply(_ context.Context, _ *gasp.InitialResponse) (*gasp.InitialReply, error) {
	return &gasp.InitialReply{UTXOList: []*gasp.Output{}}, nil
}

// SubmitNode is not needed for queue-based sync (we're pulling, not pushing).
func (r *QueueGASPRemote) SubmitNode(_ context.Context, _ *gasp.Node) (*gasp.NodeResponse, error) {
	return &gasp.NodeResponse{RequestedInputs: map[transaction.Outpoint]*gasp.NodeResponseData{}}, nil
}
